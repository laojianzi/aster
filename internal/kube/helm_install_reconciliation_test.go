package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/flowcontrol"
)

func TestHelmInstallReconciliationHTTP(t *testing.T) {
	good := []string{"success", "target-modified", "target-replacement", "target-absent", "target-flags", "target-marker-missing", "target-marker-mismatch",
		"storage-absent", "storage-unlabeled", "storage-replaced", "list-empty-null", "list-pagination", "list-page-token",
		"storage-unknown-matching-markers", "storage-unknown-unlabeled", "target-unknown-matching-markers", "deploy-intent-rv", "deploy-pending-rv"}
	bad := []string{"namespace-forbidden", "namespace-not-acceptable", "namespace-full-object", "namespace-wrong-name", "namespace-wrong-namespace", "namespace-control-uid", "namespace-control-rv", "namespace-proxy404", "namespace-unknown-field", "namespace-null-metadata",
		"storage-forbidden", "storage-unauthorized", "storage-not-acceptable", "storage-full-object", "storage-secret-data", "storage-invalid-json", "storage-duplicate-field", "storage-unknown-field", "storage-null", "storage-null-metadata",
		"storage-wrong-name", "storage-wrong-namespace", "storage-empty-uid", "storage-control-uid", "storage-empty-rv", "storage-control-rv", "storage-missing-content-type", "storage-duplicate-content-type", "storage-wrong-content-type",
		"storage-warning", "storage-trailer-warning", "storage-encoding", "storage-duplicate-encoding", "storage-truncated", "storage-oversized-length", "storage-oversized-stream", "storage-retry", "storage-redirect", "storage-proxy404",
		"target-forbidden", "target-secret-data", "target-duplicate-field", "target-control-uid", "target-trailer-warning",
		"list-forbidden", "list-not-acceptable", "list-secret-data", "list-duplicate", "list-page-rv", "list-page-repeat", "list-page-duplicate", "list-control-uid", "list-control-rv"}
	for _, route := range []string{"namespace", "storage", "target"} {
		for _, suffix := range []string{"wrong-name", "wrong-kind", "wrong-group", "uid", "cause", "retry", "listmeta", "null-details"} {
			bad = append(bad, route+"-absent-"+suffix)
		}
	}
	for _, name := range append(append([]string(nil), good...), bad...) {
		t.Run(name, func(t *testing.T) {
			valid := false
			for _, n := range good {
				valid = valid || n == name
			}
			history := "complete"
			switch name {
			case "storage-unknown-matching-markers", "storage-unknown-unlabeled":
				history = "secret-unknown"
			case "target-unknown-matching-markers":
				history = "target-unknown"
			case "deploy-intent-rv":
				history = "deploy-unknown"
			case "deploy-pending-rv":
				history = "deploy-pending"
			}
			f := newInstallReconciliationFixture(t, 2, history)
			f.mode = name
			for key, item := range f.targets {
				switch name {
				case "target-modified":
					item.ResourceVersion = "changed-rv"
				case "target-replacement":
					item.UID += "-replacement"
				case "target-absent":
					f.targets[key] = nil
				case "target-flags":
					now := metav1.Now()
					item.DeletionTimestamp = &now
					item.Finalizers = []string{"PRIVATE_FINALIZER"}
					item.OwnerReferences = []metav1.OwnerReference{{Name: "PRIVATE_OWNER"}}
					item.Annotations["helm.sh/resource-policy"] = "PRIVATE_KEEP"
					item.Annotations["helm.sh/hook"] = "PRIVATE_HOOK"
				case "target-marker-missing":
					delete(item.Labels, "app.kubernetes.io/managed-by")
				case "target-marker-mismatch":
					item.Annotations["meta.helm.sh/release-name"] = "PRIVATE_OTHER"
				}
			}
			switch name {
			case "storage-absent", "list-empty-null":
				f.storage = nil
			case "storage-unlabeled", "storage-unknown-unlabeled":
				delete(f.storage.Labels, "owner")
			case "storage-replaced":
				f.storage.UID = "replacement-storage"
			}
			c := f.consent(t)
			outcome, complete, finished, effects := f.recovery.Outcome(), f.recovery.Complete(), f.recovery.FinishedAt(), f.recovery.Effects()
			out, err := f.b.ObserveHelmInstallRecovery(context.Background(), c, f.recovery, "new-session", "team")
			counts, requests := f.inspect(t)
			if valid {
				if err != nil || out == nil || len(out.Items()) != 2 || out.ID() != f.recovery.ID() || out.Namespace() != "team" || out.Release() != "example" || out.HistoryOutcome() != outcome {
					t.Fatal("metadata observation", err)
				}
				if out.StartedAt().IsZero() || out.FinishedAt().Before(out.StartedAt()) || !out.FinishedAt().Before(out.ExpiresAt()) || !out.ExpiresAt().Equal(c.state.expires) {
					t.Fatal("observation chronology")
				}
				wantLists := 2
				if name == "list-pagination" || name == "list-page-token" {
					wantLists = 4
				}
				if counts["namespace"] != 2 || counts["list"] != wantLists || counts["storage"] != 2 || counts["target"] != 2 {
					t.Fatal("unbounded or incomplete read set", counts)
				}
				for _, item := range out.Items() {
					want := HelmInstallRecoveryOriginal
					switch name {
					case "target-modified":
						want = HelmInstallRecoveryModified
					case "target-replacement":
						want = HelmInstallRecoveryReplacement
					case "target-absent":
						want = HelmInstallRecoveryAbsent
					case "storage-unknown-matching-markers", "storage-unknown-unlabeled", "target-unknown-matching-markers":
						want = HelmInstallRecoveryUnattributed
					}
					if item.State != want || item.ObservedAt.Before(out.StartedAt()) || item.ObservedAt.After(out.FinishedAt()) {
						t.Fatal("target identity inference", item)
					}
					if want == HelmInstallRecoveryUnattributed && item.HistoricalUID != "" || want == HelmInstallRecoveryAbsent && (item.UID != "" || item.ResourceVersion != "" || item.Markers != "") {
						t.Fatal("fabricated historical or absent identity")
					}
					if name == "target-flags" && !(item.Terminating && item.Finalizers && item.Owners && item.Keep && item.Hook) ||
						name == "target-marker-missing" && item.Markers != "missing" || name == "target-marker-mismatch" && item.Markers != "mismatch" {
						t.Fatal("lost safe metadata flags")
					}
				}
				storage := out.Storage()
				switch name {
				case "storage-unknown-matching-markers", "storage-unknown-unlabeled":
					if storage.State != HelmInstallRecoveryUnattributed || storage.HistoricalUID != "" || storage.UID == "" {
						t.Fatal("unknown create attributed from present name")
					}
				case "deploy-intent-rv":
					if storage.State != HelmInstallRecoveryModified || storage.HistoricalResourceVersion != "storage-before" || storage.ResourceVersion != "storage-after" || out.HistoryOutcome() != helmjournal.Unknown {
						t.Fatal("PATCH precondition was treated as response or completion")
					}
				case "deploy-pending-rv":
					if storage.State != HelmInstallRecoveryOriginal || storage.HistoricalResourceVersion != "storage-after" || effects[len(effects)-1].Outcome != helmjournal.AcceptedPending {
						t.Fatal("recorded PATCH response version was lost")
					}
				case "storage-replaced":
					if storage.State != HelmInstallRecoveryReplacement {
						t.Fatal("storage replacement not distinguished")
					}
				case "storage-absent", "list-empty-null":
					if storage.State != HelmInstallRecoveryAbsent || storage.UID != "" || len(out.Revisions()) != 0 {
						t.Fatal("absent storage invented facts")
					}
				}
				items, rows := out.Items(), out.Revisions()
				out.Items()[0].UID = "forged"
				if len(rows) != 0 {
					out.Revisions()[0].UID = "forged"
				}
				if !reflect.DeepEqual(items, out.Items()) || !reflect.DeepEqual(rows, out.Revisions()) {
					t.Fatal("current fact accessor alias")
				}
				raw, _ := json.Marshal(struct {
					Storage HelmInstallRecoveryItem
					Items   []HelmInstallRecoveryItem
					Rows    []helmrelease.Revision
				}{out.Storage(), out.Items(), out.Revisions()})
				if strings.Contains(string(raw), "PRIVATE") {
					t.Fatal("sensitive metadata projection escaped")
				}
				c.Discard()
				if !reflect.DeepEqual(items, out.Items()) || out.HistoryOutcome() != outcome {
					t.Fatal("discard erased immutable historical facts")
				}
			} else {
				if err == nil || out != nil || strings.Contains(err.Error(), "PRIVATE") || strings.Contains(err.Error(), f.config.Host) {
					t.Fatal("unsafe response published facts or details", err)
				}
				if strings.Contains(name, "-absent-") && err != ErrHelmReconciliationResponse {
					t.Fatal("malformed NotFound was interpreted as absence", err)
				}
			}
			if f.recovery.Outcome() != outcome || f.recovery.Complete() != complete || !f.recovery.FinishedAt().Equal(finished) || !reflect.DeepEqual(effects, f.recovery.Effects()) {
				t.Fatal("current GETs changed history")
			}
			if again, err := f.b.ObserveHelmInstallRecovery(context.Background(), c, f.recovery, "new-session", "team"); again != nil || err != ErrHelmReconciliationConsent {
				t.Fatal("read confirmation reused", err)
			}
			_, after := f.inspect(t)
			if !reflect.DeepEqual(requests, after) {
				t.Fatal("reused confirmation performed I/O")
			}
		})
	}
}

func TestHelmInstallReconciliationNamespaceHistory(t *testing.T) {
	for _, mode := range []string{"namespace-absent", "namespace-replaced", "namespace-terminating", "namespace-replaced-after", "namespace-terminating-after", "namespace-rv-progress",
		"history-added-after", "history-status-after", "history-rv-after", "history-unrelated-progress", "history-existing-newer", "history-missing-storage", "history-storage-mismatch",
		"storage-unlabeled-after", "storage-replaced-after", "storage-absent-after", "storage-rv-after"} {
		t.Run(mode, func(t *testing.T) {
			f := newInstallReconciliationFixture(t, 1, "secret-unknown")
			newer := *f.storage.DeepCopy()
			newer.Name, newer.UID, newer.ResourceVersion = "sh.helm.release.v1.example.v2", "newer-storage", "newer-rv"
			newer.Labels["version"], newer.Labels["status"] = "2", "failed"
			switch mode {
			case "namespace-absent":
				f.mode = "namespace-absent"
			case "namespace-replaced":
				f.namespace.UID = "replacement-namespace"
			case "namespace-terminating":
				now := metav1.Now()
				f.namespace.DeletionTimestamp = &now
			case "history-existing-newer":
				f.extra = append(f.extra, newer)
			case "history-missing-storage":
				f.mode = "list-storage-omitted"
			case "history-storage-mismatch":
				f.mode = "list-storage-mismatch"
			}
			f.onRead = func(route string, n int) {
				if n != 2 {
					return
				}
				switch route + "/" + mode {
				case "namespace/namespace-replaced-after":
					f.namespace.UID = "replacement-namespace"
				case "namespace/namespace-terminating-after":
					now := metav1.Now()
					f.namespace.DeletionTimestamp = &now
				case "namespace/namespace-rv-progress":
					f.namespace.ResourceVersion = "new-valid-namespace-rv"
				case "list/history-added-after":
					f.extra = append(f.extra, newer)
				case "list/history-status-after":
					f.storage.Labels["status"] = "failed"
				case "list/history-rv-after":
					f.storage.ResourceVersion = "different-list-rv"
				case "list/history-unrelated-progress":
					f.extra = append(f.extra, reconciliationOtherMeta(9))
				case "storage/storage-unlabeled-after":
					delete(f.storage.Labels, "owner")
				case "storage/storage-replaced-after":
					f.storage.UID = "replacement-storage"
				case "storage/storage-absent-after":
					f.storage = nil
				case "storage/storage-rv-after":
					f.storage.ResourceVersion = "different-storage-rv"
				}
			}
			out, err := f.b.ObserveHelmInstallRecovery(context.Background(), f.consent(t), f.recovery, "new-session", "team")
			valid := mode == "namespace-rv-progress" || mode == "history-unrelated-progress" || mode == "history-existing-newer"
			if valid {
				if err != nil || out == nil || out.HistoryOutcome() != helmjournal.Unknown {
					t.Fatal("read-only historical distinction", err)
				}
				if mode == "namespace-rv-progress" {
					uid, rv := out.NamespaceIdentity()
					if uid != f.plan.NamespaceUID || rv != "new-valid-namespace-rv" {
						t.Fatal("namespace RV progress rejected or hidden")
					}
				}
				if mode == "history-existing-newer" && len(out.Revisions()) != 2 || mode == "history-unrelated-progress" && len(out.Revisions()) != 1 {
					t.Fatal("incomplete or unscoped release metadata")
				}
			} else if out != nil || err != ErrHelmReconciliationChanged {
				t.Fatal("inconsistent bracket published", err)
			}
			counts, _ := f.inspect(t)
			if (mode == "namespace-absent" || mode == "namespace-replaced" || mode == "namespace-terminating") && len(counts) != 1 {
				t.Fatal("read targets in unverified namespace", counts)
			}
		})
	}
}

func TestHelmInstallReconciliationConsent(t *testing.T) {
	for _, mode := range []string{"no-ack", "nil-recovery", "zero-recovery", "corrupt-recovery", "invalid-session", "invalid-namespace", "different-namespace", "nil-backend", "zero-backend", "different-binding", "unknown-binding", "invalid-digest"} {
		t.Run(mode, func(t *testing.T) {
			f := newInstallReconciliationFixture(t, 1, "complete")
			b, r, session, ns, ack := f.b, f.recovery, "new-session", "team", true
			switch mode {
			case "no-ack":
				ack = false
			case "nil-recovery":
				r = nil
			case "zero-recovery":
				r = &HelmInstallRecovery{}
			case "corrupt-recovery":
				_, r, _ = installRecoveryHistory(t, b, 1, "corrupt")
			case "invalid-session":
				session = ""
			case "invalid-namespace":
				ns = "../team"
			case "different-namespace":
				ns = "elsewhere"
			case "nil-backend":
				b = nil
			case "zero-backend":
				b = &Backend{}
			case "different-binding":
				copy := *b
				copy.helmJournalBinding = strings.Repeat("0", 64)
				b = &copy
			case "unknown-binding":
				copy := *b
				copy.helmJournalBinding = ""
				b = &copy
			case "invalid-digest":
				snapshot := cloneHelmInstallSnapshot(r.state.snapshot)
				snapshot.Digest = "invalid"
				r = helmInstallRecoveryFromSnapshot(snapshot, false)
			}
			if c, err := b.AcknowledgeHelmInstallReconciliation(r, session, ns, ack); c != nil || err != ErrHelmReconciliationConsent {
				t.Fatal("invalid historical scope acknowledged", err)
			}
			_, requests := f.inspect(t)
			if len(requests) != 0 {
				t.Fatal("acknowledgement performed network I/O")
			}
		})
	}
}

func TestHelmInstallReconciliationConsumption(t *testing.T) {
	for _, mode := range []string{"nil-consent", "zero-consent", "nil-context", "cancelled-context", "different-backend", "different-session", "different-namespace", "nil-recovery", "zero-recovery", "expired", "future-created", "discarded", "changed-connection-binding"} {
		t.Run(mode, func(t *testing.T) {
			f := newInstallReconciliationFixture(t, 1, "secret-unknown")
			c := f.consent(t)
			b, r, session, ns, ctx := f.b, f.recovery, "new-session", "team", context.Background()
			want := error(ErrHelmReconciliationConsent)
			switch mode {
			case "nil-consent":
				c = nil
			case "zero-consent":
				c = &HelmInstallReconciliationConsent{}
			case "nil-context":
				ctx = nil
			case "cancelled-context":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			case "different-backend":
				var err error
				b, err = New(f.config)
				if err != nil {
					t.Fatal(err)
				}
			case "different-session":
				session = "other-session"
			case "different-namespace":
				ns = "elsewhere"
			case "nil-recovery":
				r = nil
			case "zero-recovery":
				r = &HelmInstallRecovery{}
			case "expired":
				c.state.expires = time.Now().Add(-time.Second)
			case "future-created":
				c.state.created = time.Now().Add(time.Second)
			case "discarded":
				c.Discard()
			case "changed-connection-binding":
				b.helmJournalBinding = strings.Repeat("0", 64)
				want = ErrHelmReconciliationChanged
			}
			if out, err := b.ObserveHelmInstallRecovery(ctx, c, r, session, ns); out != nil || !errors.Is(err, want) {
				t.Fatal("invalid call gained metadata facts", err)
			}
			if out, err := f.b.ObserveHelmInstallRecovery(context.Background(), c, f.recovery, "new-session", "team"); out != nil || err != ErrHelmReconciliationConsent {
				t.Fatal("failed attempt did not consume grant", err)
			}
			_, requests := f.inspect(t)
			if len(requests) != 0 {
				t.Fatal("invalid call crossed dispatch")
			}
		})
	}
}

func TestHelmInstallReconciliationSnapshotBinding(t *testing.T) {
	for _, mode := range []string{"same-id-reload", "wrapper-retarget", "recovery-copy", "grant-copy", "defensive-accessors", "fresh-backend-after-expiry", "archived"} {
		t.Run(mode, func(t *testing.T) {
			history := "secret-unknown"
			if mode == "archived" {
				history = "archived"
			}
			f := newInstallReconciliationFixture(t, 1, history)
			if mode == "fresh-backend-after-expiry" {
				fresh, err := New(f.config)
				if err != nil {
					t.Fatal(err)
				}
				f.b = fresh
			}
			c := f.consent(t)
			r := f.recovery
			switch mode {
			case "same-id-reload", "wrapper-retarget":
				j, err := OpenHelmUninstallJournal(f.dir)
				if err != nil {
					t.Fatal(err)
				}
				reloaded, err := j.RecoverInstall(r.ID(), false)
				if err != nil {
					t.Fatal(err)
				}
				if err := j.Close(); err != nil {
					t.Fatal(err)
				}
				if reloaded.ID() != r.ID() || reloaded.state == r.state {
					t.Fatal("reloaded identity fixture")
				}
				if mode == "wrapper-retarget" {
					*r = *reloaded
				} else {
					r = reloaded
				}
			case "recovery-copy":
				copy := *r
				*r = HelmInstallRecovery{}
				r = &copy
			case "grant-copy":
				copy := *c
				*c = HelmInstallReconciliationConsent{}
				c = &copy
			case "defensive-accessors":
				r.Targets()[0].Name = "other"
				r.Effects()[0].UID = "forged"
			}
			out, err := f.b.ObserveHelmInstallRecovery(context.Background(), c, r, "new-session", "team")
			if mode == "same-id-reload" || mode == "wrapper-retarget" {
				if out != nil || err != ErrHelmReconciliationConsent {
					t.Fatal("new snapshot reused old read consent", err)
				}
				_, requests := f.inspect(t)
				if len(requests) != 0 {
					t.Fatal("retargeted wrapper performed I/O")
				}
				return
			}
			if err != nil || out == nil || out.ID() != r.ID() || mode != "archived" && (out.HistoryOutcome() != helmjournal.Unknown || out.Storage().State != HelmInstallRecoveryUnattributed) {
				t.Fatal("frozen read snapshot lost", err)
			}
			if !c.state.expires.Equal(c.state.created.Add(helmReconciliationLifetime)) || !c.state.created.After(r.ExpiresAt()) {
				t.Fatal("old execution expiry reused for new read grant")
			}
			raw, _ := json.Marshal(c)
			if strings.Contains(string(raw), "PRIVATE") || strings.Contains(fmt.Sprintf("%#v", c), "PRIVATE") || strings.Contains(fmt.Sprintf("%+v", c), "new-session") {
				t.Fatal("opaque grant exposed internals")
			}
		})
	}
}

func TestHelmInstallReconciliationConcurrentConsumption(t *testing.T) {
	f := newInstallReconciliationFixture(t, 2, "complete")
	c := f.consent(t)
	var good, denied atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 8 {
		wg.Go(func() {
			<-start
			copy := *c
			out, err := f.b.ObserveHelmInstallRecovery(context.Background(), &copy, f.recovery, "new-session", "team")
			if err == nil && out != nil {
				good.Add(1)
			} else if out == nil && err == ErrHelmReconciliationConsent {
				denied.Add(1)
			} else {
				t.Error("unexpected concurrent observation", err)
			}
		})
	}
	close(start)
	wg.Wait()
	if good.Load() != 1 || denied.Load() != 7 {
		t.Fatal("read grant did not linearize once")
	}
	_, requests := f.inspect(t)
	if len(requests) != 8 {
		t.Fatal("concurrent copies duplicated requests", len(requests))
	}
}

func TestHelmInstallReconciliationRevocation(t *testing.T) {
	for _, mode := range []string{"initial-namespace", "initial-storage", "first-target", "final-storage", "final-namespace"} {
		t.Run(mode, func(t *testing.T) {
			f := newInstallReconciliationFixture(t, 2, "secret-unknown")
			c := f.consent(t)
			f.onRead = func(route string, n int) {
				if mode == "initial-namespace" && route == "namespace" && n == 1 || mode == "initial-storage" && route == "storage" && n == 1 ||
					mode == "first-target" && route == "target" && n == 1 || mode == "final-storage" && route == "storage" && n == 2 || mode == "final-namespace" && route == "namespace" && n == 2 {
					c.Discard()
				}
			}
			out, err := f.b.ObserveHelmInstallRecovery(context.Background(), c, f.recovery, "new-session", "team")
			if out != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("revoked read produced completed report", err)
			}
			_, requests := f.inspect(t)
			want := map[string]int{"initial-namespace": 1, "initial-storage": 3, "first-target": 4, "final-storage": 6, "final-namespace": 8}[mode]
			if len(requests) != want {
				t.Fatal("requests continued after revocation", len(requests), want)
			}
		})
	}
}

func TestHelmInstallReconciliationBounds(t *testing.T) {
	for _, mode := range []string{"maximum-targets-default-rate", "context-deadline", "consent-deadline", "list-page-limit", "list-count-limit", "list-page-items", "list-page-bytes", "list-total-bytes", "empty-response-budget"} {
		t.Run(mode, func(t *testing.T) {
			count := 1
			if mode == "maximum-targets-default-rate" {
				count = 32
			}
			f := newInstallReconciliationFixture(t, count, "complete")
			if mode == "maximum-targets-default-rate" {
				config := *f.config
				config.RateLimiter = nil
				b, err := New(&config)
				if err != nil {
					t.Fatal(err)
				}
				f.b = b
			}
			f.mode = mode
			c := f.consent(t)
			ctx := context.Background()
			var release chan struct{}
			if mode == "context-deadline" || mode == "consent-deadline" {
				release = make(chan struct{})
				f.onRead = func(string, int) { <-release }
				if mode == "consent-deadline" {
					c.state.expires = time.Now().Add(60 * time.Millisecond)
				} else {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, 60*time.Millisecond)
					defer cancel()
				}
			}
			if mode == "empty-response-budget" {
				h, _ := freezeHelmInstallReconciliation(f.recovery)
				read := helmInstallReconciliationRead{history: h, read: helmReconciliationRead{b: f.b, ctx: ctx, active: ctx.Err, dispatch: ctx.Err, namespace: "team"}}
				if _, _, err := read.exact("secrets", f.plan.StorageName); err != ErrHelmReconciliationBudget {
					t.Fatal("empty read budget ignored", err)
				}
				_, requests := f.inspect(t)
				if len(requests) != 0 {
					t.Fatal("empty budget performed I/O")
				}
				return
			}
			out, err := f.b.ObserveHelmInstallRecovery(ctx, c, f.recovery, "new-session", "team")
			if release != nil {
				close(release)
			}
			if mode == "maximum-targets-default-rate" {
				if err != nil || out == nil || len(out.Items()) != 32 || out.FinishedAt().Sub(out.StartedAt()) >= helmrelease.ReadTimeout {
					t.Fatal("default bounded 32-target observation failed", err)
				}
				_, requests := f.inspect(t)
				if len(requests) != 38 {
					t.Fatal("unexpected maximum-profile HTTP count", len(requests))
				}
				return
			}
			if out != nil || err == nil {
				t.Fatal("partial over-budget result published", err)
			}
			if release != nil && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("bounded deadline lost", err)
			}
			if release == nil && err != ErrHelmReconciliationBudget && err != ErrHelmReconciliationResponse {
				t.Fatal("unexpected bounded-read error", err)
			}
			f.inspect(t)
		})
	}
}

func TestHelmInstallReconciliationReadBoundary(t *testing.T) {
	for _, mode := range []string{"unplanned-namespace", "unplanned-secret", "unplanned-target", "namespace-traversal", "name-traversal", "unplanned-resource", "rate-denied", "active-cancelled", "dispatch-cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f := newInstallReconciliationFixture(t, 1, "complete")
			h, _ := freezeHelmInstallReconciliation(f.recovery)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			read := helmInstallReconciliationRead{history: h, read: helmReconciliationRead{b: f.b, ctx: ctx, active: ctx.Err, dispatch: ctx.Err, namespace: "team", remaining: maxLiveReadBytes}}
			resource, name := "secrets", f.plan.StorageName
			want := error(ErrHelmReconciliationResponse)
			switch mode {
			case "unplanned-namespace":
				resource, name = "namespaces", "elsewhere"
			case "unplanned-secret":
				name = "unreviewed-secret"
			case "unplanned-target":
				resource, name = "configmaps", "unreviewed-target"
			case "namespace-traversal":
				read.read.namespace = "../team"
			case "name-traversal":
				name = "../secrets"
			case "unplanned-resource":
				resource = "pods"
			case "rate-denied":
				f.b.config.RateLimiter = flowcontrol.NewFakeNeverRateLimiter()
			case "active-cancelled":
				cancel()
				want = context.Canceled
			case "dispatch-cancelled":
				read.read.dispatch = func() error { cancel(); return ctx.Err() }
				want = context.Canceled
			}
			if data, status, err := read.exact(resource, name); data != nil || status != 0 || !errors.Is(err, want) {
				t.Fatal("invalid read crossed bounded route", err)
			}
			_, requests := f.inspect(t)
			if len(requests) != 0 {
				t.Fatal("unplanned or cancelled request dispatched")
			}
		})
	}
}

func TestHelmInstallReconciliationGuardPersistence(t *testing.T) {
	for _, mode := range []string{"secret-unknown", "secret-pending", "target-unknown", "partial", "deploy-unknown", "deploy-pending", "incomplete"} {
		t.Run(mode, func(t *testing.T) {
			f := newInstallReconciliationFixture(t, 1, mode)
			path := filepath.Join(f.dir, f.recovery.ID()+".wal")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			j, err := OpenHelmUninstallJournal(f.dir)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			scope := helmjournal.AdmissionScope{Binding: f.plan.Binding, Namespace: f.plan.Namespace, Release: f.plan.Release}
			prior, err := j.store.ReviewAdmission(context.Background(), scope)
			if !errors.Is(err, helmjournal.ErrUnresolved) || prior.Reason != helmjournal.AdmissionUnresolved {
				t.Fatal("fixture missing admission blocker", err)
			}
			c := f.consent(t)
			out, err := f.b.ObserveHelmInstallRecovery(context.Background(), c, f.recovery, "new-session", "team")
			if err != nil || out == nil || out.HistoryOutcome() != f.recovery.Outcome() {
				t.Fatal("independent read of unresolved history", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("observation changed WAL bytes", err)
			}
			current, err := j.store.ReviewAdmission(context.Background(), scope)
			if !errors.Is(err, helmjournal.ErrUnresolved) || current != prior {
				t.Fatal("observation changed admission explanation", err)
			}
			if w, err := j.store.BeginInstall(f.plan); w != nil || err != helmjournal.ErrUnresolved {
				t.Fatal("GET completion granted new execution", err)
			}
			if review, err := j.store.PrepareArchive(context.Background(), f.recovery.ID()); review != nil || err != helmjournal.ErrUnresolved {
				t.Fatal("GET completion granted archive eligibility", err)
			}
			ids, err := j.IDs()
			if err != nil || len(ids) != 1 || ids[0] != f.recovery.ID() {
				t.Fatal("observation/rejected replay created a new journal", err)
			}
		})
	}
}
