package kube

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
)

type failingInstallJournal struct {
	real        *helmjournal.InstallWriter
	mode        string
	afterIntent func()
}

func (j failingInstallJournal) Intent(i int, d string) error {
	if j.mode == "intent" {
		return helmjournal.ErrUnavailable
	}
	err := j.real.Intent(i, d)
	if err == nil && j.afterIntent != nil {
		j.afterIntent()
	}
	return err
}
func (j failingInstallJournal) Response(i int, o, u, v string) error {
	if j.mode == "response" {
		return helmjournal.ErrUnavailable
	}
	return j.real.Response(i, o, u, v)
}
func (j failingInstallJournal) Observe(i int, u, v string) error {
	if j.mode == "observe" {
		return helmjournal.ErrUnavailable
	}
	return j.real.Observe(i, u, v)
}

func TestHelmInstallExecutionJournalFailure(t *testing.T) {
	for _, mode := range []string{"intent", "response", "observe", "revoke-after-intent"} {
		t.Run(mode, func(t *testing.T) {
			b, c, pre, _, f := installExecutionSetup(t, 1)
			g, err := b.AcknowledgeHelmInstallExecution(context.Background(), pre, c, "execute-session", true, f.journal)
			if err != nil {
				t.Fatal(err)
			}
			s := g.state
			origin := s.origin
			now := time.Now()
			p := helmjournal.InstallPlan{Binding: s.binding, CandidateID: c.ID(), PreparationID: pre.PreparationID(), Namespace: c.Namespace(), NamespaceUID: origin.preparation.namespaceUID, NamespaceResourceVersion: origin.preparation.namespaceRV, Release: c.Release(), StorageName: c.StorageName(), Started: now, Expires: pre.ExpiresAt()}
			for i := 0; i < origin.plan.Len(); i++ {
				_, name := origin.plan.Identity(i)
				body := origin.plan.SensitiveBody(i)
				d := installRequestDigest(body)
				clear(body)
				if i == 0 {
					p.StorageDigest = d
				} else {
					p.Targets = append(p.Targets, helmjournal.InstallTarget{Name: name, Digest: d})
				}
			}
			w, err := s.store.BeginInstall(p)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Abort()
			snap, err := s.store.Load(w.ID())
			if err != nil {
				t.Fatal(err)
			}
			out := &HelmInstallExecution{id: w.ID(), effects: snap.Effects}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			j := failingInstallJournal{real: w, mode: mode}
			if mode == "revoke-after-intent" {
				j.afterIntent = cancel
			}
			r := installExecutionRun{b: b, ctx: ctx, active: ctx.Err, dispatch: ctx.Err, state: origin, session: "execute-session", journal: j, out: out, remaining: 16 << 20}
			err = r.run()
			if mode == "revoke-after-intent" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrHelmExecutionJournal) {
				t.Fatal("journal failure not distinguished", err)
			}
			expected := int32(1)
			if mode == "intent" || mode == "revoke-after-intent" {
				expected = 0
			}
			if f.writes.Load() != expected || f.bad.Load() {
				t.Fatal("journal failure allowed next request", f.writes.Load(), f.bad.Load())
			}
			snap, err = s.store.Load(w.ID())
			if err != nil {
				t.Fatal(err)
			}
			want := helmjournal.Unknown
			if mode == "intent" {
				want = helmjournal.NotStarted
			}
			if mode == "observe" {
				want = helmjournal.AcceptedPending
			}
			if snap.Effects[0].Outcome != want {
				t.Fatal("incorrect durable prefix", snap.Effects[0])
			}
		})
	}
}
func TestHelmInstallExecutionSharedAdmission(t *testing.T) {
	for _, mode := range []string{"unresolved-install", "closed"} {
		t.Run(mode, func(t *testing.T) {
			b, c, pre, _, f := installExecutionSetup(t, 1)
			g, err := b.AcknowledgeHelmInstallExecution(context.Background(), pre, c, "execute-session", true, f.journal)
			if err != nil {
				t.Fatal(err)
			}
			s := g.state
			origin := s.origin
			if mode == "closed" {
				_ = s.store.Close()
			} else {
				now := time.Now()
				body := origin.plan.SensitiveBody(0)
				d := installRequestDigest(body)
				clear(body)
				body = origin.plan.SensitiveBody(1)
				td := installRequestDigest(body)
				clear(body)
				p := helmjournal.InstallPlan{Binding: s.binding, CandidateID: c.ID(), PreparationID: pre.PreparationID(), Namespace: c.Namespace(), NamespaceUID: origin.preparation.namespaceUID, NamespaceResourceVersion: origin.preparation.namespaceRV, Release: c.Release(), StorageName: c.StorageName(), StorageDigest: d, Targets: []helmjournal.InstallTarget{{Name: "target-0", Digest: td}}, Started: now, Expires: pre.ExpiresAt()}
				w, e := s.store.BeginInstall(p)
				if e != nil {
					t.Fatal(e)
				}
				if e = w.Intent(0, d); e != nil {
					t.Fatal(e)
				}
				if e = w.Finish(time.Now()); e != nil {
					t.Fatal(e)
				}

			}
			if _, err = b.ExecuteHelmInstall(context.Background(), g, c, "execute-session"); !errors.Is(err, ErrHelmExecutionJournal) || f.calls.Load() != 0 {
				t.Fatal("shared admission bypass", err, f.calls.Load())
			}
		})
	}
}

func TestHelmInstallExecutionCorruptAppend(t *testing.T) {
	b, c, pre, _, f := installExecutionSetup(t, 1)
	// Replace only the fixture's journal with a second explicit private directory;
	// the grant is minted afterwards and therefore binds this original store.
	if err := f.journal.Close(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "journal")
	var err error
	f.journal, err = OpenHelmUninstallJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.journal.Close()
	g, err := b.AcknowledgeHelmInstallExecution(context.Background(), pre, c, "execute-session", true, f.journal)
	if err != nil {
		t.Fatal(err)
	}
	f.onWrite = func(n int) {
		if n != 1 {
			return
		}
		ids, e := f.journal.IDs()
		if e != nil || len(ids) != 1 {
			f.bad.Store(true)
			return
		}
		file, e := os.OpenFile(filepath.Join(dir, ids[0]+".wal"), os.O_WRONLY|os.O_APPEND, 0)
		if e != nil {
			f.bad.Store(true)
			return
		}
		_, e = file.Write([]byte{0, 0})
		if errors.Join(e, file.Sync(), file.Close()) != nil {
			f.bad.Store(true)
		}
	}
	out, err := b.ExecuteHelmInstall(context.Background(), g, c, "execute-session")
	if !errors.Is(err, ErrHelmExecutionJournal) || out.JournalComplete() || out.Effects()[0].Outcome != helmjournal.AcceptedPending || f.writes.Load() != 1 || f.bad.Load() {
		t.Fatal("failed append lost facts or continued", err)
	}
	s, e := f.journal.store.Load(out.JournalID())
	if !errors.Is(e, helmjournal.ErrCorrupt) || s.Effects[0].Outcome != helmjournal.Unknown {
		t.Fatal("durable unknown prefix erased", e)
	}
}
