package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
)

func TestHelmInstallExecutionSuccess(t *testing.T) {
	for _, n := range []int{1, 32} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			b, c, pre, _, f := installExecutionSetup(t, n)
			grant, err := b.AcknowledgeHelmInstallExecution(context.Background(), pre, c, "execute-session", true, f.journal)
			if err != nil {
				t.Fatal(err)
			}
			if f.calls.Load() != 0 {
				t.Fatal("grant performed I/O")
			}
			report, err := b.ExecuteHelmInstall(context.Background(), grant, c, "execute-session")
			if err != nil {
				t.Fatal(err)
			}
			if report.Outcome() != helmjournal.Completed || !report.JournalComplete() || len(report.Effects()) != n+2 || f.writes.Load() != int32(n+2) || f.bad.Load() {
				t.Fatal("incomplete/unsafe execution", report.Outcome(), f.writes.Load(), f.bad.Load())
			}
			s, err := f.journal.store.Load(report.JournalID())
			if err != nil || !s.Closed || s.Action != "install" || len(s.Effects) != n+2 {
				t.Fatal("missing durable history", err)
			}
			for _, e := range s.Effects {
				if e.Outcome != helmjournal.Completed || strings.Contains(e.UID, "dry-only") {
					t.Fatal("wrong completion/identity")
				}
			}
			e := report.Effects()
			e[0].Outcome = "forged"
			if report.Effects()[0].Outcome != helmjournal.Completed {
				t.Fatal("mutable report")
			}
			grant.Discard()
			if report.Outcome() != helmjournal.Completed {
				t.Fatal("late revocation erased facts")
			}
			if _, err = b.ExecuteHelmInstall(context.Background(), grant, c, "execute-session"); !errors.Is(err, ErrHelmInstallExecutionConsent) {
				t.Fatal("reused grant", err)
			}
			if _, err = b.AcknowledgeHelmInstallExecution(context.Background(), pre, c, "execute-session", true, f.journal); !errors.Is(err, ErrHelmInstallExecutionConsent) {
				t.Fatal("second mint", err)
			}
		})
	}
}
func TestHelmInstallExecutionAuthority(t *testing.T) {
	for _, mode := range []string{"unacknowledged", "nil-context", "cancelled", "wrong-session", "wrong-backend", "candidate-copy", "candidate-replaced", "revoked", "origin-revoked", "closed-store", "expired", "report-replaced", "store-replaced"} {
		t.Run(mode, func(t *testing.T) {
			b, c, pre, dry, f := installExecutionSetup(t, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "unacknowledged" {
				if _, err := b.AcknowledgeHelmInstallExecution(ctx, pre, c, "execute-session", false, f.journal); err == nil {
					t.Fatal("no independent acknowledgement")
				}
				if f.calls.Load() != 0 {
					t.Fatal("I/O")
				}
				return
			}
			grant, err := b.AcknowledgeHelmInstallExecution(ctx, pre, c, "execute-session", true, f.journal)
			if err != nil {
				t.Fatal(err)
			}
			caller, session := b, "execute-session"
			switch mode {
			case "nil-context":
				ctx = nil
			case "cancelled":
				cancel()
			case "wrong-session":
				session = "other"
			case "wrong-backend":
				caller, _ = New(b.Config())
			case "candidate-copy":
				copy := *c
				c = &copy
			case "candidate-replaced":
				*c = *installHTTPCandidate(t, time.Now())
			case "revoked":
				grant.Discard()
			case "origin-revoked":
				dry.Discard()
			case "closed-store":
				if err = f.journal.Close(); err != nil {
					t.Fatal(err)
				}
			case "expired":
				grant.state.origin.preparation.expires = time.Now().Add(-time.Second)
			case "report-replaced":
				*pre = HelmInstallDryRun{}
			case "store-replaced": // Replace the public wrapper, not the frozen store lifetime.
				old := f.journal
				*old = HelmUninstallJournal{}
				f.journal = &HelmUninstallJournal{store: grant.state.store}
				t.Cleanup(func() { _ = f.journal.Close() })
			}
			out, err := caller.ExecuteHelmInstall(ctx, grant, c, session)
			good := mode == "report-replaced" || mode == "store-replaced"
			if good {
				if err != nil || out.Outcome() != helmjournal.Completed {
					t.Fatal("wrapper retarget", err)
				}
			} else {
				if err == nil || f.calls.Load() != 0 {
					t.Fatal("invalid authority performed I/O", err, f.calls.Load())
				}
			}
			if _, err = caller.ExecuteHelmInstall(context.Background(), grant, c, session); !errors.Is(err, ErrHelmInstallExecutionConsent) {
				t.Fatal("failed attempt reusable", err)
			}
		})
	}
}
func TestHelmInstallExecutionFaults(t *testing.T) {
	modes := []string{"reject", "conflict", "proxy409", "retry-after", "redirect", "loss", "malformed", "missing-rv", "wrong-content", "warning", "encoding", "content-type", "trailer", "truncated", "observe-loss", "observe-change", "cancel", "revoke", "origin-revoke"}
	for _, phase := range []int{1, 2, 3} {
		for _, mode := range modes {
			t.Run(fmt.Sprintf("%d/%s", phase, mode), func(t *testing.T) {
				b, c, pre, dry, f := installExecutionSetup(t, 1)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				grant, err := b.AcknowledgeHelmInstallExecution(ctx, pre, c, "execute-session", true, f.journal)
				if err != nil {
					t.Fatal(err)
				}
				f.mode = mode
				f.faultAt = phase
				if mode == "cancel" || mode == "revoke" || mode == "origin-revoke" {
					f.onWrite = func(n int) {
						if n == phase {
							switch mode {
							case "cancel":
								cancel()
							case "revoke":
								grant.Discard()
							case "origin-revoke":
								dry.Discard()
							}
						}
					}
				}
				out, err := b.ExecuteHelmInstall(ctx, grant, c, "execute-session")
				if err == nil || out == nil || out.Outcome() == helmjournal.Completed || f.bad.Load() || int(f.writes.Load()) != phase {
					t.Fatal("unsafe fault handling", err, out, f.writes.Load(), f.bad.Load())
				}
				effects := out.Effects()
				for i := 0; i < phase-1; i++ {
					if effects[i].Outcome != helmjournal.Completed {
						t.Fatal("lost previous fact")
					}
				}
				for i := phase; i < len(effects); i++ {
					if effects[i].Outcome != helmjournal.NotStarted {
						t.Fatal("continued after failure")
					}
				}
				e := effects[phase-1]
				switch mode {
				case "reject", "conflict", "retry-after":
					if e.Outcome != helmjournal.Rejected {
						t.Fatal("lost exact rejection", e)
					}
				case "observe-loss", "observe-change":
					if e.Outcome != helmjournal.AcceptedPending {
						t.Fatal("lost acknowledged response", e)
					}
				case "cancel", "revoke", "origin-revoke":
					if e.Outcome != helmjournal.Unknown && e.Outcome != helmjournal.AcceptedPending {
						t.Fatal("cancellation lost potential effect", e)
					}
				default:
					if e.Outcome != helmjournal.Unknown {
						t.Fatal("unqualified response not Unknown", e)
					}
				}
				snapshot, e2 := f.journal.store.Load(out.JournalID())
				if e2 != nil || !snapshot.Closed || snapshot.Effects[phase-1].Outcome != e.Outcome {
					t.Fatal("history mismatch", e2)
				}
				raw, _ := json.Marshal(out)
				text := fmt.Sprintf("%+v %v %s", out, err, raw)
				if strings.Contains(text, "PRIVATE_") || strings.Contains(text, "Bearer current") {
					t.Fatal("sensitive diagnostic")
				}
				if _, err = b.ExecuteHelmInstall(context.Background(), grant, c, "execute-session"); !errors.Is(err, ErrHelmInstallExecutionConsent) {
					t.Fatal("retry authority")
				}
			})
		}
	}
}
func TestHelmInstallExecutionReadGuards(t *testing.T) {
	for _, mode := range []string{"no-patch", "namespace-changed", "history-conflict"} {
		t.Run(mode, func(t *testing.T) {
			b, c, pre, _, f := installExecutionSetup(t, 1)
			g, err := b.AcknowledgeHelmInstallExecution(context.Background(), pre, c, "execute-session", true, f.journal)
			if err != nil {
				t.Fatal(err)
			}
			f.mode = mode
			r, err := b.ExecuteHelmInstall(context.Background(), g, c, "execute-session")
			if err == nil || r.Outcome() != helmjournal.NotStarted || f.writes.Load() != 0 || f.bad.Load() {
				t.Fatal("read guard bypass", err)
			}
		})
	}
}
func TestHelmInstallExecutionCopiedConsent(t *testing.T) {
	b, c, pre, _, f := installExecutionSetup(t, 1)
	g, err := b.AcknowledgeHelmInstallExecution(context.Background(), pre, c, "execute-session", true, f.journal)
	if err != nil {
		t.Fatal(err)
	}
	copy := *g
	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	for _, v := range []*HelmInstallExecutionConsent{g, &copy} {
		go func(v *HelmInstallExecutionConsent) {
			defer wg.Done()
			_, e := b.ExecuteHelmInstall(context.Background(), v, c, "execute-session")
			errs <- e
		}(v)
	}
	wg.Wait()
	close(errs)
	success, denied := 0, 0
	for e := range errs {
		if e == nil {
			success++
		} else if errors.Is(e, ErrHelmInstallExecutionConsent) {
			denied++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || denied != 1 || f.writes.Load() != 3 || f.bad.Load() {
		t.Fatal("copy duplicated execution")
	}
}

func TestHelmInstallExecutionDeployedMetadata(t *testing.T) {
	for _, mode := range []string{"managed-null", "managed-array"} {
		t.Run(mode, func(t *testing.T) {
			b, c, pre, _, f := installExecutionSetup(t, 1)
			f.mode, f.faultAt = mode, 3
			g, err := b.AcknowledgeHelmInstallExecution(context.Background(), pre, c, "execute-session", true, f.journal)
			if err != nil {
				t.Fatal(err)
			}
			out, err := b.ExecuteHelmInstall(context.Background(), g, c, "execute-session")
			if !errors.Is(err, ErrHelmInstallExecutionUnknown) || out == nil || out.Outcome() != helmjournal.Unknown || f.writes.Load() != 3 || f.bad.Load() {
				t.Fatal("malformed field ownership accepted as an acknowledgement", err, out)
			}
			effects := out.Effects()
			if effects[0].Outcome != helmjournal.Completed || effects[1].Outcome != helmjournal.Completed || effects[2].Outcome != helmjournal.Unknown {
				t.Fatal("malformed acknowledgement erased prior facts", effects)
			}
			snap, err := f.journal.store.Load(out.JournalID())
			if err != nil || !snap.Closed || snap.Effects[2].Outcome != helmjournal.Unknown {
				t.Fatal("malformed acknowledgement not durably uncertain", err)
			}
		})
	}
}
