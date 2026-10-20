package kube

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
	"k8s.io/client-go/util/flowcontrol"
)

func TestHelmUninstallReconciliationHTTP(t *testing.T) {
	cases := []struct {
		name  string
		valid bool
	}{
		{"success", true}, {"target-modified", true}, {"target-replacement", true}, {"target-absent", true}, {"target-flags", true},
		{"target-marker-missing", true}, {"target-marker-mismatch", true}, {"list-unrelated", true}, {"list-pagination", true}, {"list-page-token", true},
		{"list-missing", false}, {"list-older", false}, {"list-newer", false}, {"list-replaced", false}, {"list-rv-before", false}, {"list-status-before", false},
		{"list-rv-after", false}, {"list-status-after", false}, {"list-missing-after", false}, {"list-duplicate", false}, {"list-page-rv", false},
		{"list-page-repeat", false}, {"list-page-duplicate", false}, {"list-page-limit", false}, {"list-count-limit", false}, {"list-page-items", false},
		{"list-page-bytes", false}, {"list-total-bytes", false}, {"list-full-object", false}, {"list-forbidden", false}, {"list-unauthorized", false},
		{"list-retry", false}, {"list-redirect", false}, {"list-duplicate-field", false}, {"list-unknown-field", false}, {"list-null", false},
		{"list-null-metadata", false}, {"list-invalid-json", false}, {"list-missing-content-type", false}, {"list-duplicate-content-type", false},
		{"list-wrong-content-type", false}, {"list-warning", false}, {"list-encoding", false}, {"list-duplicate-encoding", false}, {"list-truncated", false},
		{"list-oversized-length", false}, {"list-oversized-stream", false},
		{"target-wrong-name", false}, {"target-wrong-namespace", false}, {"target-empty-uid", false}, {"target-control-uid", false},
		{"target-empty-rv", false}, {"target-control-rv", false}, {"target-full-object", false}, {"target-forbidden", false}, {"target-unauthorized", false},
		{"target-retry", false}, {"target-redirect", false}, {"target-proxy404", false}, {"target-invalid-json", false}, {"target-duplicate-field", false},
		{"target-unknown-field", false}, {"target-null", false}, {"target-null-metadata", false}, {"target-missing-content-type", false},
		{"target-duplicate-content-type", false}, {"target-wrong-content-type", false}, {"target-warning", false}, {"target-encoding", false},
		{"target-duplicate-encoding", false}, {"target-truncated", false}, {"target-oversized-length", false}, {"target-oversized-stream", false},
		{"target-absent-wrong-name", false}, {"target-absent-wrong-kind", false}, {"target-absent-wrong-group", false}, {"target-absent-uid", false},
		{"target-absent-cause", false}, {"target-absent-retry", false}, {"target-absent-listmeta", false}, {"target-absent-null-details", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newHelmReconciliationFixture(t, 2)
			f.mode = tc.name
			c := f.consent(t)
			historyOutcome := f.recovery.Outcome()
			out, err := f.b.ObserveHelmUninstallRecovery(context.Background(), c, "new-session", "team", f.row)
			lists, objects, requests := f.inspect(t)
			if tc.valid {
				if err != nil || out == nil || len(out.Items()) != 2 {
					t.Fatal("observation", err)
				}
				if out.ID() != f.recovery.ID() || out.Namespace() != "team" || out.Revision() != f.row || out.HistoryOutcome() != historyOutcome || f.recovery.Outcome() != historyOutcome {
					t.Fatal("identity or historical outcome changed")
				}
				if out.StartedAt().IsZero() || out.FinishedAt().Before(out.StartedAt()) || !out.FinishedAt().Before(out.ExpiresAt()) || !out.ExpiresAt().Equal(c.state.expires) {
					t.Fatal("observation chronology")
				}
				wantLists := 2
				if tc.name == "list-pagination" || tc.name == "list-page-token" {
					wantLists = 4
				}
				if lists != wantLists || objects != 2 {
					t.Fatal("unexpected read count", lists, objects)
				}
				for _, item := range out.Items() {
					want := HelmRecoveryTargetOriginal
					if tc.name == "target-modified" {
						want = HelmRecoveryTargetModified
					}
					if tc.name == "target-replacement" {
						want = HelmRecoveryTargetReplacement
					}
					if tc.name == "target-absent" {
						want = HelmRecoveryTargetAbsent
					}
					if item.State != want || item.ObservedAt.Before(out.StartedAt()) || item.ObservedAt.After(out.FinishedAt()) {
						t.Fatal("current fact", item)
					}
					if tc.name == "target-flags" && !(item.Terminating && item.Finalizers && item.Owners && item.Keep && item.Hook) {
						t.Fatal("lost flags")
					}
					if tc.name == "target-marker-missing" && item.Markers != "missing" {
						t.Fatal("missing marker")
					}
					if tc.name == "target-marker-mismatch" && item.Markers != "mismatch" {
						t.Fatal("mismatched marker")
					}
					if want == HelmRecoveryTargetAbsent && (item.UID != "" || item.ResourceVersion != "" || item.Markers != "") {
						t.Fatal("invented absent identity")
					}
				}
				before := out.Items()
				out.Items()[0].Target.UID = "changed"
				out.Items()[0].State = HelmRecoveryTargetAbsent
				if !reflect.DeepEqual(before, out.Items()) {
					t.Fatal("observation accessor alias")
				}
				data, _ := json.Marshal(out)
				if string(data) != "{}" {
					t.Fatal("private observation serialized")
				}
				data, _ = json.Marshal(out.Items())
				if strings.Contains(string(data), "PRIVATE") {
					t.Fatal("untrusted metadata escaped")
				}
				c.Discard()
				if !reflect.DeepEqual(before, out.Items()) || out.HistoryOutcome() != historyOutcome {
					t.Fatal("discard erased historical facts")
				}
			} else {
				if err == nil || out != nil {
					t.Fatal("unsafe partial observation returned")
				}
				if strings.Contains(err.Error(), "PRIVATE") || strings.Contains(err.Error(), f.config.Host) {
					t.Fatal("private transport or server details escaped")
				}
				if strings.HasPrefix(tc.name, "list-") && !strings.HasSuffix(tc.name, "after") && objects != 0 {
					t.Fatal("target read after invalid first snapshot")
				}
				if strings.HasPrefix(tc.name, "target-") && (lists != 1 || objects != 1) {
					t.Fatal("read continued after unsafe target", lists, objects)
				}
			}
			if again, err := f.b.ObserveHelmUninstallRecovery(context.Background(), c, "new-session", "team", f.row); again != nil || !errors.Is(err, ErrHelmReconciliationConsent) {
				t.Fatal("consumed read consent reused")
			}
			_, _, after := f.inspect(t)
			if !reflect.DeepEqual(requests, after) {
				t.Fatal("reused consent performed network I/O")
			}
		})
	}
}

func TestHelmUninstallReconciliationConsent(t *testing.T) {
	for _, mode := range []string{"no-ack", "nil-recovery", "zero-recovery", "corrupt-recovery", "bad-binding", "unknown-binding", "bad-digest", "wrong-cluster", "invalid-session", "invalid-namespace", "wrong-release", "wrong-number", "wrong-storage", "wrong-uid", "unsupported-status", "nil-backend"} {
		t.Run(mode, func(t *testing.T) {
			f := newHelmReconciliationFixture(t, 1)
			b, r, session, ns, row, ack := f.b, f.recovery, "new-session", "team", f.row, true
			switch mode {
			case "no-ack":
				ack = false
			case "nil-recovery":
				r = nil
			case "zero-recovery":
				r = &HelmUninstallRecovery{}
			case "corrupt-recovery":
				r = reconciliationFixtureHistory(t, b, f.targets, "corrupt")
			case "bad-binding":
				r.binding = "invalid"
			case "unknown-binding":
				r.binding = strings.Repeat("0", 64)
			case "bad-digest":
				r.digest = ""
			case "wrong-cluster":
				cfg := b.Config()
				cfg.Host += "/different"
				var err error
				b, err = New(cfg)
				if err != nil {
					t.Fatal(err)
				}
			case "invalid-session":
				session = ""
			case "invalid-namespace":
				ns = "../team"
			case "wrong-release":
				row.Release = "other"
				row.StorageName = "sh.helm.release.v1.other.v2"
			case "wrong-number":
				row.Number = 3
				row.StorageName = "sh.helm.release.v1.example.v3"
			case "wrong-storage":
				row.StorageName = "other-storage"
			case "wrong-uid":
				row.UID = "replacement-storage"
			case "unsupported-status":
				row.Status = "pending-install"
			case "nil-backend":
				b = nil
			}
			c, err := b.AcknowledgeHelmUninstallReconciliation(r, session, ns, row, ack)
			if c != nil || !errors.Is(err, ErrHelmReconciliationConsent) {
				t.Fatal("unsafe read grant", err)
			}
			lists, objects, _ := f.inspect(t)
			if lists+objects != 0 {
				t.Fatal("acknowledgement performed I/O")
			}
		})
	}
}

func TestHelmUninstallReconciliationFreshBackend(t *testing.T) {
	for _, mode := range []string{"closed", "incomplete"} {
		t.Run(mode, func(t *testing.T) {
			f := newHelmReconciliationFixture(t, 2)
			if mode == "incomplete" {
				f.recovery = reconciliationFixtureHistory(t, f.b, f.targets, mode)
			}
			if !f.recovery.ExpiresAt().Before(time.Now()) {
				t.Fatal("fixture execution authority has not expired")
			}
			history, effects := f.recovery.Outcome(), f.recovery.Effects()
			fresh, err := New(f.config)
			if err != nil {
				t.Fatal(err)
			}
			if c, err := fresh.AcknowledgeHelmUninstallReconciliation(f.recovery, "restarted-session", "team", f.row, false); c != nil || !errors.Is(err, ErrHelmReconciliationConsent) {
				t.Fatal("restart restored authority")
			}
			c, err := fresh.AcknowledgeHelmUninstallReconciliation(f.recovery, "restarted-session", "team", f.row, true)
			if err != nil {
				t.Fatal("fresh explicit read grant", err)
			}
			if c.state.owner == f.b.identity || c.state.session == "new-session" || c.state.expires.Before(time.Now()) {
				t.Fatal("old identity or lifetime restored")
			}
			out, err := fresh.ObserveHelmUninstallRecovery(context.Background(), c, "restarted-session", "team", f.row)
			if err != nil || out == nil || out.HistoryOutcome() != history || !reflect.DeepEqual(f.recovery.Effects(), effects) {
				t.Fatal("current observation rewrote reopened history", err)
			}
			if f.recovery.Complete() != (mode == "closed") {
				t.Fatal("read created a terminal historical frame")
			}
			lists, objects, _ := f.inspect(t)
			if lists != 2 || objects != 2 {
				t.Fatal("restart performed unexpected reads")
			}
		})
	}
}

func TestHelmUninstallReconciliationConsumption(t *testing.T) {
	for _, mode := range []string{"nil-consent", "zero-consent", "discard", "copy-discard", "wrong-backend", "wrong-session", "wrong-namespace", "wrong-rv", "wrong-status", "canceled", "expired", "future-created", "nil-backend"} {
		t.Run(mode, func(t *testing.T) {
			f := newHelmReconciliationFixture(t, 1)
			c := f.consent(t)
			b, session, ns, row := f.b, "new-session", "team", f.row
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "nil-consent":
				c = nil
			case "zero-consent":
				c = &HelmUninstallReconciliationConsent{}
			case "discard":
				c.Discard()
				c.Discard()
			case "copy-discard":
				copyHandle := *c
				copyHandle.Discard()
			case "wrong-backend":
				var err error
				b, err = New(f.config)
				if err != nil {
					t.Fatal(err)
				}
			case "wrong-session":
				session = "other"
			case "wrong-namespace":
				ns = "other"
			case "wrong-rv":
				row.ResourceVersion = "another-rv"
			case "wrong-status":
				row.Status = "uninstalled"
			case "canceled":
				cancel()
			case "expired":
				c.state.expires = time.Now().Add(-time.Second)
			case "future-created":
				c.state.created = time.Now().Add(time.Second)
			case "nil-backend":
				b = nil
			}
			out, err := b.ObserveHelmUninstallRecovery(ctx, c, session, ns, row)
			if out != nil || err == nil {
				t.Fatal("invalid read attempt succeeded")
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			if again, err := f.b.ObserveHelmUninstallRecovery(context.Background(), c, "new-session", "team", f.row); again != nil || !errors.Is(err, ErrHelmReconciliationConsent) {
				t.Fatal("failed attempt did not consume consent")
			}
			lists, objects, _ := f.inspect(t)
			if lists+objects != 0 {
				t.Fatal("invalid attempt performed I/O")
			}
		})
	}
}

func TestHelmUninstallReconciliationCurrentFactsAndHistory(t *testing.T) {
	for _, status := range []string{"deployed", "uninstalling", "uninstalled"} {
		t.Run(status, func(t *testing.T) {
			f := newHelmReconciliationFixture(t, 4)
			f.row.Status = status
			f.mode = "target-mixed"
			c := f.consent(t)
			if c.state.expires.Sub(c.state.created) != helmReconciliationLifetime {
				t.Fatal("new read lifetime changed")
			}
			out, err := f.b.ObserveHelmUninstallRecovery(context.Background(), c, "new-session", "team", f.row)
			if err != nil {
				t.Fatal(err)
			}
			if out.HistoryOutcome() != HelmExecutionUnknown || f.recovery.Outcome() != HelmExecutionUnknown || out.Revision() != f.row {
				t.Fatal("history became current fact")
			}
			want := []HelmUninstallRecoveryTargetState{HelmRecoveryTargetOriginal, HelmRecoveryTargetModified, HelmRecoveryTargetReplacement, HelmRecoveryTargetAbsent}
			for i, item := range out.Items() {
				if item.State != want[i] || item.Target != f.targets[i] {
					t.Fatal("independent current facts", i, item)
				}
			}
			if out.ExpiresAt().Equal(f.recovery.ExpiresAt()) {
				t.Fatal("historical execution expiry reused")
			}
			lists, objects, _ := f.inspect(t)
			if lists != 2 || objects != 4 {
				t.Fatal("observation read set")
			}
		})
	}
}

func TestHelmUninstallReconciliationFrozenInputs(t *testing.T) {
	f := newHelmReconciliationFixture(t, 2)
	id := f.recovery.ID()
	c := f.consent(t)
	copyConsent := *c
	f.recovery.Targets()[0].Name = "changed-after-mint"
	f.recovery.Targets()[0].UID = "changed-after-mint"
	*f.recovery = HelmUninstallRecovery{}
	*c = HelmUninstallReconciliationConsent{}
	out, err := f.b.ObserveHelmUninstallRecovery(context.Background(), &copyConsent, "new-session", "team", f.row)
	if err != nil || out == nil || out.ID() != id || out.Items()[0].Target != f.targets[0] {
		t.Fatal("caller wrapper reassignment changed grant", err)
	}
	copyHandle := copyConsent
	copyHandle.Discard()
	if _, err := f.b.ObserveHelmUninstallRecovery(context.Background(), &copyConsent, "new-session", "team", f.row); !errors.Is(err, ErrHelmReconciliationConsent) {
		t.Fatal("copied handle consumption")
	}
	f.inspect(t)
}

func TestHelmUninstallReconciliationConcurrentConsumption(t *testing.T) {
	f := newHelmReconciliationFixture(t, 2)
	c := f.consent(t)
	var wg sync.WaitGroup
	var successes, denials atomic.Int32
	start := make(chan struct{})
	for i := 0; i < 16; i++ {
		copyHandle := *c
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			out, err := f.b.ObserveHelmUninstallRecovery(context.Background(), &copyHandle, "new-session", "team", f.row)
			if out != nil && err == nil {
				successes.Add(1)
			} else if out == nil && errors.Is(err, ErrHelmReconciliationConsent) {
				denials.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if successes.Load() != 1 || denials.Load() != 15 {
		t.Fatal("concurrent consumption", successes.Load(), denials.Load())
	}
	lists, objects, _ := f.inspect(t)
	if lists != 2 || objects != 2 {
		t.Fatal("concurrent grants performed extra reads")
	}
}

func TestHelmUninstallReconciliationRevocation(t *testing.T) {
	for _, at := range []string{"first-list", "first-target", "final-list"} {
		t.Run(at, func(t *testing.T) {
			f := newHelmReconciliationFixture(t, 2)
			c := f.consent(t)
			read := 0
			f.onRead = func(path string) {
				read++
				if at == "first-list" && read == 1 || at == "first-target" && strings.Contains(path, "/configmaps/") || at == "final-list" && read == 4 {
					c.Discard()
				}
			}
			out, err := f.b.ObserveHelmUninstallRecovery(context.Background(), c, "new-session", "team", f.row)
			if out != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("revoked observation escaped", err)
			}
			lists, objects, _ := f.inspect(t)
			switch at {
			case "first-list":
				if lists != 1 || objects != 0 {
					t.Fatal("read continued after revoke")
				}
			case "first-target":
				if lists != 1 || objects != 1 {
					t.Fatal("target batch continued after revoke")
				}
			case "final-list":
				if lists != 2 || objects != 2 {
					t.Fatal("unexpected final read set")
				}
			}
		})
	}
}

func TestHelmUninstallReconciliationDeadlineAndBounds(t *testing.T) {
	for _, mode := range []string{"context-deadline", "consent-deadline", "maximum-targets", "empty-response-budget"} {
		t.Run(mode, func(t *testing.T) {
			count := 1
			if mode == "maximum-targets" {
				count = helmrelease.MaxPreviewObjects
			}
			f := newHelmReconciliationFixture(t, count)
			c := f.consent(t)
			ctx := context.Background()
			var cancel context.CancelFunc
			if mode == "context-deadline" {
				ctx, cancel = context.WithTimeout(ctx, 30*time.Millisecond)
				defer cancel()
			}
			if mode == "consent-deadline" {
				c.state.expires = time.Now().Add(30 * time.Millisecond)
			}
			if mode == "context-deadline" || mode == "consent-deadline" {
				unblock := make(chan struct{})
				defer close(unblock)
				f.onRead = func(string) { <-unblock }
				out, err := f.b.ObserveHelmUninstallRecovery(ctx, c, "new-session", "team", f.row)
				if out != nil || !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("deadline lost", err)
				}
				return
			}
			if mode == "empty-response-budget" {
				read := helmReconciliationRead{b: f.b, ctx: ctx, active: func() error { return nil }, dispatch: func() error { return nil }, namespace: "team", row: f.row}
				if _, _, err := read.get("configmaps", f.targets[0].Name, "", maxLiveObjectBytes); !errors.Is(err, ErrHelmReconciliationBudget) {
					t.Fatal("unbounded read")
				}
				lists, objects, _ := f.inspect(t)
				if lists+objects != 0 {
					t.Fatal("empty budget performed I/O")
				}
				return
			}
			out, err := f.b.ObserveHelmUninstallRecovery(ctx, c, "new-session", "team", f.row)
			if err != nil || len(out.Items()) != count {
				t.Fatal("bounded full target set", err)
			}
			lists, objects, _ := f.inspect(t)
			if lists != 2 || objects != count {
				t.Fatal("bounded batch read count")
			}
		})
	}
}

func TestHelmUninstallReconciliationEmptyHandles(t *testing.T) {
	var c *HelmUninstallReconciliationConsent
	c.Discard()
	(&HelmUninstallReconciliationConsent{}).Discard()
	var o *HelmUninstallRecoveryObservation
	if o.ID() != "" || o.Namespace() != "" || o.Revision() != (helmrelease.Revision{}) || o.HistoryOutcome() != HelmExecutionNotStarted || o.Items() != nil ||
		!o.StartedAt().IsZero() || !o.FinishedAt().IsZero() || !o.ExpiresAt().IsZero() {
		t.Fatal("empty historical handle")
	}
}

func TestHelmUninstallReconciliationReadBoundary(t *testing.T) {
	for _, mode := range []string{"secret-content", "other-resource", "empty-object-name", "object-query", "namespace-traversal", "target-traversal", "foreign-target-namespace", "foreign-target-resource", "invalid-target-uid", "rate-denied", "active-canceled", "dispatch-canceled"} {
		t.Run(mode, func(t *testing.T) {
			f := newHelmReconciliationFixture(t, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			read := helmReconciliationRead{b: f.b, ctx: ctx, active: ctx.Err, dispatch: ctx.Err,
				namespace: "team", row: f.row, remaining: maxLiveReadBytes}
			resource, name, token := "configmaps", f.targets[0].Name, ""
			target, targetRead := f.targets[0], false
			want := ErrHelmReconciliationResponse
			switch mode {
			case "secret-content":
				resource, name = "secrets", f.row.StorageName
			case "other-resource":
				resource = "pods"
			case "empty-object-name":
				name = ""
			case "object-query":
				token = "PRIVATE_CURSOR"
			case "namespace-traversal":
				read.namespace = "../team"
			case "target-traversal":
				name = "../secrets"
			case "foreign-target-namespace":
				target.Namespace, targetRead = "other", true
			case "foreign-target-resource":
				target.Resource, targetRead = "secrets", true
			case "invalid-target-uid":
				target.UID, targetRead = "", true
			case "rate-denied":
				f.b.config.RateLimiter = flowcontrol.NewFakeNeverRateLimiter()
			case "active-canceled":
				cancel()
				want = context.Canceled
			case "dispatch-canceled":
				read.dispatch = func() error { cancel(); return ctx.Err() }
				want = context.Canceled
			}
			var err error
			if targetRead {
				var item HelmUninstallRecoveryItem
				item, err = read.target(target)
				if item != (HelmUninstallRecoveryItem{}) {
					t.Fatal("invalid target manufactured a fact")
				}
			} else {
				var data []byte
				var status int
				data, status, err = read.get(resource, name, token, maxLiveObjectBytes)
				if data != nil || status != 0 {
					t.Fatal("unqualified route produced response data")
				}
			}
			if !errors.Is(err, want) {
				t.Fatal("read boundary accepted invalid request", err)
			}
			lists, objects, requests := f.inspect(t)
			if lists+objects != 0 || len(requests) != 0 {
				t.Fatal("unqualified request crossed the HTTP dispatch boundary")
			}
		})
	}
}
