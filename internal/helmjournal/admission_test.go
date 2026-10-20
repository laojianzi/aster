package helmjournal

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
)

func admissionScope(p Plan) AdmissionScope {
	return AdmissionScope{Binding: p.Binding, Namespace: p.Namespace, StorageUID: p.Revision.UID}
}

type admissionFileFact struct {
	info os.FileInfo
	hash [sha256.Size]byte
}

func admissionFiles(t *testing.T, dir string, ownedLock ...*os.File) map[string]admissionFileFact {
	t.Helper()
	if len(ownedLock) > 1 || len(ownedLock) == 1 && ownedLock[0] == nil {
		t.Fatal("invalid snapshot lock handle")
	}
	entries, err := os.ReadDir(dir)
	mustJournal(t, err)
	out := make(map[string]admissionFileFact, len(entries)+1)
	st, err := os.Stat(dir)
	mustJournal(t, err)
	out["."] = admissionFileFact{info: st}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		st, err := os.Lstat(path)
		mustJournal(t, err)
		fact := admissionFileFact{info: st}
		if st.Mode().IsRegular() {
			// A zero-length file has exactly the empty byte sequence. Windows
			// denies ReadFile on the empty permanent lock while its mandatory
			// byte-range lock is held; stat still verifies its size and identity.
			fact.hash = sha256.Sum256(nil)
			if st.Size() != 0 {
				var data []byte
				if entry.Name() == lockName && len(ownedLock) == 1 {
					// A deliberately damaged nonempty lock must still be read in
					// full. Windows allows its owner handle to read the locked
					// range, but refuses a second handle, even in this process.
					lockedInfo, statErr := ownedLock[0].Stat()
					mustJournal(t, statErr)
					if !os.SameFile(st, lockedInfo) || st.Size() != lockedInfo.Size() || st.Mode() != lockedInfo.Mode() {
						t.Fatal("snapshot lock handle does not match the directory entry")
					}
					data = make([]byte, st.Size())
					var n int
					n, err = ownedLock[0].ReadAt(data, 0)
					if n != len(data) {
						t.Fatal("snapshot did not read the complete damaged lock")
					}
				} else {
					data, err = os.ReadFile(path)
				}
				mustJournal(t, err)
				fact.hash = sha256.Sum256(data)
			}
		}
		out[entry.Name()] = fact
	}
	return out
}

func unchangedAdmissionFiles(t *testing.T, dir string, before map[string]admissionFileFact, ownedLock ...*os.File) {
	t.Helper()
	after := admissionFiles(t, dir, ownedLock...)
	if len(before) != len(after) {
		t.Fatal("read-only review added or removed a directory entry")
	}
	for name, old := range before {
		now, ok := after[name]
		if !ok {
			t.Fatal("read-only review lost an original directory entry")
		}
		// Directory metadata can settle after fixture creation on a host-backed
		// filesystem, even during an in-memory-only parsing control. Assert the
		// actual directory entries/identity/mode/size and every file's bytes and
		// modification time, rather than a directory timestamp projection.
		mtimeChanged := !old.info.IsDir() && !old.info.ModTime().Equal(now.info.ModTime())
		if !os.SameFile(old.info, now.info) || old.info.Mode() != now.info.Mode() || old.info.Size() != now.info.Size() || mtimeChanged || old.hash != now.hash {
			t.Fatalf("read-only review changed %q: same inode %v, mode %v/%v, size %d/%d, modification time %v/%v, same bytes %v",
				name, os.SameFile(old.info, now.info), old.info.Mode(), now.info.Mode(), old.info.Size(), now.info.Size(),
				old.info.ModTime(), now.info.ModTime(), old.hash == now.hash)
		}
	}
}

func requireAdmission(t *testing.T, got Admission, err error, reason AdmissionReason, wantErr error) {
	t.Helper()
	if got.Reason != reason || !errors.Is(err, wantErr) {
		t.Fatalf("admission got reason %s / %v, want %s / %v", got.Reason, err, reason, wantErr)
	}
	if got.RecordID != "" && !validHex(got.RecordID, 32) {
		t.Fatal("admission exposed a noncanonical record identity")
	}
}

func TestHelmJournalAdmissionReadOnly(t *testing.T) {
	s, dir := testStore(t)
	p := testPlan()
	w := testBegin(t, s, p)
	mustJournal(t, w.Finish(time.Now()))
	before := admissionFiles(t, dir)
	// A read-only review must not reach either write/synchronization factory.
	s.newSink = func(*os.File) durableSink { t.Fatal("review created a writer"); return nil }
	s.syncRoot = func() error { t.Fatal("review synchronized storage"); return ErrUnavailable }
	got, err := s.ReviewAdmission(context.Background(), admissionScope(p))
	requireAdmission(t, got, err, AdmissionNoBlocker, nil)
	if !got.InventoryComplete || got.Records != 1 || got.Bytes != before[w.ID()+".wal"].info.Size() || got.RecordID != "" || s.active != nil || s.poisoned {
		t.Fatal("read-only projection or handle state changed")
	}
	unchangedAdmissionFiles(t, dir, before)
	mustJournal(t, s.Close())
	got, err = ReviewAdmission(context.Background(), dir, admissionScope(p))
	requireAdmission(t, got, err, AdmissionNoBlocker, nil)
	unchangedAdmissionFiles(t, dir, before)
	// A second normal owner proves that the transient review released its lock.
	next, err := Open(dir)
	mustJournal(t, err)
	mustJournal(t, next.Close())
}

func TestHelmJournalAdmissionHistoryPolicy(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind UnresolvedKind
	}{
		{"unclosed-not-started", UnresolvedUnclosed},
		{"unclosed-completed", UnresolvedUnclosed},
		{"closed-not-started", ""},
		{"closed-rejected", ""},
		{"closed-unknown", UnresolvedUnknown},
		{"closed-pending", UnresolvedPending},
		{"closed-partial", UnresolvedPartial},
		{"closed-completed", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, dir := testStore(t)
			p := testPlan()
			w := testBegin(t, s, p)
			switch tc.name {
			case "unclosed-completed", "closed-completed":
				allCompleted(t, w)
			case "closed-partial":
				beginCompleted(t, w)
				mustJournal(t, w.Intent(1, "21"))
				mustJournal(t, w.Record(1, Rejected, ""))
			case "closed-rejected", "closed-unknown", "closed-pending":
				mustJournal(t, w.Intent(0, "7"))
				outcome, rv := Rejected, ""
				if tc.name == "closed-unknown" {
					outcome = Unknown
				} else if tc.name == "closed-pending" {
					outcome, rv = AcceptedPending, "8"
				}
				mustJournal(t, w.Record(0, outcome, rv))
			}
			if strings.HasPrefix(tc.name, "unclosed-") {
				mustJournal(t, w.Abort())
			} else {
				mustJournal(t, w.Finish(time.Now()))
			}
			before := admissionFiles(t, dir)
			got, err := s.ReviewAdmission(context.Background(), admissionScope(p))
			if tc.kind != "" {
				requireAdmission(t, got, err, AdmissionUnresolved, ErrUnresolved)
				if got.RecordID != w.ID() || got.UnresolvedKind != tc.kind {
					t.Fatal("historical blocking record/reason was lost")
				}
			} else {
				requireAdmission(t, got, err, AdmissionNoBlocker, nil)
				if got.RecordID != "" || got.UnresolvedKind != "" {
					t.Fatal("historical no-effect or complete attempt was reported as unresolved")
				}
			}
			if !got.InventoryComplete || got.Records != 1 {
				t.Fatal("complete inventory was not identified")
			}
			unchangedAdmissionFiles(t, dir, before)
			// Exercise the public writer entry point, not a duplicate predicate.
			next, beginErr := s.Begin(p)
			if !errors.Is(beginErr, err) {
				t.Fatal("read-only review disagreed with actual Begin policy")
			}
			if next != nil {
				mustJournal(t, next.Abort())
			} else {
				unchangedAdmissionFiles(t, dir, before)
			}
		})
	}
}

func TestHelmJournalAdmissionExactScope(t *testing.T) {
	for _, name := range []string{"binding", "namespace", "storage-uid", "resource-version", "preparation-id"} {
		t.Run(name, func(t *testing.T) {
			s, dir := testStore(t)
			p := testPlan()
			w := testBegin(t, s, p)
			mustJournal(t, w.Abort())
			next := clonePlan(p)
			switch name {
			case "binding":
				next.Binding = strings.Repeat("c", 64)
			case "namespace":
				next.Namespace = "other"
				for i := range next.Targets {
					next.Targets[i].Namespace = next.Namespace
				}
			case "storage-uid":
				next.Revision.UID = "replacement-uid"
			case "resource-version":
				next.Revision.ResourceVersion = "new-rv"
			case "preparation-id":
				next.PreparationID = strings.Repeat("d", 32)
			}
			before := admissionFiles(t, dir)
			got, err := s.ReviewAdmission(context.Background(), admissionScope(next))
			if name == "resource-version" || name == "preparation-id" {
				requireAdmission(t, got, err, AdmissionUnresolved, ErrUnresolved)
			} else {
				requireAdmission(t, got, err, AdmissionNoBlocker, nil)
			}
			unchangedAdmissionFiles(t, dir, before)
			nextWriter, beginErr := s.Begin(next)
			if !errors.Is(beginErr, err) {
				t.Fatal("guard scope differed between review and Begin")
			}
			if nextWriter != nil {
				mustJournal(t, nextWriter.Abort())
			}
		})
	}
}

func TestHelmJournalAdmissionIntegrity(t *testing.T) {
	t.Run("unrelated-corrupt-record", func(t *testing.T) {
		s, dir := testStore(t)
		p := testPlan()
		w := testBegin(t, s, p)
		mustJournal(t, w.Finish(time.Now()))
		f, err := os.OpenFile(filepath.Join(dir, w.ID()+".wal"), os.O_WRONLY|os.O_APPEND, 0600)
		mustJournal(t, err)
		_, err = f.Write([]byte{0, 1})
		mustJournal(t, err)
		mustJournal(t, f.Close())
		p.Binding, p.Revision.UID = strings.Repeat("e", 64), "other-storage"
		before := admissionFiles(t, dir)
		got, err := s.ReviewAdmission(context.Background(), admissionScope(p))
		requireAdmission(t, got, err, AdmissionCorrupt, ErrCorrupt)
		if !got.InventoryComplete || got.RecordID != w.ID() || got.UnresolvedKind != "" {
			t.Fatal("unrelated corrupt WAL was ignored or interpreted as complete history")
		}
		if _, err := s.Begin(p); !errors.Is(err, ErrCorrupt) {
			t.Fatal("Begin bypassed global integrity barrier")
		}
		unchangedAdmissionFiles(t, dir, before)
	})
	t.Run("invalid-directory-entry", func(t *testing.T) {
		s, dir := testStore(t)
		mustJournal(t, os.WriteFile(filepath.Join(dir, "PRIVATE_FOREIGN_ENTRY"), []byte("private"), 0600))
		before := admissionFiles(t, dir)
		got, err := s.ReviewAdmission(context.Background(), admissionScope(testPlan()))
		requireAdmission(t, got, err, AdmissionCorrupt, ErrCorrupt)
		if got.InventoryComplete || got.RecordID != "" || strings.Contains(fmt.Sprint(got, err), "PRIVATE") || strings.Contains(err.Error(), dir) {
			t.Fatal("invalid entry leaked details or claimed verified inventory")
		}
		unchangedAdmissionFiles(t, dir, before)
	})
	t.Run("changed-lock-no-poison", func(t *testing.T) {
		s, dir := testStore(t)
		f, err := os.OpenFile(filepath.Join(dir, lockName), os.O_WRONLY, 0600)
		mustJournal(t, err)
		_, err = f.WriteAt([]byte{'!'}, 1)
		mustJournal(t, err)
		mustJournal(t, f.Close())
		before := admissionFiles(t, dir, s.lock)
		if before[lockName].hash != sha256.Sum256([]byte{0, '!'}) {
			t.Fatal("fixture did not preserve the complete damaged lock bytes")
		}
		got, err := s.ReviewAdmission(context.Background(), admissionScope(testPlan()))
		requireAdmission(t, got, err, AdmissionUnavailable, ErrUnavailable)
		if s.poisoned || got.InventoryComplete {
			t.Fatal("read-only failure changed the store's poison state")
		}
		unchangedAdmissionFiles(t, dir, before, s.lock)
	})
}

func TestHelmJournalAdmissionLifecycle(t *testing.T) {
	for _, name := range []string{"nil-handle", "zero-handle", "closed-handle", "copied-closed-handle", "poisoned-store", "active-writer"} {
		t.Run(name, func(t *testing.T) {
			var s *Store
			wantReason, wantErr := AdmissionClosed, ErrClosed
			switch name {
			case "zero-handle":
				s = &Store{}
			case "closed-handle", "copied-closed-handle", "poisoned-store", "active-writer":
				s, _ = testStore(t)
				if name == "poisoned-store" {
					s.poisoned = true
					wantReason, wantErr = AdmissionUnavailable, ErrUnavailable
				} else if name == "active-writer" {
					testBegin(t, s, testPlan())
					wantReason, wantErr = AdmissionBusy, ErrBusy
				} else {
					copy := *s
					mustJournal(t, s.Close())
					if name == "copied-closed-handle" {
						s = &copy
					}
				}
			}
			got, err := s.ReviewAdmission(context.Background(), admissionScope(testPlan()))
			requireAdmission(t, got, err, wantReason, wantErr)
			if got.InventoryComplete {
				t.Fatal("unreadable or busy handle claimed a complete inventory")
			}
		})
	}
	t.Run("blocked-sync", func(t *testing.T) {
		s, _ := testStore(t)
		w := testBegin(t, s, testPlan())
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(release) })
		w.sink = &faultSink{File: w.file, entered: entered, release: release}
		done := make(chan error, 1)
		go func() { done <- w.Intent(0, "7") }()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("writer did not reach durable-storage barrier")
		}
		reviewed := make(chan Admission, 1)
		go func() {
			got, err := s.ReviewAdmission(context.Background(), admissionScope(testPlan()))
			if !errors.Is(err, ErrBusy) {
				got.Reason = AdmissionNoBlocker
			}
			reviewed <- got
		}()
		select {
		case got := <-reviewed:
			if got.Reason != AdmissionBusy {
				t.Fatal("blocked durable writer did not produce Busy")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("review blocked behind a filesystem synchronization")
		}
		once.Do(func() { close(release) })
		mustJournal(t, <-done)
		w.sink = w.file
	})
}

// Cancelling through a context implementation makes cancellation visible during
// a bounded scan without sleeps, storage races, or a production test hook. The
// budget is intentionally smaller than the number of records, not an assertion
// about an exact implementation call sequence.
type admissionCancelContext struct {
	context.Context
	cancel context.CancelFunc
	left   int
}

func (c *admissionCancelContext) Err() error {
	c.left--
	if c.left <= 0 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestHelmJournalAdmissionContext(t *testing.T) {
	for _, name := range []string{"nil", "cancelled", "expired", "bounded-scan"} {
		t.Run(name, func(t *testing.T) {
			s, dir := testStore(t)
			p := testPlan()
			if name == "bounded-scan" {
				for i := 0; i < 24; i++ {
					w := testBegin(t, s, p)
					mustJournal(t, w.Finish(time.Now()))
				}
			}
			mustJournal(t, s.Close())
			var ctx context.Context
			wantErr, wantReason := error(ErrInvalid), AdmissionInvalid
			if name == "cancelled" || name == "bounded-scan" {
				base, cancel := context.WithCancel(context.Background())
				defer cancel()
				ctx, wantErr, wantReason = base, context.Canceled, AdmissionCancelled
				if name == "cancelled" {
					cancel()
				} else {
					ctx = &admissionCancelContext{Context: base, cancel: cancel, left: 12}
				}
			} else if name == "expired" {
				deadline, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer cancel()
				ctx, wantErr, wantReason = deadline, context.DeadlineExceeded, AdmissionCancelled
			}
			before := admissionFiles(t, dir)
			got, err := ReviewAdmission(ctx, dir, admissionScope(p))
			requireAdmission(t, got, err, wantReason, wantErr)
			unchangedAdmissionFiles(t, dir, before)
			next, err := Open(dir)
			mustJournal(t, err)
			mustJournal(t, next.Close())
		})
	}
}

func TestHelmJournalAdmissionExistingOnly(t *testing.T) {
	for _, name := range []string{"missing-directory", "missing-parent", "missing-lock", "missing-lock-with-history"} {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "journal")
			p := testPlan()
			if strings.HasPrefix(name, "missing-lock") {
				s, err := Open(dir)
				mustJournal(t, err)
				if name == "missing-lock-with-history" {
					w := testBegin(t, s, p)
					mustJournal(t, w.Finish(time.Now()))
				}
				mustJournal(t, s.Close())
				mustJournal(t, os.Remove(filepath.Join(dir, lockName)))
			} else if name == "missing-parent" {
				dir = filepath.Join(dir, "child")
			}
			before := admissionFiles(t, parent)
			var records map[string]admissionFileFact
			if strings.HasPrefix(name, "missing-lock") {
				records = admissionFiles(t, dir)
			}
			got, err := ReviewAdmission(context.Background(), dir, admissionScope(p))
			requireAdmission(t, got, err, AdmissionNotInitialized, ErrUnavailable)
			if got.InventoryComplete {
				t.Fatal("uninitialized store was described as an empty usable store")
			}
			unchangedAdmissionFiles(t, parent, before)
			if records != nil {
				unchangedAdmissionFiles(t, dir, records)
			}
		})
	}
	t.Run("invalid-path-and-scope", func(t *testing.T) {
		s, dir := testStore(t)
		mustJournal(t, s.Close())
		before := admissionFiles(t, dir)
		for _, path := range []string{"", "relative", dir + string(os.PathSeparator) + "."} {
			got, err := ReviewAdmission(context.Background(), path, admissionScope(testPlan()))
			requireAdmission(t, got, err, AdmissionInvalid, ErrInvalid)
		}
		for _, scope := range []AdmissionScope{{}, {Binding: strings.Repeat("a", 64), Namespace: "team", StorageUID: "\nPRIVATE"}} {
			got, err := ReviewAdmission(context.Background(), dir, scope)
			requireAdmission(t, got, err, AdmissionInvalid, ErrInvalid)
		}
		unchangedAdmissionFiles(t, dir, before)
	})
	t.Run("private-path", platformAdmissionPermissionChecks)
	t.Run("redirects", platformAdmissionRedirectChecks)
	t.Run("hardlinks", func(t *testing.T) {
		s, dir := testStore(t)
		w := testBegin(t, s, testPlan())
		mustJournal(t, w.Finish(time.Now()))
		mustJournal(t, s.Close())
		mustJournal(t, os.Link(filepath.Join(dir, w.ID()+".wal"), filepath.Join(filepath.Dir(dir), "outside")))
		before := admissionFiles(t, dir)
		got, err := ReviewAdmission(context.Background(), dir, admissionScope(testPlan()))
		requireAdmission(t, got, err, AdmissionUnavailable, ErrUnavailable)
		unchangedAdmissionFiles(t, dir, before)
	})
}

func TestHelmJournalAdmissionProcessLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "journal")
	child, _ := startJournalChild(t, dir, "hold", "")
	before := admissionFiles(t, dir)
	got, err := ReviewAdmission(context.Background(), dir, admissionScope(testPlan()))
	requireAdmission(t, got, err, AdmissionBusy, ErrBusy)
	unchangedAdmissionFiles(t, dir, before)
	killJournalChild(t, child)
	got, err = ReviewAdmission(context.Background(), dir, admissionScope(testPlan()))
	requireAdmission(t, got, err, AdmissionNoBlocker, nil)
	unchangedAdmissionFiles(t, dir, before)
	next, err := Open(dir)
	mustJournal(t, err)
	mustJournal(t, next.Close())
}

func TestHelmJournalAdmissionRechecksAfterReview(t *testing.T) {
	s, dir := testStore(t)
	p := testPlan()
	mustJournal(t, s.Close())
	checked, err := ReviewAdmission(context.Background(), dir, admissionScope(p))
	requireAdmission(t, checked, err, AdmissionNoBlocker, nil)
	other, err := Open(dir)
	mustJournal(t, err)
	w := testBegin(t, other, p)
	mustJournal(t, w.Intent(0, "7"))
	mustJournal(t, w.Record(0, Unknown, ""))
	mustJournal(t, w.Finish(time.Now()))
	mustJournal(t, other.Close())
	before := admissionFiles(t, dir)
	current, err := Open(dir)
	mustJournal(t, err)
	defer current.Close()
	if _, err := current.Begin(p); !errors.Is(err, ErrUnresolved) {
		t.Fatal("stale no-blocker report bypassed a subsequent Unknown record")
	}
	got, err := current.ReviewAdmission(context.Background(), admissionScope(p))
	requireAdmission(t, got, err, AdmissionUnresolved, ErrUnresolved)
	if got.RecordID != w.ID() || checked.Reason != AdmissionNoBlocker {
		t.Fatal("review was an aliased capability or lost the new barrier")
	}
	unchangedAdmissionFiles(t, dir, before)
}

func TestHelmJournalAdmissionCleanupFailure(t *testing.T) {
	for _, name := range []string{"no-blocker", "blocked"} {
		t.Run(name, func(t *testing.T) {
			s, dir := testStore(t)
			p := testPlan()
			w := testBegin(t, s, p)
			if name == "blocked" {
				mustJournal(t, w.Intent(0, "7"))
				mustJournal(t, w.Record(0, Unknown, ""))
			}
			mustJournal(t, w.Finish(time.Now()))
			mustJournal(t, s.Close())
			before := admissionFiles(t, dir)
			root, err := openExistingPrivateRoot(dir)
			mustJournal(t, err)
			defer root.close()
			lock, err := root.openFile(lockName, false, false)
			mustJournal(t, err)
			defer lock.Close()
			mustJournal(t, acquireLock(lock))
			reader := &Store{storeState: &storeState{root: root, lock: lock}}
			got, reviewErr := reader.ReviewAdmission(context.Background(), admissionScope(p))
			if got.Records != 1 || !got.InventoryComplete {
				t.Fatal("cleanup fixture did not perform the real read-only review")
			}
			// Force actual OS handle cleanup errors, without replacing the
			// production lock or file APIs with a successful fake.
			mustJournal(t, lock.Close())
			got, err = finishAdmission(context.Background(), root, lock, true, got, reviewErr)
			if !errors.Is(err, ErrUnavailable) || got.Reason == AdmissionNoBlocker || got.Records != 1 || !got.InventoryComplete {
				t.Fatal("cleanup error left success or erased known inventory facts")
			}
			if name == "blocked" && (!errors.Is(err, ErrUnresolved) || got.RecordID != w.ID() || got.UnresolvedKind != UnresolvedUnknown) {
				t.Fatal("cleanup error erased the observed historical barrier")
			}
			unchangedAdmissionFiles(t, dir, before)
			next, err := Open(dir)
			mustJournal(t, err)
			mustJournal(t, next.Close())
		})
	}
}

func admissionWriteFixture(t *testing.T, s *Store, id string, data []byte) {
	t.Helper()
	f, err := s.root.openFile(id+".wal", true, true)
	mustJournal(t, err)
	_, err = f.Write(data)
	mustJournal(t, err)
	mustJournal(t, f.Sync())
	mustJournal(t, f.Close())
}

// Use the real format and valid ordered transitions to reach the aggregate
// reservation boundary. Zero padding would only exercise corruption, which
// deliberately takes precedence over the reservation check.
func admissionLargePlan(t *testing.T) Plan {
	t.Helper()
	p := testPlan()
	p.Revision.ResourceVersion = strings.Repeat("<", 256)
	p.Targets = make([]helmrelease.UninstallTarget, 32)
	for width := 200; width >= 0; width-- {
		for i := range p.Targets {
			p.Targets[i] = helmrelease.UninstallTarget{APIVersion: "v1", Kind: "ConfigMap", Resource: "configmaps",
				Namespace: p.Namespace, Name: fmt.Sprintf("cm-%02d-", i) + strings.Repeat("n", width),
				UID: fmt.Sprintf("%03d", i) + strings.Repeat("u", 125), ResourceVersion: p.Revision.ResourceVersion}
		}
		if _, _, err := encodeFrame(frame{Version: 1, Sequence: 1, Event: "start", ID: fixtureID, Plan: &p}); err == nil {
			return p
		}
	}
	t.Fatal("could not construct a valid bounded large operation")
	return Plan{}
}

func admissionLargeRecord(t *testing.T, id string, p Plan) []byte {
	t.Helper()
	frames := []frame{{Event: "start", ID: id, Plan: &p}}
	beginRV, finishRV := strings.Repeat(">", 256), strings.Repeat("&", 256)
	for i := 0; i < len(p.Targets)+2; i++ {
		rv, observed := p.Revision.ResourceVersion, ""
		if i == 0 {
			observed = beginRV
		} else if i == len(p.Targets)+1 {
			rv, observed = beginRV, finishRV
		}
		index := i
		frames = append(frames, frame{Event: "intent", Index: &index, ResourceVersion: rv},
			frame{Event: "result", Index: &index, Outcome: AcceptedPending, ObservedResourceVersion: observed},
			frame{Event: "result", Index: &index, Outcome: Completed, ObservedResourceVersion: observed})
	}
	finished := p.Started.Add(time.Second)
	frames = append(frames, frame{Event: "finish", Finished: &finished})
	data, previous := []byte(journalMagic), ""
	for i, f := range frames {
		f.Version, f.Sequence, f.Previous = 1, i+1, previous
		encoded, digest, err := encodeFrame(f)
		mustJournal(t, err)
		data, previous = append(data, encoded...), digest
	}
	if len(data) > MaxOperationBytes {
		t.Fatal("capacity fixture exceeded the single-operation limit")
	}
	return data
}

func TestHelmJournalAdmissionCapacity(t *testing.T) {
	t.Run("record-limit", func(t *testing.T) {
		s, dir := testStore(t)
		p := testPlan()
		for i := 0; i < MaxRecords; i++ {
			w := testBegin(t, s, p)
			mustJournal(t, w.Finish(time.Now()))
		}
		before := admissionFiles(t, dir)
		got, err := s.ReviewAdmission(context.Background(), admissionScope(p))
		requireAdmission(t, got, err, AdmissionRecordLimit, ErrBudget)
		if !got.InventoryComplete || got.Records != MaxRecords || got.Bytes <= 0 {
			t.Fatal("record limit lost the complete inventory")
		}
		if _, err := s.Begin(p); !errors.Is(err, ErrBudget) {
			t.Fatal("Begin disagreed with record-count review")
		}
		unchangedAdmissionFiles(t, dir, before)
	})
	t.Run("entry-limit", func(t *testing.T) {
		s, dir := testStore(t)
		for i := 0; i <= MaxRecords; i++ {
			admissionWriteFixture(t, s, fmt.Sprintf("%032x", i), nil)
		}
		before := admissionFiles(t, dir)
		got, err := s.ReviewAdmission(context.Background(), admissionScope(testPlan()))
		requireAdmission(t, got, err, AdmissionRecordLimit, ErrBudget)
		if got.InventoryComplete {
			t.Fatal("bounded directory scan claimed a full over-limit inventory")
		}
		unchangedAdmissionFiles(t, dir, before)
	})
	for _, name := range []string{"operation-bytes", "store-bytes"} {
		t.Run(name, func(t *testing.T) {
			s, dir := testStore(t)
			count, size, reason := 1, int64(MaxOperationBytes+1), AdmissionOperationBytes
			if name == "store-bytes" {
				count, size, reason = MaxStoreBytes/MaxOperationBytes+1, MaxOperationBytes, AdmissionStoreBytes
			}
			for i := 0; i < count; i++ {
				f, err := s.root.openFile(fmt.Sprintf("%032x.wal", i), true, true)
				mustJournal(t, err)
				mustJournal(t, f.Truncate(size))
				mustJournal(t, f.Close())
			}
			before := admissionFiles(t, dir)
			got, err := s.ReviewAdmission(context.Background(), admissionScope(testPlan()))
			requireAdmission(t, got, err, reason, ErrBudget)
			if got.InventoryComplete {
				t.Fatal("failed inventory advertised verified total usage")
			}
			if _, err := s.Begin(testPlan()); !errors.Is(err, ErrBudget) {
				t.Fatal("Begin disagreed with the inventory byte barrier")
			}
			unchangedAdmissionFiles(t, dir, before)
		})
	}
	t.Run("reservation-boundary", func(t *testing.T) {
		s, dir := testStore(t)
		large := admissionLargePlan(t)
		sample := admissionLargeRecord(t, fixtureID, large)
		parsed := parse(sample, fixtureID).snapshot
		if parsed.Corrupt || !parsed.Closed || unresolved(parsed) {
			t.Fatal("capacity fixture is not valid completed history")
		}
		count := (MaxStoreBytes - MaxOperationBytes) / len(sample)
		if count+1 >= MaxRecords || count < 1 {
			t.Fatalf("fixture cannot isolate reservation from record limit: %d bytes", len(sample))
		}
		for i := 0; i < count; i++ {
			id := fmt.Sprintf("%032x", i)
			admissionWriteFixture(t, s, id, admissionLargeRecord(t, id, large))
		}
		mustJournal(t, s.root.sync())
		before := admissionFiles(t, dir)
		got, err := s.ReviewAdmission(context.Background(), admissionScope(testPlan()))
		requireAdmission(t, got, err, AdmissionNoBlocker, nil)
		if !got.InventoryComplete || got.Bytes+MaxOperationBytes > MaxStoreBytes {
			t.Fatal("valid reserved capacity was reported unavailable")
		}
		unchangedAdmissionFiles(t, dir, before)
		id := fmt.Sprintf("%032x", count)
		admissionWriteFixture(t, s, id, admissionLargeRecord(t, id, large))
		mustJournal(t, s.root.sync())
		before = admissionFiles(t, dir)
		got, err = s.ReviewAdmission(context.Background(), admissionScope(testPlan()))
		requireAdmission(t, got, err, AdmissionReserveBytes, ErrBudget)
		if !got.InventoryComplete || got.Records != count+1 || got.Bytes >= MaxStoreBytes || got.Bytes+MaxOperationBytes <= MaxStoreBytes {
			t.Fatal("reservation failure was confused with full storage or invalid history")
		}
		if _, err := s.Begin(testPlan()); !errors.Is(err, ErrBudget) {
			t.Fatal("Begin did not repeat the same full-operation reservation")
		}
		unchangedAdmissionFiles(t, dir, before)
	})
}
