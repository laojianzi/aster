package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmfixture"
	"github.com/laojianzi/aster/internal/helmrelease"
	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

type dryRunFixture struct {
	mu                         sync.Mutex
	mode                       string
	wire                       []byte
	requests, patches, deletes int
	bad                        bool
	onPatch                    func()
	onDelete                   func()
	b                          *Backend
	p                          *HelmUninstallPreparation
}

func newDryRunFixture(t *testing.T, multiple ...bool) *dryRunFixture {
	t.Helper()
	f := &dryRunFixture{wire: preparationWire(t, false)}
	if len(multiple) != 0 && multiple[0] {
		var storage corev1.Secret
		if err := json.Unmarshal(f.wire, &storage); err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(map[string]any{
			"name": "example", "namespace": "team", "version": 2, "info": map[string]any{"status": "deployed"},
			"chart":    map[string]any{"metadata": map[string]any{"name": "example-chart", "version": "1.2.3"}},
			"manifest": liveManifest + "---\n" + strings.Replace(liveManifest, "name: sample", "name: second", 1),
		})
		if err != nil {
			t.Fatal(err)
		}
		storage.Data["release"] = helmfixture.Stored(payload)
		f.wire, err = json.Marshal(storage)
		if err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	var err error
	f.b, err = New(&rest.Config{Host: srv.URL + "/prefix", BearerToken: "current", RateLimiter: flowcontrol.NewFakeAlwaysRateLimiter()})
	if err != nil {
		t.Fatal(err)
	}
	f.p, err = f.b.PrepareHelmUninstall(context.Background(), "team", selectedHelmRevision(), "session", true)
	if err != nil {
		t.Fatal("initial preparation", err)
	}
	f.requests = 0
	return f
}
func (f *dryRunFixture) consent(t *testing.T) *HelmUninstallDryRunConsent {
	t.Helper()
	c, err := f.b.AcknowledgeHelmUninstallDryRun(f.p, "session", "team", selectedHelmRevision(), true)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func (f *dryRunFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	if r.Header.Get("Authorization") != "Bearer current" {
		f.bad = true
	}
	w.Header().Set("Content-Type", "application/json")
	p := strings.TrimPrefix(r.URL.Path, "/prefix")
	if r.Method == http.MethodGet {
		switch p {
		case "/api/v1/namespaces/team/secrets":
			row := liveRevisionMeta()
			if f.mode == "source-changed" || f.mode == "source-after" && f.deletes > 0 {
				row.ResourceVersion = "changed"
			}
			_, _ = w.Write(releasePage("", "collection", row))
		case "/api/v1/namespaces/team/secrets/sh.helm.release.v1.example.v2":
			if f.mode == "content-denied" {
				w.WriteHeader(403)
				_, _ = w.Write([]byte("PRIVATE_ERROR"))
				return
			}
			_, _ = w.Write(f.wire)
		case "/api/v1":
			_ = json.NewEncoder(w).Encode(liveDiscovery())
		case "/api/v1/namespaces/team/configmaps/sample", "/api/v1/namespaces/team/configmaps/second":
			m := liveObject()
			if strings.HasSuffix(p, "/second") {
				m.Name = "second"
				m.UID = "second-uid"
				m.ResourceVersion = "rv-b"
			}
			if f.mode == "uid-changed" {
				m.UID = "replaced"
			}
			if f.mode == "rv-changed" || f.mode == "target-after" && f.deletes > 0 {
				m.ResourceVersion = "changed"
			}
			if f.mode == "missing" {
				w.WriteHeader(404)
				_, _ = w.Write([]byte("PRIVATE_PROXY"))
				return
			}
			_ = json.NewEncoder(w).Encode(m)
		default:
			f.bad = true
			w.WriteHeader(500)
		}
		return
	}
	if r.URL.Query().Encode() != "dryRun=All" || r.Header.Get("Accept-Encoding") != "identity" {
		f.bad = true
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, helmrelease.MaxSecretBytes+1))
	if err != nil || len(body) > helmrelease.MaxSecretBytes {
		f.bad = true
		w.WriteHeader(500)
		return
	}
	defer clear(body)
	if r.Method == http.MethodPatch && p == "/api/v1/namespaces/team/secrets/sh.helm.release.v1.example.v2" {
		f.patches++
		if r.Header.Get("Content-Type") != "application/json-patch+json" || r.Header.Get("Accept") != "application/json" {
			f.bad = true
		}
		if f.onPatch != nil {
			f.onPatch()
		}
		for _, status := range []int{400, 401, 403, 404, 409, 415, 429, 500} {
			if f.mode == fmt.Sprintf("patch-%d", status) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("PRIVATE_ERROR"))
				return
			}
		}
		if f.mode == "redirect" {
			w.Header().Set("Location", "/prefix/unexpected")
			w.WriteHeader(307)
			return
		}
		if f.mode == "patch-budget" {
			w.Header().Set("Content-Length", fmt.Sprint(helmrelease.MaxSecretBytes+1))
			return
		}
		patch, e := jsonpatch.DecodePatch(body)
		if e != nil {
			f.bad = true
			w.WriteHeader(500)
			return
		}
		out, e := patch.Apply(f.wire)
		if e != nil {
			f.bad = true
			w.WriteHeader(500)
			return
		}
		if f.mode == "patch-invalid" {
			out = []byte("PRIVATE_INVALID")
		}
		if f.mode == "patch-trailing" {
			out = append(out, []byte(" {}")...)
		}
		if f.mode == "patch-media" {
			w.Header().Set("Content-Type", "text/plain")
		}
		if f.mode == "patch-warning" {
			w.Header().Set("Warning", "299 example PRIVATE_WARNING")
		}
		if f.mode == "patch-encoding" {
			w.Header().Set("Content-Encoding", "gzip")
		}
		if strings.HasPrefix(f.mode, "patch-change-") {
			var object map[string]any
			_ = json.Unmarshal(out, &object)
			meta := object["metadata"].(map[string]any)
			switch strings.TrimPrefix(f.mode, "patch-change-") {
			case "uid":
				meta["uid"] = "other"
			case "namespace":
				meta["namespace"] = "other"
			case "rv-empty":
				meta["resourceVersion"] = ""
			case "labels":
				meta["labels"].(map[string]any)["status"] = "uninstalled"
			case "payload":
				object["data"].(map[string]any)["release"] = "U F J J V k F U R Q=="
			case "unknown":
				object["unknown"] = "PRIVATE"
			case "kind":
				object["kind"] = "ConfigMap"
			}
			out, _ = json.Marshal(object)
		}
		if f.mode == "patch-duplicate" {
			out = append([]byte(`{"kind":"Secret",`), out[1:]...)
		}
		_, _ = w.Write(out)
		clear(out)
		return
	}
	if r.Method == http.MethodDelete && (p == "/api/v1/namespaces/team/configmaps/sample" || p == "/api/v1/namespaces/team/configmaps/second") {
		wantUID, wantRV := "live-uid", "rv-a"
		if strings.HasSuffix(p, "/second") {
			wantUID, wantRV = "second-uid", "rv-b"
		}
		f.deletes++
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != liveMetadataAccept {
			f.bad = true
		}
		var options metav1.DeleteOptions
		if json.Unmarshal(body, &options) != nil || options.Preconditions == nil || options.Preconditions.UID == nil || options.Preconditions.ResourceVersion == nil ||
			string(*options.Preconditions.UID) != wantUID || *options.Preconditions.ResourceVersion != wantRV || options.PropagationPolicy == nil ||
			*options.PropagationPolicy != metav1.DeletePropagationOrphan || !reflect.DeepEqual(options.DryRun, []string{"All"}) ||
			options.GracePeriodSeconds != nil || options.OrphanDependents != nil || options.IgnoreStoreReadErrorWithClusterBreakingPotential != nil {
			f.bad = true
		}
		if f.onDelete != nil {
			f.onDelete()
		}
		if f.mode == "delete-denied" {
			w.WriteHeader(403)
			_, _ = w.Write([]byte("PRIVATE_ERROR"))
			return
		}
		if f.mode == "delete-missing" {
			w.WriteHeader(404)
			_, _ = w.Write([]byte("PRIVATE_ERROR"))
			return
		}
		if f.mode == "delete-budget" {
			w.Header().Set("Content-Length", fmt.Sprint(maxLiveObjectBytes+1))
			return
		}
		if strings.HasPrefix(f.mode, "status") {
			s := metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: "Success", Details: &metav1.StatusDetails{Name: "sample", Kind: "configmaps", UID: "live-uid"}}
			switch f.mode {
			case "status-failure":
				s.Status = "Failure"
			case "status-no-uid":
				s.Details.UID = ""
			case "status-wrong-name":
				s.Details.Name = "other"
			case "status-wrong-resource":
				s.Details.Kind = "secrets"
			case "status-wrong-code":
				s.Code = 404
			case "status-cause":
				s.Details.Causes = []metav1.StatusCause{{Message: "PRIVATE"}}
			case "status-code":
				s.Code = 200
			}
			_ = json.NewEncoder(w).Encode(s)
			return
		}
		m := liveObject()
		if strings.HasSuffix(p, "/second") {
			m.Name = "second"
			m.UID = "second-uid"
			m.ResourceVersion = "rv-b"
		}
		now := metav1.Now()
		zero := int64(0)
		m.DeletionTimestamp = &now
		m.DeletionGracePeriodSeconds = &zero
		m.Finalizers = []string{metav1.FinalizerOrphanDependents}
		switch f.mode {
		case "delete-full":
			m.TypeMeta = metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"}
		case "delete-uid":
			m.UID = "replacement"
		case "delete-name":
			m.Name = "other"
		case "delete-ns":
			m.Namespace = "elsewhere"
		case "delete-finalizer":
			m.Finalizers = []string{"custom/hold"}
		case "delete-not-terminating":
			m.DeletionTimestamp = nil
		case "delete-marker":
			delete(m.Labels, "app.kubernetes.io/managed-by")
		case "delete-hook":
			m.Annotations["helm.sh/hook"] = "pre-delete"
		case "delete-owner":
			m.OwnerReferences = []metav1.OwnerReference{{UID: "owner"}}
		case "delete-202":
			w.WriteHeader(202)
		}
		_ = json.NewEncoder(w).Encode(m)
		return
	}
	f.bad = true
	w.WriteHeader(500)
}

func TestHelmUninstallDryRunHTTP(t *testing.T) {
	for _, mode := range []string{"success", "status", "status-code", "uid-changed", "rv-changed", "source-changed", "content-denied", "missing",
		"patch-400", "patch-401", "patch-403", "patch-404", "patch-409", "patch-415", "patch-429", "patch-500", "redirect", "patch-budget", "patch-invalid", "patch-trailing", "patch-duplicate", "patch-media", "patch-warning", "patch-encoding",
		"patch-change-uid", "patch-change-namespace", "patch-change-rv-empty", "patch-change-labels", "patch-change-payload", "patch-change-unknown", "patch-change-kind",
		"delete-denied", "delete-missing", "delete-budget", "delete-full", "delete-uid", "delete-name", "delete-ns", "delete-finalizer", "delete-not-terminating", "delete-hook", "delete-marker", "delete-owner", "delete-202",
		"status-failure", "status-no-uid", "status-wrong-name", "status-wrong-resource", "status-wrong-code", "status-cause", "target-after", "source-after"} {
		t.Run(mode, func(t *testing.T) {
			f := newDryRunFixture(t)
			c := f.consent(t)
			f.mode = mode
			r, err := f.b.PreflightHelmUninstall(context.Background(), c, "session", "team", selectedHelmRevision())
			good := mode == "success" || mode == "status" || mode == "status-code"
			if good {
				if err != nil || r == nil || !r.Matches(f.b, "session", "team", selectedHelmRevision(), time.Now()) {
					t.Fatal("preflight", err)
				}
				if r.PreparationID() != f.p.ID() || r.ExpiresAt() != f.p.ExpiresAt() || f.patches != 1 || f.deletes != 1 {
					t.Fatal("renewed/repeated work")
				}
				targets := r.Targets()
				r.Targets()[0].UID = "tampered"
				if !reflect.DeepEqual(targets, r.Targets()) {
					t.Fatal("alias")
				}
				wire, _ := json.Marshal(r)
				if string(wire) != "{}" {
					t.Fatal("serialized state")
				}
				if r.Matches(f.b, "other", "team", selectedHelmRevision(), time.Now()) || r.Matches(f.b, "session", "team", selectedHelmRevision(), r.ExpiresAt()) {
					t.Fatal("binding")
				}
				c.Discard()
				if r.Matches(f.b, "session", "team", selectedHelmRevision(), time.Now()) {
					t.Fatal("revived report")
				}
			} else if err == nil || r != nil || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("unsafe/partial result", err)
			}
			requests := f.requests
			copyHandle := *c
			if _, err := f.b.PreflightHelmUninstall(context.Background(), &copyHandle, "session", "team", selectedHelmRevision()); err == nil || f.requests != requests {
				t.Fatal("copied consent replay")
			}
			if f.bad || f.patches > 1 || f.deletes > 1 {
				t.Fatal("bad/repeated request")
			}
			if strings.HasPrefix(mode, "patch-") || mode == "redirect" {
				if f.deletes != 0 {
					t.Fatal("continued after failed PATCH")
				}
			}
			if mode == "uid-changed" || mode == "rv-changed" || mode == "source-changed" || mode == "content-denied" || mode == "missing" {
				if f.patches+f.deletes != 0 {
					t.Fatal("stale preflight attempted")
				}
			}
		})
	}
}

func TestHelmUninstallDryRunConsentLifetime(t *testing.T) {
	for _, mode := range []string{"no-ack", "nil-consent", "zero-consent", "discard", "wrong-backend", "wrong-session", "wrong-namespace", "wrong-revision", "canceled", "revoked-in-patch", "revoked-in-delete"} {
		t.Run(mode, func(t *testing.T) {
			f := newDryRunFixture(t)
			c := f.consent(t)
			b := f.b
			session, ns := "session", "team"
			row := selectedHelmRevision()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "no-ack":
				out, err := b.AcknowledgeHelmUninstallDryRun(f.p, session, ns, row, false)
				if err == nil || out != nil {
					t.Fatal("no consent")
				}
				return
			case "nil-consent":
				c = nil
			case "zero-consent":
				c = &HelmUninstallDryRunConsent{}
			case "discard":
				c.Discard()
			case "wrong-backend":
				var err error
				b, err = New(b.Config())
				if err != nil {
					t.Fatal(err)
				}
			case "wrong-session":
				session = "other"
			case "wrong-namespace":
				ns = "other"
			case "wrong-revision":
				row.ResourceVersion = "other"
			case "canceled":
				cancel()
			case "revoked-in-patch":
				f.onPatch = c.Discard
			case "revoked-in-delete":
				f.onDelete = c.Discard
			}
			r, err := b.PreflightHelmUninstall(ctx, c, session, ns, row)
			if err == nil || r != nil {
				t.Fatal("invalid consent qualified")
			}
			if !strings.HasPrefix(mode, "revoked-in-") && f.requests != 0 {
				t.Fatal("unconsented network")
			}
			if mode == "revoked-in-patch" && f.deletes != 0 {
				t.Fatal("revocation ignored")
			}
		})
	}
}

func TestHelmUninstallDryRunConcurrentConsumption(t *testing.T) {
	f := newDryRunFixture(t)
	c := f.consent(t)
	var wg sync.WaitGroup
	var passed atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copied := *c
			r, err := f.b.PreflightHelmUninstall(context.Background(), &copied, "session", "team", selectedHelmRevision())
			if err == nil && r != nil {
				passed.Add(1)
			}
		}()
	}
	wg.Wait()
	if passed.Load() != 1 || f.patches != 1 || f.deletes != 1 || f.bad {
		t.Fatal("single consumption failed", passed.Load(), f.patches, f.deletes)
	}
}

func TestHelmUninstallDryRunOriginalExpiry(t *testing.T) {
	f := newDryRunFixture(t)
	row := selectedHelmRevision()
	started := time.Now().Add(-time.Minute)
	profile, err := helmrelease.InspectUninstallProfile(context.Background(), f.wire, "team", row, "session", started)
	if err != nil {
		t.Fatal(err)
	}
	before := helmrelease.Snapshot{Namespace: "team", ResourceVersion: "collection", ObservedAt: started.Add(time.Second), Revisions: []helmrelease.Revision{row}}
	after := before
	after.ObservedAt = started.Add(3 * time.Second)
	items := []helmrelease.LiveItem{{Stored: profile.Preview().Items()[0], Resource: "configmaps", Namespace: "team", UID: "live-uid", ResourceVersion: "rv-a", State: "present", Markers: "match", ObservedAt: started.Add(2 * time.Second)}}
	prepared, err := helmrelease.FinishUninstallPreparation(context.Background(), profile, "session", before, after, items, started.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	p := &HelmUninstallPreparation{owner: f.b.identity, result: prepared}
	if c, err := f.b.AcknowledgeHelmUninstallDryRun(p, "session", "team", row, true); err == nil || c != nil {
		t.Fatal("expired acknowledgement")
	}
	// Construct only the private test state to verify the dispatch expiry gate.
	c := &HelmUninstallDryRunConsent{state: &helmDryRunConsentState{preparation: p}}
	if r, err := f.b.PreflightHelmUninstall(context.Background(), c, "session", "team", row); err == nil || r != nil || f.requests != 0 {
		t.Fatal("expired dispatch")
	}
}

func TestHelmUninstallDryRunBodyLifetime(t *testing.T) {
	original := []byte("PRIVATE_BODY")
	owned := &helmDryRunBody{data: append([]byte(nil), original...)}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = io.ReadAll(owned); _ = owned.Close() }()
	}
	wg.Wait()
	if string(original) != "PRIVATE_BODY" || len(owned.data) != 0 {
		t.Fatal("body ownership")
	}
	if n, err := owned.Read(make([]byte, 1)); n != 0 || err != io.EOF {
		t.Fatal("closed body read")
	}
}

func TestHelmUninstallDryRunBatchStops(t *testing.T) {
	for _, mode := range []string{"two-targets", "first-denied", "first-revoked"} {
		t.Run(mode, func(t *testing.T) {
			f := newDryRunFixture(t, true)
			if len(f.p.Targets()) != 2 {
				t.Fatal("fixture did not contain two targets")
			}
			c := f.consent(t)
			if mode == "first-denied" {
				f.mode = "delete-denied"
			}
			if mode == "first-revoked" {
				f.onDelete = c.Discard
			}
			r, err := f.b.PreflightHelmUninstall(context.Background(), c, "session", "team", selectedHelmRevision())
			f.mu.Lock()
			defer f.mu.Unlock()
			if mode == "two-targets" {
				if err != nil || r == nil || len(r.Targets()) != 2 || f.deletes != 2 {
					t.Fatal("batch", err)
				}
			} else if err == nil || r != nil || f.deletes != 1 {
				t.Fatal("later DELETE scheduled after failure", err)
			}
			if f.patches != 1 || f.bad {
				t.Fatal("request invariant")
			}
		})
	}
}
