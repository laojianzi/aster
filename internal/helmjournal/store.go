package helmjournal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const lockName = "journal.lock"

func validStorePath(dir string) bool {
	return filepath.IsAbs(dir) && filepath.Clean(dir) == dir && dir != filepath.VolumeName(dir)+string(os.PathSeparator)
}

// Store holds a permanent OS lock until Close. It serializes cooperating
// processes using this directory, not all clients using the same cluster.
// Copies share private lock and lease state. No method repairs, deletes or
// replays a record. Explicit archival preserves the original bytes under a
// separately bounded retained-history name.
type Store struct {
	*storeState
}

type storeState struct {
	mu            sync.Mutex
	root          *privateRoot
	lock          *os.File
	active        *writerState
	closed        bool
	poisoned      bool
	newSink       func(*os.File) durableSink
	syncRoot      func() error
	renameArchive func(string, string) error
}

type durableSink interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

// Writer owns the sole active operation lease. Abort preserves its historical
// file, including any Unknown intent, and releases the lease without a terminal
// frame. Finish records that the attempt ended, not that the server succeeded.
type Writer struct {
	*writerState
}

type writerState struct {
	store         *Store
	id            string
	file          *os.File
	sink          durableSink
	state         journalState
	size          int
	contentDigest [sha256.Size]byte
	closed        bool
}

// Open creates only the final directory if absent. Its parent must exist; an
// existing directory must already satisfy the platform's current-user privacy
// contract. It never changes an existing directory's owner or permissions.
func Open(dir string) (*Store, error) {
	if !validStorePath(dir) {
		return nil, ErrInvalid
	}
	r, err := openPrivateRoot(dir)
	if err != nil {
		return nil, err
	}
	f, err := r.openFile(lockName, false, true)
	if err != nil {
		r.close()
		return nil, err
	}
	if err = acquireLock(f); err != nil {
		f.Close()
		r.close()
		return nil, err
	}
	if err = r.checkFile(lockName, f); err != nil {
		releaseLock(f)
		f.Close()
		r.close()
		return nil, err
	}
	return &Store{storeState: &storeState{root: r, lock: f, newSink: func(f *os.File) durableSink { return f }, syncRoot: r.sync}}, nil
}

func (s *Store) usable() error {
	err := s.checkUsable()
	if err == ErrUnavailable {
		s.poisoned = true
	}
	return err
}

// checkUsable shares the execution predicate without changing lease state.
func (s *Store) checkUsable() error {
	if s == nil || s.storeState == nil || s.closed {
		return ErrClosed
	}
	if s.poisoned {
		return ErrUnavailable
	}
	if s.root.check() != nil || s.root.checkFile(lockName, s.lock) != nil {
		return ErrUnavailable
	}
	return nil
}

// List lists bounded canonical IDs only. Corrupt operation files remain listed
// so callers can request their checked prefix with Load.
func (s *Store) List() ([]string, error) {
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
	ids, _, err := s.inventory()
	return ids, err
}

func (s *Store) inventory() ([]string, int64, error) {
	ids, report, err := s.inventoryAdmission(context.Background())
	if err != nil {
		return nil, 0, err
	}
	return ids, report.Bytes, nil
}

// Load reads a complete bounded file, preserving a fully verified prefix when
// the tail is corrupt. Corrupt is returned on Snapshot alongside ErrCorrupt;
// neither is a network capability. Reads never change the original file.
func (s *Store) Load(id string) (Snapshot, error) {
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
	return s.load(id)
}

func (s *Store) load(id string) (Snapshot, error) {
	f, err := s.root.openFile(id+".wal", false, false)
	if err != nil {
		return Snapshot{}, err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, MaxOperationBytes+1))
	checkErr := s.root.checkFile(id+".wal", f)
	closeErr := f.Close()
	if readErr != nil || closeErr != nil || checkErr != nil {
		clear(data)
		return Snapshot{}, ErrUnavailable
	}
	defer clear(data)
	snapshot := parse(data, id).snapshot
	if snapshot.Corrupt {
		return snapshot, ErrCorrupt
	}
	return snapshot, nil
}

func (s *Store) Begin(p Plan) (*Writer, error) {
	if s == nil || s.storeState == nil {
		return nil, ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return nil, err
	}
	if s.active != nil {
		return nil, ErrBusy
	}
	if !validPlan(p) {
		return nil, ErrInvalid
	}
	if _, err := s.admissionLocked(context.Background(), AdmissionScope{Binding: p.Binding, Namespace: p.Namespace, StorageUID: p.Revision.UID, Release: p.Revision.Release}); err != nil {
		return nil, err
	}
	p = clonePlan(p)
	p.Started, p.Expires = p.Started.UTC(), p.Expires.UTC()
	return s.beginFrame(frame{Version: 1, Sequence: 1, Event: "start", Plan: &p})
}

// beginFrame is the sole allocation, durable write and lease path for both
// payload versions. The caller holds mu and has checked the common admission.
func (s *Store) beginFrame(first frame) (*Writer, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, ErrUnavailable
	}
	id := hex.EncodeToString(random[:])
	if _, err := s.root.root.Lstat(id + archivedSuffix); !os.IsNotExist(err) {
		return nil, ErrUnavailable
	}
	first.ID = id
	encoded, digest, err := encodeFrame(first)
	if err != nil {
		return nil, err
	}
	data := append([]byte(journalMagic), encoded...)
	defer clear(data)
	f, err := s.root.openFile(id+".wal", true, true)
	if err != nil {
		return nil, err
	}
	sink := s.newSink(f)
	if n, e := sink.Write(data); e != nil || n != len(data) {
		s.poisoned = true
		sink.Close()
		return nil, ErrUnavailable
	}
	if sink.Sync() != nil || s.syncRoot() != nil || s.root.checkFile(id+".wal", f) != nil {
		s.poisoned = true
		sink.Close()
		return nil, ErrUnavailable
	}
	state := newState()
	if state.apply(first, digest, id) != nil {
		s.poisoned = true
		f.Close()
		return nil, ErrUnavailable
	}
	// Keep an owned wrapper so assigning to a caller-held Store cannot retarget
	// this operation, while all value copies still share one private lease.
	ownedStore := &Store{storeState: s.storeState}
	w := &Writer{writerState: &writerState{store: ownedStore, id: id, file: f, sink: sink, state: state, size: len(data), contentDigest: sha256.Sum256(data)}}
	s.active = w.writerState
	return w, nil
}

func (w *Writer) ID() string {
	if w == nil || w.writerState == nil {
		return ""
	}
	return w.id
}

func (w *Writer) check() error {
	if w == nil || w.writerState == nil || w.store == nil || w.closed || w.store.active != w.writerState {
		return ErrClosed
	}
	if err := w.store.usable(); err != nil {
		return err
	}
	if w.store.root.checkFile(w.id+".wal", w.file) != nil {
		w.store.poisoned = true
		return ErrUnavailable
	}
	st, err := w.file.Stat()
	if err != nil || st.Size() != int64(w.size) {
		w.store.poisoned = true
		return ErrUnavailable
	}
	return nil
}

func (w *Writer) append(f frame) error {
	if err := w.check(); err != nil {
		return err
	}
	f.Version, f.Sequence, f.Previous = w.state.snapshot.Version, w.state.sequence+1, w.state.digest
	encoded, digest, err := encodeFrame(f)
	if err != nil {
		return err
	}
	defer clear(encoded)
	next := w.state.clone()
	if next.apply(f, digest, w.id) != nil {
		return ErrInvalid
	}
	if w.size+len(encoded) > MaxOperationBytes {
		w.store.poisoned = true
		return ErrBudget
	}
	// Verify the held inode's exact checked prefix before appending. ReadAt does
	// not move the writer's offset; unexpected external changes fail closed.
	prefix := make([]byte, w.size)
	defer clear(prefix)
	if n, err := w.file.ReadAt(prefix, 0); err != nil || n != len(prefix) || sha256.Sum256(prefix) != w.contentDigest {
		w.store.poisoned = true
		return ErrUnavailable
	}
	if n, err := w.sink.Write(encoded); err != nil || n != len(encoded) {
		w.store.poisoned = true
		return ErrUnavailable
	}
	if w.sink.Sync() != nil || w.store.root.checkFile(w.id+".wal", w.file) != nil {
		w.store.poisoned = true
		return ErrUnavailable
	}
	w.state, w.size = next, w.size+len(encoded)
	w.contentDigest = sha256.Sum256(append(prefix, encoded...))
	return nil
}

// Intent must succeed before the caller dispatches the one corresponding HTTP
// mutation. The persisted intent itself is recovered as Unknown until a later
// authoritative result is durably recorded, even if the process died pre-send.
func (w *Writer) Intent(index int, resourceVersion string) error {
	if w == nil || w.writerState == nil || w.store == nil {
		return ErrClosed
	}
	w.store.mu.Lock()
	defer w.store.mu.Unlock()
	if w.state.snapshot.Version != 1 {
		return ErrInvalid
	}
	return w.append(frame{Event: "intent", Index: &index, ResourceVersion: resourceVersion})
}

func (w *Writer) Record(index int, outcome, observedRV string) error {
	if w == nil || w.writerState == nil || w.store == nil {
		return ErrClosed
	}
	w.store.mu.Lock()
	defer w.store.mu.Unlock()
	if err := w.check(); err != nil {
		return err
	}
	if w.state.snapshot.Version != 1 {
		return ErrInvalid
	}
	if index >= 0 && index < len(w.state.snapshot.Effects) && outcome == Completed {
		e := w.state.snapshot.Effects[index]
		if e.Outcome == Completed && e.ObservedResourceVersion == observedRV {
			return nil
		}
	}
	return w.append(frame{Event: "result", Index: &index, Outcome: outcome, ObservedResourceVersion: observedRV})
}

func (w *Writer) Finish(finished time.Time) error {
	if w == nil || w.writerState == nil || w.store == nil {
		return ErrClosed
	}
	w.store.mu.Lock()
	defer w.store.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	finished = finished.UTC()
	event := "finish"
	if w.state.snapshot.Version == 2 {
		event = "install-finish"
	}
	if err := w.append(frame{Event: event, Finished: &finished}); err != nil {
		return err
	}
	return w.close()
}

// Abort is idempotent. It does not manufacture a terminal event and never drops
// a partial file. Close failures poison the store and preserve the fixed error.
func (w *Writer) Abort() error {
	if w == nil || w.writerState == nil || w.store == nil {
		return nil
	}
	w.store.mu.Lock()
	defer w.store.mu.Unlock()
	if w.closed {
		return nil
	}
	return w.close()
}

func (w *Writer) close() error {
	w.closed = true
	if w.store.active == w.writerState {
		w.store.active = nil
	}
	if w.sink.Close() != nil {
		w.store.poisoned = true
		return ErrUnavailable
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.storeState == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.active != nil {
		return ErrBusy
	}
	s.closed = true
	unlockErr := releaseLock(s.lock)
	fileErr := s.lock.Close()
	rootErr := s.root.close()
	if unlockErr != nil || fileErr != nil || rootErr != nil {
		return ErrUnavailable
	}
	return nil
}

type privateRoot struct {
	path string
	dir  *os.File
	root *os.Root
}

func (r *privateRoot) check() error {
	if r == nil || r.dir == nil || r.root == nil {
		return ErrUnavailable
	}
	opened, err := r.dir.Stat()
	if err != nil || validatePrivateFile(r.dir, true) != nil {
		return ErrUnavailable
	}
	current, err := os.Lstat(r.path)
	if err != nil || !current.IsDir() || !os.SameFile(opened, current) {
		return ErrUnavailable
	}
	anchored, err := r.root.Stat(".")
	if err != nil || !os.SameFile(opened, anchored) {
		return ErrUnavailable
	}
	return nil
}

func (r *privateRoot) checkFile(name string, f *os.File) error {
	if r.check() != nil || validatePrivateFile(f, false) != nil {
		return ErrUnavailable
	}
	opened, err := f.Stat()
	if err != nil || name == lockName && opened.Size() != 0 {
		return ErrUnavailable
	}
	current, err := r.root.Lstat(name)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return ErrUnavailable
	}
	return nil
}

func (r *privateRoot) close() error {
	a, b := r.root.Close(), r.dir.Close()
	if a != nil || b != nil {
		return ErrUnavailable
	}
	return nil
}
