package helmjournal

import (
	"bytes"
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

func testPlan() Plan {
	now := time.Now().UTC()
	return Plan{Binding: strings.Repeat("a", 64), PreparationID: strings.Repeat("b", 32), Namespace: "team",
		Revision: helmrelease.Revision{Release: "example", Number: 2, Status: "deployed", StorageName: "sh.helm.release.v1.example.v2", UID: "storage-uid", ResourceVersion: "7"},
		Targets: []helmrelease.UninstallTarget{
			{APIVersion: "v1", Kind: "ConfigMap", Resource: "configmaps", Namespace: "team", Name: "first", UID: "uid-first", ResourceVersion: "21"},
			{APIVersion: "v1", Kind: "ConfigMap", Resource: "configmaps", Namespace: "team", Name: "second", UID: "uid-second", ResourceVersion: "22"},
		}, Started: now, Expires: now.Add(20 * time.Second)}
}

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "journal")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, dir
}

func testBegin(t *testing.T, s *Store, p Plan) *Writer {
	t.Helper()
	w, err := s.Begin(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.Abort(); err != nil {
			t.Error(err)
		}
	})
	return w
}

func mustJournal(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func beginCompleted(t *testing.T, w *Writer) {
	t.Helper()
	mustJournal(t, w.Intent(0, "7"))
	mustJournal(t, w.Record(0, AcceptedPending, "8"))
	mustJournal(t, w.Record(0, Completed, "8"))
}

func allCompleted(t *testing.T, w *Writer) {
	t.Helper()
	beginCompleted(t, w)
	for i, rv := range []string{"21", "22"} {
		mustJournal(t, w.Intent(i+1, rv))
		mustJournal(t, w.Record(i+1, AcceptedPending, ""))
		mustJournal(t, w.Record(i+1, Completed, ""))
	}
	mustJournal(t, w.Intent(3, "8"))
	mustJournal(t, w.Record(3, AcceptedPending, "9"))
	mustJournal(t, w.Record(3, Completed, "9"))
}

func TestHelmJournalRoundTripAndImmutableCopies(t *testing.T) {
	s, dir := testStore(t)
	p := testPlan()
	w := testBegin(t, s, p)
	id := w.ID()
	if !validHex(id, 32) {
		t.Fatal("noncanonical operation ID")
	}
	p.Targets[0].UID = "caller-changed"
	a, err := s.Load(id)
	mustJournal(t, err)
	if a.Plan.Targets[0].UID != "uid-first" || a.Effects[0].Outcome != NotStarted || a.Closed || a.Corrupt || !validHex(a.Digest, 64) {
		t.Fatal("initial projection changed")
	}
	a.Plan.Targets[0].UID, a.Effects[0].UID = "copy-changed", "copy-changed"
	b, err := s.Load(id)
	mustJournal(t, err)
	if b.Plan.Targets[0].UID != "uid-first" || b.Effects[0].UID != "storage-uid" {
		t.Fatal("returned projection aliases a stored plan")
	}
	mustJournal(t, w.Intent(0, "7"))
	intent, err := s.Load(id)
	mustJournal(t, err)
	if intent.Effects[0].Outcome != Unknown || intent.Closed || intent.Digest == b.Digest {
		t.Fatal("unacknowledged durable intent was not Unknown")
	}
	mustJournal(t, w.Record(0, AcceptedPending, "8"))
	mustJournal(t, w.Record(0, Completed, "8"))
	before, err := os.ReadFile(filepath.Join(dir, id+".wal"))
	mustJournal(t, err)
	mustJournal(t, w.Record(0, Completed, "8"))
	after, err := os.ReadFile(filepath.Join(dir, id+".wal"))
	mustJournal(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("idempotent completion wrote another frame")
	}
	for i, rv := range []string{"21", "22"} {
		mustJournal(t, w.Intent(i+1, rv))
		mustJournal(t, w.Record(i+1, AcceptedPending, ""))
		mustJournal(t, w.Record(i+1, Completed, ""))
	}
	mustJournal(t, w.Intent(3, "8"))
	mustJournal(t, w.Record(3, AcceptedPending, "9"))
	mustJournal(t, w.Record(3, Completed, "9"))
	mustJournal(t, w.Finish(time.Now()))
	done, err := s.Load(id)
	mustJournal(t, err)
	if !done.Closed || done.Finished.IsZero() || done.Corrupt || unresolved(done) {
		t.Fatal("complete retained history failed")
	}
	for _, e := range done.Effects {
		if e.Outcome != Completed {
			t.Fatal("ordered completion was lost")
		}
	}
	if err := w.Intent(0, "7"); !errors.Is(err, ErrClosed) {
		t.Fatal("finished writer was reusable")
	}
	mustJournal(t, w.Abort())
	mustJournal(t, s.Close())
	reopened, err := Open(dir)
	mustJournal(t, err)
	defer reopened.Close()
	recovered, err := reopened.Load(id)
	mustJournal(t, err)
	if recovered.Digest != done.Digest || !recovered.Closed || recovered.Effects[3].ObservedResourceVersion != "9" {
		t.Fatal("reopen did not retain the exact terminal record")
	}
}

type faultSink struct {
	*os.File
	kind    string
	entered chan struct{}
	release chan struct{}
}

func (f *faultSink) Write(p []byte) (int, error) {
	switch f.kind {
	case "zero-write":
		return 0, nil
	case "short-write":
		return f.File.Write(p[:len(p)/2])
	case "write-error":
		return 0, errors.New("PRIVATE_DISK_PATH_TOKEN")
	}
	return f.File.Write(p)
}

func (f *faultSink) Sync() error {
	if f.entered != nil {
		close(f.entered)
		<-f.release
	}
	if f.kind == "sync-error" {
		return errors.New("PRIVATE_DISK_PATH_TOKEN")
	}
	return f.File.Sync()
}

func (f *faultSink) Close() error {
	err := f.File.Close()
	if f.kind == "close-error" {
		return errors.New("PRIVATE_DISK_PATH_TOKEN")
	}
	return err
}

func TestHelmJournalWriteAndSyncFailures(t *testing.T) {
	for _, phase := range []string{"start", "intent", "acknowledgement", "observation", "terminal"} {
		for _, kind := range []string{"zero-write", "short-write", "write-error", "sync-error"} {
			t.Run(phase+"/"+kind, func(t *testing.T) {
				s, dir := testStore(t)
				var err error
				if phase == "start" {
					s.newSink = func(f *os.File) durableSink { return &faultSink{File: f, kind: kind} }
					_, err = s.Begin(testPlan())
				} else {
					w := testBegin(t, s, testPlan())
					if phase != "intent" {
						mustJournal(t, w.Intent(0, "7"))
					}
					if phase == "observation" || phase == "terminal" {
						mustJournal(t, w.Record(0, AcceptedPending, "8"))
					}
					if phase == "terminal" {
						mustJournal(t, w.Record(0, Completed, "8"))
					}
					w.sink = &faultSink{File: w.file, kind: kind}
					switch phase {
					case "intent":
						err = w.Intent(0, "7")
					case "acknowledgement":
						err = w.Record(0, AcceptedPending, "8")
					case "observation":
						err = w.Record(0, Completed, "8")
					case "terminal":
						err = w.Finish(time.Now())
					}
					if !errors.Is(w.Intent(1, "21"), ErrUnavailable) {
						t.Fatal("failed writer did not stay poisoned")
					}
					mustJournal(t, w.Abort())
				}
				if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "PRIVATE") || strings.Contains(err.Error(), dir) {
					t.Fatal("storage error was widened or leaked details")
				}
				if _, err := s.Begin(testPlan()); !errors.Is(err, ErrUnavailable) {
					t.Fatal("poisoned store admitted another operation")
				}
				entries, err := os.ReadDir(dir)
				mustJournal(t, err)
				if len(entries) != 2 {
					t.Fatal("failed append removed or duplicated its recovery file")
				}
				for _, entry := range entries {
					if strings.HasSuffix(entry.Name(), ".wal") {
						data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
						mustJournal(t, err)
						if bytes.Contains(data, []byte("PRIVATE_DISK_PATH_TOKEN")) {
							t.Fatal("raw disk error was persisted")
						}
					}
				}
			})
		}
	}
	t.Run("directory-sync", func(t *testing.T) {
		s, _ := testStore(t)
		s.syncRoot = func() error { return errors.New("PRIVATE_DIRECTORY") }
		if _, err := s.Begin(testPlan()); !errors.Is(err, ErrUnavailable) || !s.poisoned {
			t.Fatal("failed directory synchronization was admitted")
		}
	})
	t.Run("close-error", func(t *testing.T) {
		s, _ := testStore(t)
		w := testBegin(t, s, testPlan())
		allCompleted(t, w)
		w.sink = &faultSink{File: w.file, kind: "close-error"}
		if err := w.Finish(time.Now()); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "PRIVATE") || !s.poisoned {
			t.Fatal("failed close was hidden")
		}
		if _, err := s.Begin(testPlan()); !errors.Is(err, ErrUnavailable) {
			t.Fatal("close failure permitted another operation")
		}
	})
}

func TestHelmJournalBudgetsAndUnresolvedScope(t *testing.T) {
	for _, outcome := range []string{"open-intent", Unknown, AcceptedPending, "partial"} {
		t.Run(outcome, func(t *testing.T) {
			s, dir := testStore(t)
			p := testPlan()
			w := testBegin(t, s, p)
			mustJournal(t, w.Intent(0, "7"))
			switch outcome {
			case Unknown:
				mustJournal(t, w.Record(0, Unknown, ""))
				mustJournal(t, w.Finish(time.Now()))
			case AcceptedPending:
				mustJournal(t, w.Record(0, AcceptedPending, "8"))
				mustJournal(t, w.Finish(time.Now()))
			case "partial":
				mustJournal(t, w.Record(0, AcceptedPending, "8"))
				mustJournal(t, w.Record(0, Completed, "8"))
				mustJournal(t, w.Finish(time.Now()))
			default:
				mustJournal(t, w.Abort())
			}
			if _, err := s.Begin(p); !errors.Is(err, ErrUnresolved) {
				t.Fatal("new attempt bypassed unresolved prior effects")
			}
			mustJournal(t, s.Close())
			reopened, err := Open(dir)
			mustJournal(t, err)
			defer reopened.Close()
			if _, err := reopened.Begin(p); !errors.Is(err, ErrUnresolved) {
				t.Fatal("reopen erased the unresolved scope restriction")
			}
			p.Binding = strings.Repeat("c", 64)
			other, err := reopened.Begin(p)
			mustJournal(t, err)
			mustJournal(t, other.Abort())
		})
	}
	t.Run("rejected-is-historical", func(t *testing.T) {
		s, _ := testStore(t)
		p := testPlan()
		w := testBegin(t, s, p)
		mustJournal(t, w.Intent(0, "7"))
		mustJournal(t, w.Record(0, Rejected, ""))
		mustJournal(t, w.Finish(time.Now()))
		next := testBegin(t, s, p)
		mustJournal(t, next.Abort())
	})
	t.Run("record-count", func(t *testing.T) {
		s, _ := testStore(t)
		for i := 0; i < MaxRecords; i++ {
			w := testBegin(t, s, testPlan())
			mustJournal(t, w.Finish(time.Now()))
		}
		if _, err := s.Begin(testPlan()); !errors.Is(err, ErrBudget) {
			t.Fatal("record capacity did not fail closed")
		}
		ids, err := s.List()
		mustJournal(t, err)
		if len(ids) != MaxRecords {
			t.Fatal("capacity enforcement silently removed history")
		}
	})
	t.Run("operation-bytes", func(t *testing.T) {
		s, _ := testStore(t)
		w := testBegin(t, s, testPlan())
		mustJournal(t, w.Abort())
		f, err := s.root.openFile(w.ID()+".wal", false, true)
		mustJournal(t, err)
		mustJournal(t, f.Truncate(MaxOperationBytes+1))
		mustJournal(t, f.Close())
		if _, err := s.List(); !errors.Is(err, ErrBudget) {
			t.Fatal("oversized operation admitted")
		}
		snap, err := s.Load(w.ID())
		if !errors.Is(err, ErrCorrupt) || !snap.Corrupt {
			t.Fatal("oversized file was treated as a clean record")
		}
	})
	t.Run("store-bytes", func(t *testing.T) {
		s, _ := testStore(t)
		for i := 0; i < MaxStoreBytes/MaxOperationBytes+1; i++ {
			f, err := s.root.openFile(fmt.Sprintf("%032x.wal", i), true, true)
			mustJournal(t, err)
			mustJournal(t, f.Truncate(MaxOperationBytes))
			mustJournal(t, f.Close())
		}
		if _, err := s.List(); !errors.Is(err, ErrBudget) {
			t.Fatal("total byte capacity did not fail closed")
		}
	})
	t.Run("corruption-blocks-new-plan", func(t *testing.T) {
		s, dir := testStore(t)
		w := testBegin(t, s, testPlan())
		mustJournal(t, w.Abort())
		f, err := os.OpenFile(filepath.Join(dir, w.ID()+".wal"), os.O_APPEND|os.O_WRONLY, 0600)
		mustJournal(t, err)
		_, err = f.Write([]byte{0, 1})
		mustJournal(t, err)
		mustJournal(t, f.Close())
		p := testPlan()
		p.Binding = strings.Repeat("d", 64)
		if _, err := s.Begin(p); !errors.Is(err, ErrCorrupt) {
			t.Fatal("unattributable corrupt record allowed a new execution")
		}
	})
}

func TestHelmJournalConcurrentLeaseAndClose(t *testing.T) {
	t.Run("copied-handles-share-private-state", func(t *testing.T) {
		s, _ := testStore(t)
		copiedStore := *s
		t.Cleanup(func() { mustJournal(t, copiedStore.Close()) })
		w := testBegin(t, s, testPlan())
		copiedWriter := *w
		if _, err := copiedStore.Begin(testPlan()); !errors.Is(err, ErrBusy) {
			t.Fatal("copied store duplicated the operation lease")
		}
		*s = Store{}
		mustJournal(t, w.Intent(0, "7"))
		mustJournal(t, copiedWriter.Record(0, AcceptedPending, "8"))
		snapshot, err := copiedStore.Load(w.ID())
		mustJournal(t, err)
		if snapshot.Effects[0].Outcome != AcceptedPending {
			t.Fatal("copied writer forked the durable state")
		}
		mustJournal(t, copiedWriter.Abort())
		if err := w.Record(0, Completed, "8"); !errors.Is(err, ErrClosed) {
			t.Fatal("copied writer close did not close the shared lease")
		}
		mustJournal(t, copiedStore.Close())
	})
	s, dir := testStore(t)
	p := testPlan()
	var wg sync.WaitGroup
	writers := make(chan *Writer, 16)
	failures := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w, err := s.Begin(p)
			if err != nil {
				failures <- err
			} else {
				writers <- w
			}
		}()
	}
	wg.Wait()
	close(writers)
	close(failures)
	if len(writers) != 1 || len(failures) != 15 {
		t.Fatal("store did not grant exactly one operation lease")
	}
	for err := range failures {
		if !errors.Is(err, ErrBusy) {
			t.Fatal("concurrent lease lost its bounded busy state")
		}
	}
	w := <-writers
	t.Cleanup(func() { w.Abort() })
	if !errors.Is(s.Close(), ErrBusy) {
		t.Fatal("active lease let Close release the OS lock")
	}
	if other, err := Open(dir); !errors.Is(err, ErrBusy) {
		if other != nil {
			other.Close()
		}
		t.Fatal("active lease lost its process lock")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	w.sink = &faultSink{File: w.file, entered: entered, release: release}
	intentDone := make(chan error, 1)
	go func() { intentDone <- w.Intent(0, "7") }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("synchronization did not reach the test barrier")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- s.Close() }()
	select {
	case <-closeDone:
		t.Fatal("Close returned while durable append was still in progress")
	case <-time.After(30 * time.Millisecond):
	}
	if other, err := Open(dir); !errors.Is(err, ErrBusy) {
		if other != nil {
			other.Close()
		}
		t.Fatal("blocked append released the process lock")
	}
	close(release)
	mustJournal(t, <-intentDone)
	if err := <-closeDone; !errors.Is(err, ErrBusy) {
		t.Fatal("Close erased a completed-but-active append lease")
	}
	w.sink = w.file
	var ackWG sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		ackWG.Add(1)
		go func() { defer ackWG.Done(); results <- w.Record(0, AcceptedPending, "8") }()
	}
	ackWG.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatal("concurrent acknowledgements duplicated the state transition")
	}
	mustJournal(t, w.Abort())
	mustJournal(t, s.Close())
	reopened, err := Open(dir)
	mustJournal(t, err)
	mustJournal(t, reopened.Close())
}
