package helmjournal

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"sync/atomic"
	"time"
)

const (
	MaxArchivedRecords = 128
	MaxArchivedBytes   = 16 << 20
	ArchiveLifetime    = 30 * time.Second
	archivedSuffix     = ".archived.wal"
)

var ErrArchiveConsent = errors.New("Helm journal archive confirmation is invalid, expired or consumed")

// ArchiveReview is inert, local information. Its private state freezes the exact
// file and open Store, not just a path. Returned projections cannot retarget it.
// It grants neither cluster access nor permission to discard uncertain history.
type ArchiveReview struct{ state *archiveReviewState }
type archiveReviewState struct {
	store     *storeState
	snapshot  Snapshot
	info      os.FileInfo
	bytes     int64
	sum       [sha256.Size]byte
	expires   time.Time
	confirmed atomic.Bool
}

func (r *ArchiveReview) Record() Snapshot {
	if r == nil || r.state == nil {
		return Snapshot{}
	}
	return cloneSnapshot(r.state.snapshot)
}
func (r *ArchiveReview) Bytes() int64 {
	if r == nil || r.state == nil {
		return 0
	}
	return r.state.bytes
}
func (r *ArchiveReview) ExpiresAt() time.Time {
	if r == nil || r.state == nil {
		return time.Time{}
	}
	return r.state.expires
}

// Confirm requires a separate exact canonical record-ID confirmation. Any first
// confirmation attempt consumes this review; callers must review again after an
// incorrect ID or expiry. A copy shares consumption, not an independent grant.
func (r *ArchiveReview) Confirm(id string) (*ArchiveConsent, error) {
	if r == nil || r.state == nil || !r.state.confirmed.CompareAndSwap(false, true) {
		return nil, ErrArchiveConsent
	}
	if id != r.state.snapshot.ID || !time.Now().Before(r.state.expires) {
		return nil, ErrArchiveConsent
	}
	return &ArchiveConsent{state: &archiveConsentState{review: r.state}}, nil
}

// ArchiveConsent permits exactly one local archive attempt. It cannot authorize
// an uninstall, GET, history repair or replay, and is never serialized.
type ArchiveConsent struct{ state *archiveConsentState }
type archiveConsentState struct {
	review  *archiveReviewState
	used    atomic.Bool
	revoked atomic.Bool
}

func (c *ArchiveConsent) Discard() {
	if c != nil && c.state != nil {
		c.state.revoked.Store(true)
	}
}
func (c *archiveConsentState) valid(ctx context.Context) error {
	if _, err := admissionContext(ctx); err != nil {
		return err
	}
	if c.revoked.Load() || !time.Now().Before(c.review.expires) {
		return ErrArchiveConsent
	}
	return nil
}

type ArchiveOutcome string

const (
	ArchiveNotApplied ArchiveOutcome = "NotApplied"
	ArchiveUnknown    ArchiveOutcome = "Unknown"
	ArchiveCompleted  ArchiveOutcome = "Archived"
)

// ArchiveResult distinguishes pre-dispatch refusal from uncertain local storage
// and a checked archive. An error after rename never claims that no move occurred.
// Archived bytes remain retained; quota reclamation is not filesystem deletion.
type ArchiveResult struct {
	RecordID string
	Outcome  ArchiveOutcome
	Bytes    int64
}

func archiveEligible(s Snapshot) bool {
	return s.ID != "" && s.Integrity == IntegrityValid && !s.Corrupt && s.Closed && !unresolved(s)
}

// PrepareArchive reads one terminal, resolved record under the existing lock.
// It creates no file or directory and does not acquire an execution lease.
func (s *Store) PrepareArchive(ctx context.Context, id string) (*ArchiveReview, error) {
	if _, err := admissionContext(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.storeState == nil {
		return nil, ErrClosed
	}
	if !s.mu.TryLock() {
		return nil, ErrBusy
	}
	defer s.mu.Unlock()
	if err := s.checkUsable(); err != nil {
		return nil, err
	}
	if s.active != nil {
		return nil, ErrBusy
	}
	if !validHex(id, 32) {
		return nil, ErrInvalid
	}
	started := time.Now()
	_, inventory, err := s.inventoryJournal(ctx)
	if err != nil {
		return nil, err
	}
	snap, info, sum, err := s.readArchiveRecord(id, false)
	if err != nil {
		return nil, err
	}
	if !archiveEligible(snap) {
		return nil, ErrUnresolved
	}
	if inventory.ArchivedRecords >= MaxArchivedRecords || inventory.ArchivedBytes+info.Size() > MaxArchivedBytes {
		return nil, ErrBudget
	}
	if _, err := admissionContext(ctx); err != nil {
		return nil, err
	}
	if err := s.checkUsable(); err != nil {
		return nil, err
	}
	expires := started.Add(ArchiveLifetime)
	if !time.Now().Before(expires) {
		return nil, ErrArchiveConsent
	}
	return &ArchiveReview{state: &archiveReviewState{store: s.storeState, snapshot: snap, info: info,
		bytes: info.Size(), sum: sum, expires: expires}}, nil
}

// Archive consumes the original independent consent before any validation or
// filesystem access. It only renames the exact reviewed WAL, without replacement,
// within this private directory. No fallback copy/delete or automatic retry is
// allowed. Storage errors after dispatch poison this open Store; reopen and
// explicitly inspect active/archived history rather than replaying the grant.
func (s *Store) Archive(ctx context.Context, consent *ArchiveConsent) (ArchiveResult, error) {
	out := ArchiveResult{Outcome: ArchiveNotApplied}
	if consent == nil || consent.state == nil || !consent.state.used.CompareAndSwap(false, true) {
		return out, ErrArchiveConsent
	}
	c := consent.state
	review := c.review
	out.RecordID, out.Bytes = review.snapshot.ID, review.bytes
	if err := c.valid(ctx); err != nil {
		return out, err
	}
	if s == nil || s.storeState == nil || s.storeState != review.store {
		return out, ErrArchiveConsent
	}
	if !s.mu.TryLock() {
		return out, ErrBusy
	}
	defer s.mu.Unlock()
	if err := s.checkUsable(); err != nil {
		return out, err
	}
	if s.active != nil {
		return out, ErrBusy
	}
	_, inventory, err := s.inventoryJournal(ctx)
	if err != nil {
		return out, err
	}
	if inventory.ArchivedRecords >= MaxArchivedRecords || inventory.ArchivedBytes+review.bytes > MaxArchivedBytes {
		return out, ErrBudget
	}
	snap, info, sum, err := s.readArchiveRecord(review.snapshot.ID, false)
	if err != nil {
		return out, err
	}
	if !archiveEligible(snap) {
		return out, ErrUnresolved
	}
	if !os.SameFile(info, review.info) || info.Size() != review.bytes || sum != review.sum || snap.Digest != review.snapshot.Digest {
		return out, ErrArchiveConsent
	}
	if err := s.checkUsable(); err != nil {
		return out, err
	}
	if err := c.valid(ctx); err != nil {
		return out, err
	}
	old, new := out.RecordID+".wal", out.RecordID+archivedSuffix
	// From here even an OS error is conservative Unknown: a storage failure or
	// lost response must never cause a retry to overwrite either historical name.
	out.Outcome = ArchiveUnknown
	rename := s.renameArchive
	if rename == nil {
		rename = s.root.renameNoReplace
	}
	if rename(old, new) != nil {
		s.poisoned = true
		return out, ErrUnavailable
	}
	// Verification after dispatch is not another state-changing operation. It
	// must finish even when cancellation happens immediately after the move.
	archived, after, afterSum, checkErr := s.readArchiveRecord(out.RecordID, true)
	_, oldErr := s.root.root.Lstat(old)
	if checkErr != nil || !archiveEligible(archived) || after == nil || !os.SameFile(review.info, after) ||
		afterSum != review.sum || !errors.Is(oldErr, os.ErrNotExist) || s.syncRoot() != nil || s.checkUsable() != nil {
		s.poisoned = true
		return out, ErrUnavailable
	}
	out.Outcome = ArchiveCompleted
	// Preserve a verified effect alongside late cancellation/revocation.
	return out, c.valid(ctx)
}

// ListArchived lists only bounded canonical IDs after validating every retained
// archive. It does not hide corruption behind an ignored filename extension.
func (s *Store) ListArchived() ([]string, error) {
	if s == nil || s.storeState == nil {
		return nil, ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if s.root.check() != nil || s.root.checkFile(lockName, s.lock) != nil {
		return nil, ErrUnavailable
	}
	inv, _, err := s.inventoryJournal(context.Background())
	if err != nil {
		return nil, err
	}
	return inv.archived, nil
}

// LoadArchived returns the original checked snapshot without moving it back to
// the active namespace, mutating it, or granting any network authority.
func (s *Store) LoadArchived(id string) (Snapshot, error) {
	if s == nil || s.storeState == nil {
		return Snapshot{}, ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Snapshot{}, ErrClosed
	}
	if !validHex(id, 32) {
		return Snapshot{}, ErrInvalid
	}
	if s.root.check() != nil || s.root.checkFile(lockName, s.lock) != nil {
		return Snapshot{}, ErrUnavailable
	}
	snap, _, _, err := s.readArchiveRecord(id, true)
	if err == nil && !archiveEligible(snap) {
		return snap, ErrCorrupt
	}
	return snap, err
}

func (s *Store) readArchiveRecord(id string, archived bool) (Snapshot, os.FileInfo, [sha256.Size]byte, error) {
	name := id + ".wal"
	if archived {
		name = id + archivedSuffix
	}
	var sum [sha256.Size]byte
	f, err := s.root.openFile(name, false, false)
	if err != nil {
		return Snapshot{}, nil, sum, err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, MaxOperationBytes+1))
	defer clear(data)
	info, statErr := f.Stat()
	checkErr, closeErr := s.root.checkFile(name, f), f.Close()
	if readErr != nil || statErr != nil || checkErr != nil || closeErr != nil || info.Size() != int64(len(data)) {
		return Snapshot{}, nil, sum, ErrUnavailable
	}
	snap := parse(data, id).snapshot
	if snap.Corrupt {
		return snap, info, sum, ErrCorrupt
	}
	return snap, info, sha256.Sum256(data), nil
}
