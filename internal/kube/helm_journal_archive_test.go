package kube

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
)

func archiveSessionFixture(t *testing.T, unknown bool) (string, *HelmUninstallRecovery) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "journal")
	s, err := helmjournal.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	p := helmjournal.Plan{Binding: strings.Repeat("a", 64), PreparationID: strings.Repeat("b", 32), Namespace: "team", Revision: selectedHelmRevision(),
		Targets: []helmrelease.UninstallTarget{{APIVersion: "v1", Kind: "ConfigMap", Resource: "configmaps", Namespace: "team", Name: "sample", UID: "uid-sample", ResourceVersion: "21"}}, Started: now, Expires: now.Add(20 * time.Second)}
	w, err := s.Begin(p)
	if err != nil {
		t.Fatal(err)
	}
	if unknown {
		if err = w.Intent(0, p.Revision.ResourceVersion); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Finish(time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	j, err := OpenHelmUninstallJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	r, err := j.Recover(w.ID())
	if err != nil {
		t.Fatal(err)
	}
	return path, r
}
func awaitArchive(t *testing.T, s *HelmJournalArchiveSession) (helmjournal.ArchiveResult, error) {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not release lock")
	}
	result, err, done := s.Result()
	if !done {
		t.Fatal("closed completion lost result")
	}
	return result, err
}
func assertArchiveUnlocked(t *testing.T, path string) {
	t.Helper()
	s, err := helmjournal.OpenExisting(path)
	if err != nil {
		t.Fatal("leaked lock", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHelmJournalArchiveSessionRoundTrip(t *testing.T) {
	path, r := archiveSessionFixture(t, false)
	before, err := os.ReadFile(filepath.Join(path, r.ID()+".wal"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := StartHelmJournalArchive(context.Background(), path, r)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cancel()
	if s.ID() != r.ID() || s.Bytes() != int64(len(before)) || s.Inventory().Records != 1 || time.Until(s.ExpiresAt()) <= 0 {
		t.Fatal("review scope")
	}
	if other, e := helmjournal.OpenExisting(path); e == nil {
		other.Close()
		t.Fatal("review did not hold original Store")
	}
	if err = s.Confirm(r.ID(), true); err != nil {
		t.Fatal(err)
	}
	result, err := awaitArchive(t, s)
	if err != nil || result.Outcome != helmjournal.ArchiveCompleted {
		t.Fatal(result, err)
	}
	if e := s.Confirm(r.ID(), true); !errors.Is(e, helmjournal.ErrArchiveConsent) {
		t.Fatal("replayed")
	}
	assertArchiveUnlocked(t, path)
	j, err := OpenHelmUninstallJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	ids, err := j.ArchivedIDs()
	if err != nil || len(ids) != 1 || ids[0] != r.ID() {
		t.Fatal(ids, err)
	}
	retained, err := j.RecoverArchived(r.ID())
	if err != nil || !retained.Archived() || retained.Outcome() != r.Outcome() || r.Archived() {
		t.Fatal("history mutated", err)
	}
	after, err := os.ReadFile(filepath.Join(path, r.ID()+".archived.wal"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("original changed", err)
	}
}

func TestHelmJournalArchiveSessionCleanup(t *testing.T) {
	for _, kind := range []string{"cancel", "deadline", "wrong-id", "no-ack", "copy-cancel", "parent-cancel"} {
		t.Run(kind, func(t *testing.T) {
			path, r := archiveSessionFixture(t, false)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "deadline" {
				var end context.CancelFunc
				ctx, end = context.WithTimeout(ctx, 40*time.Millisecond)
				defer end()
			}
			s, err := StartHelmJournalArchive(ctx, path, r)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Cancel()
			switch kind {
			case "cancel":
				s.Cancel()
			case "copy-cancel":
				copy := *s
				copy.Cancel()
			case "parent-cancel":
				cancel()
			case "wrong-id":
				if s.Confirm("wrong", true) == nil {
					t.Fatal("wrong ID")
				}
			case "no-ack":
				if s.Confirm(r.ID(), false) == nil {
					t.Fatal("no acknowledgement")
				}
			}
			result, err := awaitArchive(t, s)
			if err == nil || result.Outcome != helmjournal.ArchiveNotApplied {
				t.Fatal(result, err)
			}
			assertArchiveUnlocked(t, path)
			if _, err = os.Stat(filepath.Join(path, r.ID()+".wal")); err != nil {
				t.Fatal("cancel removed record", err)
			}
		})
	}
}

func TestHelmJournalArchiveSessionCopiesSingleUse(t *testing.T) {
	path, r := archiveSessionFixture(t, false)
	s, err := StartHelmJournalArchive(context.Background(), path, r)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cancel()
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		copy := *s
		wg.Add(1)
		go func() {
			defer wg.Done()
			if copy.Confirm(r.ID(), true) == nil {
				accepted.Add(1)
			}
			for i := 0; i < 100; i++ {
				copy.Result()
			}
		}()
	}
	wg.Wait()
	result, err := awaitArchive(t, s)
	if accepted.Load() != 1 || err != nil || result.Outcome != helmjournal.ArchiveCompleted {
		t.Fatal(accepted.Load(), result, err)
	}
	assertArchiveUnlocked(t, path)
}

func TestHelmJournalArchiveSessionRejectsStaleAndUnknown(t *testing.T) {
	for _, kind := range []string{"unknown", "same-bytes-replacement", "wrong-directory", "changed-after-review", "cancelled-start", "nil-history"} {
		t.Run(kind, func(t *testing.T) {
			path, r := archiveSessionFixture(t, kind == "unknown")
			if kind == "same-bytes-replacement" {
				original := filepath.Join(path, r.ID()+".wal")
				b, e := os.ReadFile(original)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.Rename(original, filepath.Join(t.TempDir(), "old")); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(original, b, 0600); e != nil {
					t.Fatal(e)
				}
			}
			if kind == "wrong-directory" {
				path = filepath.Join(t.TempDir(), "absent")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancelled-start" {
				cancel()
			}
			if kind == "nil-history" {
				r = nil
			}
			s, err := StartHelmJournalArchive(ctx, path, r)
			if kind == "changed-after-review" {
				if err != nil {
					t.Fatal(err)
				}
				defer s.Cancel()
				f, e := os.OpenFile(filepath.Join(path, r.ID()+".wal"), os.O_WRONLY|os.O_APPEND, 0600)
				if e != nil {
					t.Fatal(e)
				}
				_, e = f.Write([]byte("torn"))
				if e != nil {
					t.Fatal(e)
				}
				if e = f.Close(); e != nil {
					t.Fatal(e)
				}
				if e = s.Confirm(r.ID(), true); e != nil {
					t.Fatal(e)
				}
				result, e := awaitArchive(t, s)
				if e == nil || result.Outcome != helmjournal.ArchiveNotApplied {
					t.Fatal("changed record moved", result, e)
				}
			} else if err == nil || s != nil {
				if s != nil {
					s.Cancel()
				}
				t.Fatal("stale or uncertain history admitted")
			}
			if kind == "wrong-directory" {
				if _, e := os.Stat(path); !os.IsNotExist(e) {
					t.Fatal("new path initialized")
				}
			} else {
				assertArchiveUnlocked(t, path)
			}
		})
	}
}
