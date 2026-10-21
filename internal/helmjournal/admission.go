package helmjournal

import (
	"context"
	"errors"
	"os"

	"github.com/laojianzi/aster/internal/helmrelease"
)

// AdmissionScope is the exact local guard key used by Begin. It is not proof
// of cluster identity, a prepared operation, or authority to read or mutate it.
type AdmissionScope struct {
	Binding    string
	Namespace  string
	StorageUID string
}

// Only existing-only native opens may return this internal distinction. The
// public API keeps fixed journal errors and never exposes an OS path error.
var errStoreNotInitialized = errors.New("private Helm recovery journal is not initialized")

type AdmissionReason string

const (
	AdmissionNoBlocker      AdmissionReason = "NoBlocker"
	AdmissionNotInitialized AdmissionReason = "NotInitialized"
	AdmissionBusy           AdmissionReason = "Busy"
	AdmissionClosed         AdmissionReason = "Closed"
	AdmissionInvalid        AdmissionReason = "Invalid"
	AdmissionUnavailable    AdmissionReason = "Unavailable"
	AdmissionCorrupt        AdmissionReason = "Corrupt"
	AdmissionRecordLimit    AdmissionReason = "RecordLimit"
	AdmissionOperationBytes AdmissionReason = "OperationBytes"
	AdmissionStoreBytes     AdmissionReason = "StoreBytes"
	AdmissionReserveBytes   AdmissionReason = "ReserveBytes"
	AdmissionUnresolved     AdmissionReason = "Unresolved"
	AdmissionCancelled      AdmissionReason = "Cancelled"
	AdmissionArchiveLimit   AdmissionReason = "ArchiveLimit"
)

type UnresolvedKind string

const (
	UnresolvedUnclosed UnresolvedKind = "Unclosed"
	UnresolvedUnknown  UnresolvedKind = "Unknown"
	UnresolvedPending  UnresolvedKind = "Pending"
	UnresolvedPartial  UnresolvedKind = "Partial"
)

// Admission explains the first observed local barrier. Counts are usable only
// when InventoryComplete is true; that flag does not assert that every WAL was
// parsed. NoBlocker means the bounded check completed without finding a barrier,
// never that a later execution is permitted. Begin always checks again.
type Admission struct {
	Reason            AdmissionReason
	RecordID          string
	Records           int
	Bytes             int64
	InventoryComplete bool
	ArchivedRecords   int
	ArchivedBytes     int64
	UnresolvedKind    UnresolvedKind
}

func validAdmissionScope(scope AdmissionScope) bool {
	return validHex(scope.Binding, 64) && helmrelease.ValidNamespace(scope.Namespace) && validIdentity(scope.StorageUID, 128)
}

func admissionContext(ctx context.Context) (AdmissionReason, error) {
	if ctx == nil {
		return AdmissionInvalid, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return AdmissionCancelled, err
	}
	return "", nil
}

func admissionError(err error) AdmissionReason {
	switch {
	case errors.Is(err, ErrClosed):
		return AdmissionClosed
	case errors.Is(err, ErrBusy):
		return AdmissionBusy
	case errors.Is(err, ErrInvalid):
		return AdmissionInvalid
	case errors.Is(err, ErrCorrupt):
		return AdmissionCorrupt
	default:
		return AdmissionUnavailable
	}
}

// ReviewAdmission reads only an existing private directory and its existing
// permanent lock. Missing storage is uninitialized, not an empty usable store.
// It never creates or repairs a directory, lock, WAL, permission, or authority.
// The temporary nonblocking OS lock is released before this function returns.
func ReviewAdmission(ctx context.Context, dir string, scope AdmissionScope) (out Admission, err error) {
	if reason, contextErr := admissionContext(ctx); contextErr != nil {
		return Admission{Reason: reason}, contextErr
	}
	if !validStorePath(dir) || !validAdmissionScope(scope) {
		return Admission{Reason: AdmissionInvalid}, ErrInvalid
	}
	r, openErr := openExistingPrivateRoot(dir)
	if openErr != nil {
		if errors.Is(openErr, errStoreNotInitialized) {
			return Admission{Reason: AdmissionNotInitialized}, ErrUnavailable
		}
		return Admission{Reason: admissionError(openErr)}, openErr
	}
	var lock *os.File
	locked := false
	defer func() {
		out, err = finishAdmission(ctx, r, lock, locked, out, err)
	}()
	if reason, contextErr := admissionContext(ctx); contextErr != nil {
		return Admission{Reason: reason}, contextErr
	}
	if _, statErr := r.root.Lstat(lockName); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return Admission{Reason: AdmissionNotInitialized}, ErrUnavailable
		}
		return Admission{Reason: AdmissionUnavailable}, ErrUnavailable
	}
	// No create or write access: LockFileEx accepts GENERIC_READ handles, just
	// as flock accepts a read-only descriptor on the supported Unix platforms.
	lock, err = r.openFile(lockName, false, false)
	if err != nil {
		return Admission{Reason: admissionError(err)}, err
	}
	if err = acquireLock(lock); err != nil {
		return Admission{Reason: admissionError(err)}, err
	}
	locked = true
	s := &Store{storeState: &storeState{root: r, lock: lock}}
	return s.ReviewAdmission(ctx, scope)
}

func finishAdmission(ctx context.Context, root *privateRoot, lock *os.File, locked bool, out Admission, err error) (Admission, error) {
	if closeErr := closeAdmission(root, lock, locked); closeErr != nil {
		if out.Reason == AdmissionNoBlocker {
			out.Reason = AdmissionUnavailable
		}
		err = errors.Join(err, closeErr)
	}
	if reason, contextErr := admissionContext(ctx); contextErr != nil {
		out.Reason = reason
		err = errors.Join(err, contextErr)
	}
	return out, err
}

func closeAdmission(root *privateRoot, lock *os.File, locked bool) error {
	failed := false
	if locked && releaseLock(lock) != nil {
		failed = true
	}
	if lock != nil && lock.Close() != nil {
		failed = true
	}
	if root.close() != nil {
		failed = true
	}
	if failed {
		return ErrUnavailable
	}
	return nil
}

// ReviewAdmission inspects the same policy as Begin without changing this
// handle's lease or poisoned state. A busy mutex returns immediately, including
// while a writer is awaiting durable storage. It never grants a writer lease.
func (s *Store) ReviewAdmission(ctx context.Context, scope AdmissionScope) (Admission, error) {
	if reason, err := admissionContext(ctx); err != nil {
		return Admission{Reason: reason}, err
	}
	if s == nil || s.storeState == nil {
		return Admission{Reason: AdmissionClosed}, ErrClosed
	}
	if !s.mu.TryLock() {
		return Admission{Reason: AdmissionBusy}, ErrBusy
	}
	defer s.mu.Unlock()
	if err := s.checkUsable(); err != nil {
		return Admission{Reason: admissionError(err)}, err
	}
	if s.active != nil {
		return Admission{Reason: AdmissionBusy}, ErrBusy
	}
	if !validAdmissionScope(scope) {
		return Admission{Reason: AdmissionInvalid}, ErrInvalid
	}
	out, err := s.admissionLocked(ctx, scope)
	if err == nil {
		if checkErr := s.checkUsable(); checkErr != nil {
			out.Reason = admissionError(checkErr)
			return out, checkErr
		}
	}
	return out, err
}

// admissionLocked is shared by the read-only explanation and every Begin.
// The caller has checked handle usability, the active lease, and its input.
// Preserve the original inventory/count/ordered-record/reservation priority.
func (s *Store) admissionLocked(ctx context.Context, scope AdmissionScope) (Admission, error) {
	ids, out, err := s.inventoryAdmission(ctx)
	if err != nil {
		return out, err
	}
	if len(ids) >= MaxRecords {
		out.Reason = AdmissionRecordLimit
		return out, ErrBudget
	}
	for _, id := range ids {
		if reason, contextErr := admissionContext(ctx); contextErr != nil {
			out.Reason = reason
			return out, contextErr
		}
		old, loadErr := s.load(id)
		if loadErr != nil {
			out.Reason, out.RecordID = admissionError(loadErr), id
			return out, loadErr
		}
		if reason, contextErr := admissionContext(ctx); contextErr != nil {
			out.Reason = reason
			return out, contextErr
		}
		if old.Plan.Binding == scope.Binding && old.Plan.Namespace == scope.Namespace && old.Plan.Revision.UID == scope.StorageUID {
			if blocked, kind := explainUnresolved(old); blocked {
				out.Reason, out.RecordID, out.UnresolvedKind = AdmissionUnresolved, id, kind
				return out, ErrUnresolved
			}
		}
	}
	if reason, contextErr := admissionContext(ctx); contextErr != nil {
		out.Reason = reason
		return out, contextErr
	}
	// Reserve the full maximum operation space, not merely the first frame.
	// A successful check still cannot promise a subsequent write or Sync.
	if out.Bytes+MaxOperationBytes > MaxStoreBytes {
		out.Reason = AdmissionReserveBytes
		return out, ErrBudget
	}
	return out, nil
}

// inventoryAdmission shares List's original bounded inventory checks. A partial
// scan never publishes a verified zero count/size or an arbitrary entry name.
func (s *Store) inventoryAdmission(ctx context.Context) ([]string, Admission, error) {
	inv, out, err := s.inventoryJournal(ctx)
	if err != nil {
		return nil, out, err
	}
	return inv.active, out, nil
}
