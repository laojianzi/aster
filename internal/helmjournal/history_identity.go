package helmjournal

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
)

// OpenExisting acquires an existing private Store without creating or repairing
// its directory, permanent lock or WALs. Its owner must Close it on every exit.
func OpenExisting(dir string) (*Store, error) {
	if !validStorePath(dir) {
		return nil, ErrInvalid
	}
	r, err := openExistingPrivateRoot(dir)
	if err != nil {
		return nil, ErrUnavailable
	}
	f, err := r.openFile(lockName, false, false)
	if err != nil {
		r.close()
		return nil, err
	}
	if err = acquireLock(f); err != nil {
		f.Close()
		r.close()
		return nil, err
	}
	if r.check() != nil || r.checkFile(lockName, f) != nil {
		closeAdmission(r, f, true)
		return nil, ErrUnavailable
	}
	return &Store{storeState: &storeState{root: r, lock: f, newSink: func(f *os.File) durableSink { return f }, syncRoot: r.sync}}, nil
}

// HistoryIdentity is inert evidence of the directory, file and bytes actually
// read. It survives closing a reader, but is never a grant or a reusable Store.
// Private fields prevent a caller from retargeting an immutable history view.
type HistoryIdentity struct {
	directory, file os.FileInfo
	id              string
	sum             [sha256.Size]byte
	archived        bool
}

// LoadIdentified retains the checked corrupt prefix, but only a valid complete
// read receives an identity. File identity is obtained from an open handle (not
// a lazily resolved path) so Windows renames cannot change the sampled identity.
func (s *Store) LoadIdentified(id string, archived bool) (Snapshot, *HistoryIdentity, error) {
	if s == nil || s.storeState == nil {
		return Snapshot{}, nil, ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkUsable(); err != nil {
		return Snapshot{}, nil, err
	}
	if !validHex(id, 32) {
		return Snapshot{}, nil, ErrInvalid
	}
	snap, file, sum, err := s.readArchiveRecord(id, archived)
	if err != nil {
		return snap, nil, err
	}
	if archived && !archiveEligible(snap) {
		return snap, nil, ErrCorrupt
	}
	directory, err := s.root.dir.Stat()
	if err != nil || s.checkUsable() != nil {
		return Snapshot{}, nil, ErrUnavailable
	}
	return snap, &HistoryIdentity{directory: directory, file: file, id: id, sum: sum, archived: archived}, nil
}

// PrepareLoadedArchive reuses the existing classification and review. It also
// rejects file or directory replacement since history was loaded, even if every
// WAL byte is identical. Opening a new Store cannot revive a previous consent.
func (s *Store) PrepareLoadedArchive(ctx context.Context, loaded *HistoryIdentity) (*ArchiveReview, error) {
	if loaded == nil || loaded.archived {
		return nil, ErrArchiveConsent
	}
	review, err := s.PrepareArchive(ctx, loaded.id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.checkUsable(); err != nil {
		return nil, err
	}
	directory, err := s.root.dir.Stat()
	if err != nil {
		return nil, ErrUnavailable
	}
	r := review.state
	if !os.SameFile(directory, loaded.directory) || !os.SameFile(r.info, loaded.file) || r.sum != loaded.sum {
		return nil, ErrArchiveConsent
	}
	return review, nil
}

// Inventory is bounded local accounting, not permission to perform any action.
func (s *Store) Inventory(ctx context.Context) (Admission, error) {
	if s == nil || s.storeState == nil {
		return Admission{}, ErrClosed
	}
	if !s.mu.TryLock() {
		return Admission{Reason: AdmissionBusy}, ErrBusy
	}
	defer s.mu.Unlock()
	if err := s.checkUsable(); err != nil {
		return Admission{}, err
	}
	_, out, err := s.inventoryJournal(ctx)
	return out, errors.Join(err, s.checkUsable())
}
