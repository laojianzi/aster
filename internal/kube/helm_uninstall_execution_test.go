package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func executeHelmFixture(f *helmExecutionFixture, c *HelmUninstallExecutionConsent) (*HelmUninstallExecution, error) {
	return f.b.ExecuteHelmUninstall(context.Background(), c, "session", "team", selectedHelmRevision())
}

func assertHelmExecutionPrivate(t *testing.T, r *HelmUninstallExecution, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal("private server body exposed", err)
	}
	if r == nil {
		return
	}
	wire, e := json.Marshal(r)
	if e != nil || string(wire) != "{}" {
		t.Fatal("execution private state serialized")
	}
	public, e := json.Marshal(struct {
		Effects           []HelmUninstallExecutionEffect
		Targets           []helmrelease.UninstallTarget
		Revision, Storage helmrelease.Revision
	}{r.Effects(), r.Targets(), r.Revision(), r.Storage()})
	if e != nil || bytes.Contains(public, []byte("PRIVATE")) || bytes.Contains(public, []byte("current")) {
		t.Fatal("sensitive execution projection")
	}
}

func assertExecutionKinds(t *testing.T, requests []helmExecutionRequest, want ...string) {
	t.Helper()
	got := make([]string, len(requests))
	for i, r := range requests {
		got[i] = r.kind
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mutation order = %v, want %v", got, want)
	}
}

func TestHelmUninstallExecutionHTTP(t *testing.T) {
	for _, mode := range []string{"one-target", "two-targets", "delete-status", "delete-status-code", "delete-pending-then-absent"} {
		t.Run(mode, func(t *testing.T) {
			f := newHelmExecutionFixture(t, mode == "two-targets")
			f.mode = mode
			c := f.consent(t)
			started := time.Now()
			r, err := executeHelmFixture(f, c)
			if err != nil || r == nil || r.Outcome() != HelmExecutionCompleted {
				t.Fatal("restricted execution", err, r)
			}
			assertHelmExecutionPrivate(t, r, err)
			requests, events, storage := f.inspect(t)
			wantKinds := []string{"begin", "delete", "finish"}
			if mode == "two-targets" {
				wantKinds = []string{"begin", "delete", "delete", "finish"}
			}
			assertExecutionKinds(t, requests, wantKinds...)
			if storage.Labels["status"] != "uninstalled" || storage.ResourceVersion != "9" || string(storage.UID) != selectedHelmRevision().UID {
				t.Fatal("retained history not actually finished")
			}
			if r.PreparationID() != f.p.ID() || r.ExpiresAt() != f.p.ExpiresAt() || r.Revision() != selectedHelmRevision() ||
				r.Storage().Status != "uninstalled" || r.Storage().ResourceVersion != "9" || r.StartedAt().Before(started) ||
				r.FinishedAt().Before(r.StartedAt()) || r.FinishedAt().After(time.Now()) {
				t.Fatal("result identity, history, or time renewed")
			}
			effects := r.Effects()
			if len(effects) != len(wantKinds) {
				t.Fatal("incomplete effect ledger")
			}
			for i, e := range effects {
				if e.Kind != wantKinds[i] || e.Outcome != HelmExecutionCompleted || e.Namespace != "team" || e.Name == "" || e.UID == "" || e.ResourceVersion == "" {
					t.Fatal("unverified ledger effect", i, e)
				}
				if e.Kind == "delete" {
					target := f.p.Targets()[i-1]
					if e.Name != target.Name || e.UID != target.UID || e.ResourceVersion != target.ResourceVersion {
						t.Fatal("DELETE changed sampled identity", e)
					}
				} else {
					wantRV := "7"
					if e.Kind == "finish" {
						wantRV = "8"
					}
					if e.Name != storage.Name || e.UID != string(storage.UID) || e.ResourceVersion != wantRV {
						t.Fatal("storage precondition advanced incorrectly", e)
					}
				}
			}
			// A source GET and metadata-only current-object reads precede the
			// first mutation. Actual absence precedes every later mutation.
			firstMutation, lastAbsence := -1, -1
			for i, e := range events {
				if strings.HasPrefix(e, "PATCH ") && firstMutation < 0 {
					firstMutation = i
				}
				if strings.HasPrefix(e, "absent ") {
					lastAbsence = i
				}
			}
			if firstMutation < 4 || lastAbsence < firstMutation || events[len(events)-1] == "absent sample" {
				t.Fatal("effect qualified without fresh reads and final history write", events)
			}
			if !reflect.DeepEqual(r.Targets(), f.p.Targets()) {
				t.Fatal("reviewed targets changed")
			}
			f.mu.Lock()
			dryPatches, dryDeletes := f.dryPatches, f.dryDeletes
			f.mu.Unlock()
			if dryPatches != 2 || dryDeletes != len(f.names) {
				t.Fatal("fresh admission or final storage dry-run missing", dryPatches, dryDeletes)
			}
			r.Targets()[0].UID = "tampered"
			r.Effects()[0].Outcome = HelmExecutionUnknown
			if r.Targets()[0].UID == "tampered" || r.Effects()[0].Outcome != HelmExecutionCompleted {
				t.Fatal("ledger aliases caller memory")
			}
			c.Discard()
			if r.Outcome() != HelmExecutionCompleted || !reflect.DeepEqual(effects, r.Effects()) {
				t.Fatal("historical outcomes erased by later revocation")
			}
			count := len(requests)
			copyConsent := *c
			if _, err := executeHelmFixture(f, &copyConsent); err == nil {
				t.Fatal("completed execution replayed")
			}
			requests, _, _ = f.inspect(t)
			if len(requests) != count {
				t.Fatal("consumed handle caused network replay")
			}
		})
	}
}

func TestHelmUninstallExecutionConsentLifetime(t *testing.T) {
	for _, mode := range []string{"false", "nil-report", "zero-report", "nil-consent", "zero-consent", "discard", "preflight-discard",
		"wrong-backend", "nil-backend", "wrong-session", "wrong-namespace", "wrong-revision", "wrong-uid", "wrong-release", "wrong-storage", "wrong-status", "wrong-version", "canceled", "expired-mint", "expired-dispatch",
		"revoked-in-begin", "revoked-in-delete", "preflight-revoked-in-delete"} {
		t.Run(mode, func(t *testing.T) {
			f := newHelmExecutionFixture(t, false)
			b, session, ns, row := f.b, "session", "team", selectedHelmRevision()
			if mode == "false" || mode == "nil-report" || mode == "zero-report" || mode == "expired-mint" {
				report := f.preflight
				if mode == "nil-report" {
					report = nil
				}
				if mode == "zero-report" {
					report = &HelmUninstallDryRun{}
				}
				if mode == "expired-mint" {
					report = expiredExecutionPreflight(t, f, time.Now().Add(-time.Minute))
				}
				c, err := b.AcknowledgeHelmUninstallExecution(report, session, ns, row, mode != "false", f.journal)
				if err == nil || c != nil {
					t.Fatal("unqualified report minted execution")
				}
				if mode == "false" {
					_ = f.consent(t)
				} // false acknowledgement never consumes true consent.
				requests, events, _ := f.inspect(t)
				if len(requests)+len(events) != 0 {
					t.Fatal("acknowledgement performed network I/O")
				}
				return
			}
			if mode == "expired-dispatch" {
				f.preflight = expiredExecutionPreflight(t, f, time.Now().Add(-helmrelease.PreparationLifetime+time.Second))
			}
			c := f.consent(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "nil-consent":
				c = nil
			case "zero-consent":
				c = &HelmUninstallExecutionConsent{}
			case "discard":
				c.Discard()
				c.Discard()
			case "preflight-discard":
				f.dryConsent.Discard()
			case "wrong-backend":
				var err error
				b, err = New(f.b.Config())
				if err != nil {
					t.Fatal(err)
				}
			case "nil-backend":
				b = nil
			case "wrong-session":
				session = "other"
			case "wrong-namespace":
				ns = "other"
			case "wrong-revision":
				row.ResourceVersion = "changed"
			case "wrong-uid":
				row.UID = "replacement"
			case "wrong-release":
				row.Release = "other"
			case "wrong-storage":
				row.StorageName = "sh.helm.release.v1.other.v2"
			case "wrong-status":
				row.Status = "uninstalling"
			case "wrong-version":
				row.Number++
			case "canceled":
				cancel()
			case "expired-dispatch":
				<-time.After(time.Until(f.preflight.ExpiresAt()) + 5*time.Millisecond)
			case "revoked-in-begin":
				f.onWrite = func(kind string) {
					if kind == "begin" {
						c.Discard()
					}
				}
			case "revoked-in-delete":
				f.onWrite = func(kind string) {
					if kind == "delete" {
						c.Discard()
					}
				}
			case "preflight-revoked-in-delete":
				f.onWrite = func(kind string) {
					if kind == "delete" {
						f.dryConsent.Discard()
					}
				}
			}
			r, err := b.ExecuteHelmUninstall(ctx, c, session, ns, row)
			if err == nil || r != nil && r.Outcome() == HelmExecutionCompleted {
				t.Fatal("unqualified or revoked execution accepted")
			}
			assertHelmExecutionPrivate(t, r, err)
			requests, events, _ := f.inspect(t)
			if strings.Contains(mode, "in-begin") {
				assertExecutionKinds(t, requests, "begin")
			} else if strings.Contains(mode, "in-delete") {
				assertExecutionKinds(t, requests, "begin", "delete")
			} else if len(requests)+len(events) != 0 {
				t.Fatal("unconsented read or write")
			}
			if c != nil {
				count := len(events)
				if _, err := executeHelmFixture(f, c); err == nil {
					t.Fatal("failed consent remained replayable")
				}
				_, events, _ = f.inspect(t)
				if len(events) != count {
					t.Fatal("replay issued reads")
				}
			}
		})
	}
	var absent *HelmUninstallExecutionConsent
	absent.Discard()
	(&HelmUninstallExecutionConsent{}).Discard()
}

// The old timestamps are confined to test-owned state. Public constructors are
// still used to validate the complete profile, snapshot bracket and targets.
func expiredExecutionPreflight(t *testing.T, f *helmExecutionFixture, started time.Time) *HelmUninstallDryRun {
	t.Helper()
	row := selectedHelmRevision()
	profile, err := helmrelease.InspectUninstallProfile(context.Background(), f.original, "team", row, "session", started)
	if err != nil {
		t.Fatal(err)
	}
	before := helmrelease.Snapshot{Namespace: "team", ResourceVersion: "collection", ObservedAt: started.Add(time.Second), Revisions: []helmrelease.Revision{row}}
	after := before
	after.ObservedAt = started.Add(3 * time.Second)
	items := []helmrelease.LiveItem{{Stored: profile.Preview().Items()[0], Resource: "configmaps", Namespace: "team", UID: "live-uid", ResourceVersion: "rv-a", State: "present", Markers: "match", ObservedAt: started.Add(2 * time.Second)}}
	p, err := helmrelease.FinishUninstallPreparation(context.Background(), profile, "session", before, after, items, started.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return &HelmUninstallDryRun{state: &helmDryRunConsentState{preparation: &HelmUninstallPreparation{owner: f.b.identity, result: p}, used: true}, finished: started.Add(5 * time.Second)}
}

func TestHelmUninstallExecutionSingleUse(t *testing.T) {
	t.Run("caller-wrapper-reassignment-cannot-change-grant", func(t *testing.T) {
		f := newHelmExecutionFixture(t, false)
		id, targets, expiry := f.p.ID(), f.p.Targets(), f.p.ExpiresAt()
		originalPreparation := *f.p
		// A later real preparation for the same storage row has its own
		// review identity, expiry and observed target. Reassigning the original
		// caller slot must not transfer those values into the old dry-run report.
		f.mu.Lock()
		originalObject := f.objects["sample"]
		replacementObject := *originalObject.DeepCopy()
		replacementObject.UID, replacementObject.ResourceVersion = "fresh-target-uid", "fresh-target-rv"
		f.objects["sample"] = replacementObject
		f.mu.Unlock()
		fresh, err := f.b.PrepareHelmUninstall(context.Background(), "team", selectedHelmRevision(), "session", true)
		f.mu.Lock()
		f.objects["sample"] = originalObject
		f.mu.Unlock()
		if err != nil || fresh == nil || fresh.ID() == id || !fresh.ExpiresAt().After(expiry) || reflect.DeepEqual(fresh.Targets(), targets) {
			t.Fatal("fresh preparation did not establish distinct reviewed values", err)
		}
		*f.p = *fresh
		if f.preflight.PreparationID() != id || f.preflight.ExpiresAt() != expiry || !reflect.DeepEqual(f.preflight.Targets(), targets) {
			t.Fatal("caller renewed or substituted a completed dry-run before execution mint")
		}
		*f.p = HelmUninstallPreparation{}
		c := f.consent(t)
		originalReport := *f.preflight
		// Both assignments are legal for callers outside package kube. The
		// execution grant must own its reviewed value, not these caller slots.
		*f.preflight = HelmUninstallDryRun{}
		*f.p = HelmUninstallPreparation{}
		stop, stopped := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(stopped)
			for {
				select {
				case <-stop:
					return
				default:
					*f.preflight, *f.p = originalReport, originalPreparation
					*f.preflight, *f.p = HelmUninstallDryRun{}, HelmUninstallPreparation{}
					runtime.Gosched()
				}
			}
		}()
		r, err := executeHelmFixture(f, c)
		close(stop)
		<-stopped
		if err != nil || r == nil || r.Outcome() != HelmExecutionCompleted || r.PreparationID() != id || r.ExpiresAt() != expiry || !reflect.DeepEqual(r.Targets(), targets) {
			t.Fatal("caller reassigned an already-minted grant", err)
		}
		requests, _, _ := f.inspect(t)
		assertExecutionKinds(t, requests, "begin", "delete", "finish")
	})
	t.Run("owned-wrapper-preserves-original-revocation", func(t *testing.T) {
		f := newHelmExecutionFixture(t, false)
		*f.p = HelmUninstallPreparation{}
		c := f.consent(t)
		*f.preflight = HelmUninstallDryRun{}
		*f.p = HelmUninstallPreparation{}
		f.dryConsent.Discard()
		if r, err := executeHelmFixture(f, c); err == nil || r != nil && r.Outcome() != HelmExecutionNotStarted {
			t.Fatal("owned snapshot detached revocation")
		}
		requests, events, _ := f.inspect(t)
		if len(requests)+len(events) != 0 {
			t.Fatal("revoked snapshot dispatched")
		}
	})
	t.Run("report-copies-cannot-mint-twice", func(t *testing.T) {
		f := newHelmExecutionFixture(t, false)
		c := f.consent(t)
		copied := *f.preflight
		if other, err := f.b.AcknowledgeHelmUninstallExecution(&copied, "session", "team", selectedHelmRevision(), true, f.journal); err == nil || other != nil {
			t.Fatal("copied preflight minted second consent")
		}
		c.Discard()
		if other, err := f.b.AcknowledgeHelmUninstallExecution(f.preflight, "session", "team", selectedHelmRevision(), true, f.journal); err == nil || other != nil {
			t.Fatal("discard reset mint authority")
		}
		_, events, _ := f.inspect(t)
		if len(events) != 0 {
			t.Fatal("mint caused requests")
		}
	})
	t.Run("concurrent-mint", func(t *testing.T) {
		f := newHelmExecutionFixture(t, false)
		var wg sync.WaitGroup
		var wins atomic.Int32
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				copied := *f.preflight
				c, err := f.b.AcknowledgeHelmUninstallExecution(&copied, "session", "team", selectedHelmRevision(), true, f.journal)
				if err == nil && c != nil {
					wins.Add(1)
					c.Discard()
				}
			}()
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatal("concurrent mint winners", wins.Load())
		}
	})
	t.Run("concurrent-execution-copies", func(t *testing.T) {
		f := newHelmExecutionFixture(t, true)
		c := f.consent(t)
		var wg sync.WaitGroup
		var wins atomic.Int32
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				copied := *c
				r, err := executeHelmFixture(f, &copied)
				if err == nil && r != nil && r.Outcome() == HelmExecutionCompleted {
					wins.Add(1)
				}
			}()
		}
		wg.Wait()
		requests, _, _ := f.inspect(t)
		assertExecutionKinds(t, requests, "begin", "delete", "delete", "finish")
		if wins.Load() != 1 {
			t.Fatal("concurrent execution winners", wins.Load())
		}
	})
}

func TestHelmUninstallExecutionRevalidation(t *testing.T) {
	for _, mode := range []string{"pre-source-uid", "pre-source-rv", "pre-extra-revision", "pre-storage-denied", "pre-target-uid", "pre-target-rv",
		"pre-target-keep", "pre-target-hook", "pre-target-finalizer", "pre-target-owner", "pre-target-marker", "source-after-begin", "revision-after-begin",
		"payload-after-begin", "source-after-delete", "revision-after-delete", "second-replaced"} {
		t.Run(mode, func(t *testing.T) {
			f := newHelmExecutionFixture(t, true)
			c := f.consent(t)
			f.mode = mode
			r, err := executeHelmFixture(f, c)
			if err == nil || r == nil || r.Outcome() == HelmExecutionCompleted {
				t.Fatal("changed execution continued", err)
			}
			assertHelmExecutionPrivate(t, r, err)
			requests, _, storage := f.inspect(t)
			if strings.HasPrefix(mode, "pre-") {
				if len(requests) != 0 || r.Outcome() != HelmExecutionNotStarted || storage.Labels["status"] != "deployed" {
					t.Fatal("changed preparation mutated storage")
				}
			} else if strings.Contains(mode, "after-begin") {
				assertExecutionKinds(t, requests, "begin")
				if r.Outcome() != HelmExecutionAcceptedPending || r.Effects()[0].Outcome != HelmExecutionAcceptedPending {
					t.Fatal("accepted storage effect lost", r.Outcome())
				}
			} else {
				assertExecutionKinds(t, requests, "begin", "delete")
				if r.Outcome() != HelmExecutionPartial || r.Effects()[0].Outcome != HelmExecutionCompleted {
					t.Fatal("partial effects lost", r.Outcome())
				}
			}
			for _, e := range r.Effects() {
				if e.Kind == "finish" && e.Outcome != HelmExecutionNotStarted {
					t.Fatal("changed history was finalized")
				}
			}
		})
	}
}

func TestHelmUninstallExecutionStrictResponses(t *testing.T) {
	for _, stage := range []string{"begin", "delete", "finish"} {
		faults := []string{"invalid", "trailing", "duplicate", "media", "warning", "encoding", "202", "change-uid", "change-name", "change-ns", "change-empty-rv", "change-same-rv", "change-unknown"}
		if stage == "delete" {
			faults = append(faults, "change-full-kind", "change-finalizer", "change-not-terminating", "change-marker", "status-no-uid", "status-wrong-name", "status-message", "status-failure", "status-cause")
		} else {
			faults = append(faults, "change-payload", "change-status")
		}
		for _, fault := range faults {
			t.Run(stage+"/"+fault, func(t *testing.T) {
				f := newHelmExecutionFixture(t, false)
				c := f.consent(t)
				f.mode = stage + "-" + fault
				r, err := executeHelmFixture(f, c)
				if err == nil || r == nil || r.Outcome() != HelmExecutionUnknown {
					t.Fatal("unrecognized mutation response not Unknown", err, r)
				}
				assertHelmExecutionPrivate(t, r, err)
				requests, _, _ := f.inspect(t)
				want := []string{"begin"}
				if stage != "begin" {
					want = append(want, "delete")
				}
				if stage == "finish" {
					want = append(want, "finish")
				}
				assertExecutionKinds(t, requests, want...)
				effects := r.Effects()
				index := len(want) - 1
				if effects[index].Kind != stage || effects[index].Outcome != HelmExecutionUnknown {
					t.Fatal("ambiguous effect lost", effects)
				}
				for i := 0; i < index; i++ {
					if effects[i].Outcome != HelmExecutionCompleted {
						t.Fatal("prior verified effect erased")
					}
				}
				for i := index + 1; i < len(effects); i++ {
					if effects[i].Outcome != HelmExecutionNotStarted {
						t.Fatal("later effect scheduled after unsupported response")
					}
				}
			})
		}
	}
}

func TestHelmUninstallExecutionPartialUnknown(t *testing.T) {
	for _, tc := range []struct {
		name, mode  string
		faultAt     int
		want        HelmUninstallExecutionOutcome
		kinds       []string
		failedIndex int
	}{
		{"begin-rejected", "begin-reject-409", 0, HelmExecutionRejected, []string{"begin"}, 0},
		{"anonymous422-is-not-attributable", "begin-anonymous422", 0, HelmExecutionUnknown, []string{"begin"}, 0},
		{"delete-rejected", "delete-reject-403", 0, HelmExecutionPartial, []string{"begin", "delete"}, 1},
		{"second-delete-rejected", "delete-reject-409", 3, HelmExecutionPartial, []string{"begin", "delete", "delete"}, 2},
		{"finish-rejected", "finish-reject-422", 0, HelmExecutionPartial, []string{"begin", "delete", "delete", "finish"}, 3},
		{"begin-commit-response-loss", "begin-drop", 0, HelmExecutionUnknown, []string{"begin"}, 0},
		{"first-delete-commit-response-loss", "delete-drop", 0, HelmExecutionUnknown, []string{"begin", "delete"}, 1},
		{"second-delete-commit-response-loss", "delete-drop", 3, HelmExecutionUnknown, []string{"begin", "delete", "delete"}, 2},
		{"finish-commit-response-loss", "finish-drop", 0, HelmExecutionUnknown, []string{"begin", "delete", "delete", "finish"}, 3},
		{"bare-forbidden-is-not-proof", "delete-bare-403", 0, HelmExecutionUnknown, []string{"begin", "delete"}, 1},
		{"proxy-404-is-not-proof", "delete-bare-404", 0, HelmExecutionUnknown, []string{"begin", "delete"}, 1},
		{"server-unavailable-is-not-proof", "delete-bare-503", 0, HelmExecutionUnknown, []string{"begin", "delete"}, 1},
		{"write-redirect-not-followed", "begin-redirect", 0, HelmExecutionUnknown, []string{"begin"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHelmExecutionFixture(t, true)
			c := f.consent(t)
			f.mode, f.faultAt = tc.mode, tc.faultAt
			r, err := executeHelmFixture(f, c)
			if err == nil || r == nil || r.Outcome() != tc.want {
				t.Fatal("effect classification", tc.want, err, r)
			}
			assertHelmExecutionPrivate(t, r, err)
			requests, _, storage := f.inspect(t)
			assertExecutionKinds(t, requests, tc.kinds...)
			effects := r.Effects()
			wantEffect := HelmExecutionUnknown
			if strings.Contains(tc.mode, "reject-") {
				wantEffect = HelmExecutionRejected
			}
			if effects[tc.failedIndex].Outcome != wantEffect {
				t.Fatal("failed effect classification", effects[tc.failedIndex])
			}
			for i := 0; i < tc.failedIndex; i++ {
				if effects[i].Outcome != HelmExecutionCompleted {
					t.Fatal("verified prior effect erased", effects)
				}
			}
			for i := tc.failedIndex + 1; i < len(effects); i++ {
				if effects[i].Outcome != HelmExecutionNotStarted {
					t.Fatal("later effect scheduled")
				}
			}
			if strings.Contains(tc.mode, "drop") {
				wantStatus := "uninstalling"
				if strings.HasPrefix(tc.mode, "finish") {
					wantStatus = "uninstalled"
				}
				if storage.Labels["status"] != wantStatus {
					t.Fatal("fixture did not actually commit before response loss")
				}
			}
			count := len(requests)
			if _, err := executeHelmFixture(f, c); err == nil {
				t.Fatal("uncertain execution replayed")
			}
			requests, _, _ = f.inspect(t)
			if len(requests) != count {
				t.Fatal("uncertain mutation retried")
			}
		})
	}
	t.Run("cancel-after-delete-dispatch", func(t *testing.T) {
		f := newHelmExecutionFixture(t, true)
		c := f.consent(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f.mode = "delete-cancel"
		f.onWrite = func(kind string) {
			if kind == "delete" {
				cancel()
			}
		}
		r, err := f.b.ExecuteHelmUninstall(ctx, c, "session", "team", selectedHelmRevision())
		if err == nil || r == nil || r.Outcome() != HelmExecutionUnknown || r.Effects()[0].Outcome != HelmExecutionCompleted || r.Effects()[1].Outcome != HelmExecutionUnknown {
			t.Fatal("post-dispatch cancellation was represented as no-op", err)
		}
		requests, _, _ := f.inspect(t)
		assertExecutionKinds(t, requests, "begin", "delete")
	})
}

// Kubernetes BeforeDelete attributes UID/RV conflicts to the canonical GVK
// kind, whereas other rejection paths use the plural API resource. Exercise the
// complete HTTP execution boundary so the allowed shape cannot erase prior
// effects or broaden another resource, status code, or identity into rejection.
func TestHelmUninstallExecutionCanonicalConflictResponses(t *testing.T) {
	for _, tc := range []struct {
		name, mode, kind string
		edit             func(*metav1.Status)
		rejected         bool
	}{
		{"canonical-configmap-409", "delete-reject-409", "ConfigMap", nil, true},
		{"canonical-configmap-409-no-uid", "delete-reject-409", "ConfigMap", func(s *metav1.Status) { s.Details.UID = "" }, true},
		{"plural-configmaps-409", "delete-reject-409", "configmaps", nil, true},
		{"canonical-configmap-403", "delete-reject-403", "ConfigMap", nil, false},
		{"canonical-configmap-404", "delete-reject-404", "ConfigMap", nil, false},
		{"canonical-configmap-422", "delete-reject-422", "ConfigMap", nil, false},
		{"lowercase-configmap", "delete-reject-409", "configmap", nil, false},
		{"uppercase-configmap", "delete-reject-409", "CONFIGMAP", nil, false},
		{"capitalized-plural-configmaps", "delete-reject-409", "ConfigMaps", nil, false},
		{"secret-kind", "delete-reject-409", "Secret", nil, false},
		{"deployment-kind", "delete-reject-409", "Deployment", nil, false},
		{"missing-name", "delete-reject-409", "ConfigMap", func(s *metav1.Status) { s.Details.Name = "" }, false},
		{"wrong-name", "delete-reject-409", "ConfigMap", func(s *metav1.Status) { s.Details.Name = "other" }, false},
		{"wrong-group", "delete-reject-409", "ConfigMap", func(s *metav1.Status) { s.Details.Group = "apps" }, false},
		{"wrong-uid", "delete-reject-409", "ConfigMap", func(s *metav1.Status) { s.Details.UID = "replacement" }, false},
		{"wrong-reason", "delete-reject-409", "ConfigMap", func(s *metav1.Status) { s.Reason = metav1.StatusReasonForbidden }, false},
		{"wrong-envelope-code", "delete-reject-409", "ConfigMap", func(s *metav1.Status) { s.Code = 403 }, false},
		{"success-envelope", "delete-reject-409", "ConfigMap", func(s *metav1.Status) { s.Status = metav1.StatusSuccess }, false},
		{"unexpected-causes", "delete-reject-409", "ConfigMap", func(s *metav1.Status) {
			s.Details.Causes = []metav1.StatusCause{{Type: metav1.CauseTypeFieldValueInvalid, Message: "PRIVATE_CAUSE"}}
		}, false},
		{"unexpected-retry-after", "delete-reject-409", "ConfigMap", func(s *metav1.Status) { s.Details.RetryAfterSeconds = 1 }, false},
		{"anonymous-conflict", "delete-reject-409", "ConfigMap", func(s *metav1.Status) { s.Details = &metav1.StatusDetails{} }, false},
		{"anonymous-invalid", "begin-reject-422", "ConfigMap", func(s *metav1.Status) { s.Details = &metav1.StatusDetails{} }, false},
		{"nil-details", "delete-reject-409", "ConfigMap", func(s *metav1.Status) { s.Details = nil }, false},
		{"canonical-kind-on-secret-request", "begin-reject-409", "ConfigMap", nil, false},
		{"singular-secret-kind-on-secret-request", "begin-reject-409", "Secret", nil, false},
		{"plural-secrets-409", "begin-reject-409", "secrets", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHelmExecutionFixture(t, true)
			c := f.consent(t)
			f.mode = tc.mode
			f.onReject = func(s *metav1.Status) {
				s.Details.Kind = tc.kind
				if tc.edit != nil {
					tc.edit(s)
				}
			}
			want, wantEffect, wantErr := HelmExecutionUnknown, HelmExecutionUnknown, ErrHelmExecutionUnknown
			failedIndex := 1
			wantKinds, wantStatus := []string{"begin", "delete"}, "uninstalling"
			if strings.HasPrefix(tc.mode, "begin-") {
				failedIndex, wantKinds, wantStatus = 0, []string{"begin"}, "deployed"
			}
			if tc.rejected {
				want, wantEffect, wantErr = HelmExecutionPartial, HelmExecutionRejected, ErrHelmExecutionRejected
				if failedIndex == 0 {
					want = HelmExecutionRejected
				}
			}
			r, err := executeHelmFixture(f, c)
			if !errors.Is(err, wantErr) || r == nil || r.Outcome() != want {
				t.Fatal("canonical conflict attribution", want, err, r)
			}
			assertHelmExecutionPrivate(t, r, err)
			requests, events, storage := f.inspect(t)
			assertExecutionKinds(t, requests, wantKinds...)
			if storage.Labels["status"] != wantStatus {
				t.Fatal("rejection changed retained storage")
			}
			effects := r.Effects()
			if effects[failedIndex].Outcome != wantEffect {
				t.Fatal("exact response shape changed effect classification", effects)
			}
			for i, effect := range effects {
				if i < failedIndex && effect.Outcome != HelmExecutionCompleted ||
					i > failedIndex && effect.Outcome != HelmExecutionNotStarted {
					t.Fatal("prior effect erased or later mutation scheduled", effects)
				}
			}
			f.mu.Lock()
			deleted := len(f.deleted)
			f.mu.Unlock()
			if deleted != 0 {
				t.Fatal("a target was deleted after a rejected conditional request")
			}
			if _, replayErr := executeHelmFixture(f, c); replayErr == nil {
				t.Fatal("rejection or uncertainty renewed execution consent")
			}
			afterRequests, afterEvents, _ := f.inspect(t)
			if len(afterRequests) != len(requests) || len(afterEvents) != len(events) {
				t.Fatal("consumed conflict execution caused further I/O")
			}
		})
	}
}

func TestHelmUninstallExecutionAbsence(t *testing.T) {
	for _, mode := range []string{"delete-pending", "delete-replacement", "delete-proxy404", "delete-wrong404", "delete-cause404", "delete-uid404", "delete-null404",
		"delete-duplicate404", "delete-trailing404", "delete-absence-denied", "delete-absence-budget", "delete-absence-warning", "delete-late404"} {
		t.Run(mode, func(t *testing.T) {
			f := newHelmExecutionFixture(t, true)
			c := f.consent(t)
			f.mode = mode
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "delete-late404" {
				f.onRead = func(path string) {
					if f.deletes > 0 && strings.HasSuffix(path, "/configmaps/sample") {
						cancel()
					}
				}
			}
			r, err := f.b.ExecuteHelmUninstall(ctx, c, "session", "team", selectedHelmRevision())
			if err == nil || r == nil || r.Outcome() == HelmExecutionCompleted {
				t.Fatal("acknowledgement confused with absence", err)
			}
			assertHelmExecutionPrivate(t, r, err)
			requests, _, storage := f.inspect(t)
			assertExecutionKinds(t, requests, "begin", "delete")
			if storage.Labels["status"] != "uninstalling" || r.Effects()[0].Outcome != HelmExecutionCompleted || r.Effects()[1].Outcome != HelmExecutionAcceptedPending ||
				r.Effects()[2].Outcome != HelmExecutionNotStarted || r.Effects()[3].Outcome != HelmExecutionNotStarted {
				t.Fatal("accepted or pending evidence erased", r.Effects())
			}
			if mode == "delete-pending" {
				if r.Outcome() != HelmExecutionPartial {
					t.Fatal("pending DELETE hid preceding storage mutation")
				}
				f.mu.Lock()
				reads := f.absenceReads["sample"]
				f.mu.Unlock()
				if reads < 2 || reads > 80 {
					t.Fatal("unbounded or absent disappearance polling", reads)
				}
			}
		})
	}
}

type executionRoundTripper func(*http.Request) (*http.Response, error)

func (f executionRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHelmUninstallExecutionBodyAndBudgets(t *testing.T) {
	t.Run("shared-response-budget-stops-further-I-O", func(t *testing.T) {
		f := newHelmExecutionFixture(t, false)
		runner := helmExecutionRun{b: f.b, ctx: context.Background(), active: func() error { return nil }, ns: "team", remaining: 2 * len(f.wire)}
		for i := 0; i < 2; i++ {
			data, status, err := runner.read(context.Background(), "secrets", selectedHelmRevision().StorageName, helmrelease.MaxSecretBytes, false)
			if err != nil || status != 200 || len(data) != len(f.wire) {
				t.Fatal("bounded response", err)
			}
			clear(data)
		}
		if runner.remaining != 0 {
			t.Fatal("read budget not charged exactly")
		}
		_, before, _ := f.inspect(t)
		if _, _, err := runner.read(context.Background(), "secrets", selectedHelmRevision().StorageName, helmrelease.MaxSecretBytes, false); err == nil {
			t.Fatal("exhausted read admitted")
		}
		if _, dispatched, _, err := runner.mutate(0, http.MethodPatch, "secrets", selectedHelmRevision().StorageName, []byte("[]"), helmrelease.MaxSecretBytes); err == nil || dispatched {
			t.Fatal("exhausted mutation admitted")
		}
		requests, after, _ := f.inspect(t)
		if len(requests) != 0 || !reflect.DeepEqual(before, after) {
			t.Fatal("exhaustion caused further network work")
		}
	})
	for _, stage := range []string{"begin", "delete", "finish"} {
		for _, size := range []string{"large-header", "large-stream"} {
			t.Run(stage+"/"+size, func(t *testing.T) {
				f := newHelmExecutionFixture(t, false)
				c := f.consent(t)
				f.mode = stage + "-" + size
				r, err := executeHelmFixture(f, c)
				if err == nil || r == nil || r.Outcome() != HelmExecutionUnknown {
					t.Fatal("oversized response qualified")
				}
				assertHelmExecutionPrivate(t, r, err)
				requests, _, _ := f.inspect(t)
				want := []string{"begin"}
				if stage != "begin" {
					want = append(want, "delete")
				}
				if stage == "finish" {
					want = append(want, "finish")
				}
				assertExecutionKinds(t, requests, want...)
			})
		}
	}
	t.Run("actual-request-body-is-owned-and-unreplayable", func(t *testing.T) {
		f := newHelmExecutionFixture(t, false)
		c := f.consent(t)
		base := f.b.metricsHTTP.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		var wg sync.WaitGroup
		var writes, replayable atomic.Int32
		f.b.metricsHTTP.Transport = executionRoundTripper(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet || req.URL.Query().Encode() == "dryRun=All" {
				return base.RoundTrip(req)
			}
			writes.Add(1)
			if req.GetBody != nil || req.Body == nil || req.ContentLength <= 0 {
				replayable.Add(1)
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				buffer := make([]byte, 7)
				for {
					if _, err := req.Body.Read(buffer); err != nil {
						break
					}
				}
				_ = req.Body.Close()
			}()
			return &http.Response{StatusCode: 503, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader("PRIVATE_TRANSPORT")), Request: req}, nil
		})
		r, err := executeHelmFixture(f, c)
		wg.Wait()
		if err == nil || r == nil || r.Outcome() != HelmExecutionUnknown || writes.Load() != 1 || replayable.Load() != 0 {
			t.Fatal("body replay or early response ownership", err)
		}
		assertHelmExecutionPrivate(t, r, err)
		_, _, _ = f.inspect(t)
		if !bytes.Equal(f.original, f.wire) {
			t.Fatal("test-owned source bytes mutated by cleanup")
		}
		if !errors.Is(err, ErrHelmExecutionUnknown) {
			t.Fatal("transport body leaked through error classification", err)
		}
	})
}
