package kube

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
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
			if f.calls.Load() != int32(16*n+40) {
				t.Fatal("single-page request census changed", f.calls.Load())
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

// QPS alone cannot establish an opaque limiter's burst or completion capacity.
type opaqueInstallRateLimiter struct{ flowcontrol.RateLimiter }

func (opaqueInstallRateLimiter) QPS() float32 { return 0.00001 }

func TestHelmInstallExecutionBudgetAdmission(t *testing.T) {
	for _, mode := range []string{"default-32", "default-small", "configured-fast-32", "short-parent-deadline", "short-original-expiry", "opaque-limiter", "sensitive-read-bytes", "final-patch-bytes"} {
		t.Run(mode, func(t *testing.T) {
			n, cfg := 1, rest.Config{}
			var values map[string]any
			success := mode == "default-small" || mode == "configured-fast-32" || mode == "opaque-limiter"
			switch mode {
			case "default-32":
				n = 32
			case "configured-fast-32":
				n, cfg.QPS, cfg.Burst = 32, 1000, 1000
			case "opaque-limiter":
				n = 32
				cfg.QPS, cfg.Burst = 0.00001, 1
				cfg.RateLimiter = opaqueInstallRateLimiter{flowcontrol.NewFakeAlwaysRateLimiter()}
			case "sensitive-read-bytes", "final-patch-bytes":
				cfg.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()
				entropyBytes := 650 << 10
				if mode == "sensitive-read-bytes" {
					n, entropyBytes = 32, 360<<10
				}
				// Deterministic synthetic values resist compression while staying
				// within the existing capture/storage/preflight profile.
				data := make([]byte, entropyBytes)
				_, _ = rand.New(rand.NewSource(109)).Read(data)
				values = map[string]any{"synthetic": base64.StdEncoding.EncodeToString(data)}
				clear(data)
			}
			b, c, pre, _, f := installExecutionSetupConfigured(t, n, cfg, values)
			if cfg.RateLimiter == nil {
				wantQPS, wantBurst := float32(20), 40
				if mode == "configured-fast-32" {
					wantQPS, wantBurst = 1000, 1000
				}
				if b.ownedRateLimit == nil || b.ownedRateLimit.qps != wantQPS || b.ownedRateLimit.burst != wantBurst {
					t.Fatal("missing constructor-owned request capacity")
				}
			} else if b.ownedRateLimit != nil {
				t.Fatal("opaque limiter was assigned an invented capacity")
			}
			if mode == "sensitive-read-bytes" || mode == "final-patch-bytes" {
				pending, patch, e := helmrelease.InstallExecutionSizeBounds(context.Background(), c, pre.state.plan)
				if e != nil {
					t.Fatal(e)
				}
				readExceeded := pending*(2*n+4) > installExecutionReadBytes
				patchExceeded := patch > helmrelease.MaxSecretBytes
				if readExceeded != (mode == "sensitive-read-bytes") || patchExceeded != (mode == "final-patch-bytes") {
					t.Fatal("fixture does not independently exercise its byte budget", pending, patch)
				}
			}
			grant, err := b.AcknowledgeHelmInstallExecution(context.Background(), pre, c, "execute-session", true, f.journal)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if mode == "short-parent-deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
				defer cancel()
			}
			if mode == "short-original-expiry" {
				grant.state.origin.preparation.expires = time.Now().Add(200 * time.Millisecond)
			}
			out, err := b.ExecuteHelmInstall(ctx, grant, c, "execute-session")
			if success {
				if err != nil || out == nil || out.Outcome() != helmjournal.Completed || !out.JournalComplete() || f.writes.Load() != int32(n+2) || f.bad.Load() {
					t.Fatal("admissible execution failed", err, out, f.writes.Load(), f.bad.Load())
				}
				if f.calls.Load() != int32(16*n+40) {
					t.Fatal("single-page request census changed", f.calls.Load())
				}
			} else {
				if !errors.Is(err, ErrHelmInstallExecutionBudget) || out == nil || out.Outcome() != helmjournal.NotStarted || out.JournalID() != "" || out.JournalComplete() || len(out.Effects()) != n+2 || out.FinishedAt().IsZero() {
					t.Fatal("impossible execution was not refused before admission", err, out)
				}
				if f.calls.Load() != 0 || f.writes.Load() != 0 || f.bad.Load() {
					t.Fatal("budget refusal made a request", f.calls.Load(), f.writes.Load())
				}
				ids, e := f.journal.IDs()
				if e != nil || len(ids) != 0 {
					t.Fatal("budget refusal created a WAL record", e, ids)
				}
				for _, effect := range out.Effects() {
					if effect.Outcome != helmjournal.NotStarted || effect.UID != "" || effect.ObservedResourceVersion != "" {
						t.Fatal("budget refusal reported a server effect", effect)
					}
				}
			}
			if _, err = b.ExecuteHelmInstall(context.Background(), grant, c, "execute-session"); !errors.Is(err, ErrHelmInstallExecutionConsent) {
				t.Fatal("budget attempt left a reusable grant", err)
			}
			if _, err = b.AcknowledgeHelmInstallExecution(context.Background(), pre, c, "execute-session", true, f.journal); !errors.Is(err, ErrHelmInstallExecutionConsent) {
				t.Fatal("budget attempt allowed a second mint", err)
			}
		})
	}
}

func TestHelmInstallExecutionRateCapacity(t *testing.T) {
	for _, tc := range []struct {
		name      string
		limit     *backendRateLimit
		requests  int
		remaining time.Duration
		refuse    bool
	}{
		{"default-32", &backendRateLimit{20, 40}, 552, 10 * time.Second, true},
		{"full-burst-boundary", &backendRateLimit{20, 40}, 240, 10 * time.Second, false},
		{"one-over-capacity", &backendRateLimit{20, 40}, 241, 10 * time.Second, true},
		{"rounded-refill", &backendRateLimit{1, 40}, 41, time.Nanosecond, false},
		{"tiny-refill", &backendRateLimit{0.00001, 40}, 42, time.Second, true},
		{"large-burst", &backendRateLimit{20, int(^uint(0) >> 1)}, 552, time.Nanosecond, false},
		{"unknown", nil, 552, time.Nanosecond, false},
		{"nonfinite", &backendRateLimit{float32(math.Inf(1)), 40}, 552, time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := installRateCapacityInsufficient(tc.limit, tc.requests, tc.remaining); got != tc.refuse {
				t.Fatal("incorrect optimistic capacity refusal", got)
			}
		})
	}
}
