package uiworkbench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmfixture"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

// This fixture applies the actual conditional JSON patches and DELETE
// preconditions. Its retained server state is independent of UI result objects.
// Delayed-response modes commit first, allowing cancellation and lost replies
// to exercise genuine Unknown outcomes in the native event loop.
type nativeHelmUninstallFixture struct {
	mu                 sync.Mutex
	server             *httptest.Server
	wire, original     []byte
	objects            map[string]metav1.PartialObjectMetadata
	deleted            map[string]bool
	calls              []string
	dry, writes        int
	patches            int
	mode               string
	blocked            bool
	entered, cancelled chan struct{}
	bad                []string
}

func newNativeHelmUninstallFixture(t *testing.T, count int) *nativeHelmUninstallFixture {
	t.Helper()
	var manifest strings.Builder
	f := &nativeHelmUninstallFixture{objects: make(map[string]metav1.PartialObjectMetadata), deleted: make(map[string]bool), entered: make(chan struct{}), cancelled: make(chan struct{})}
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("config-%02d", i)
		fmt.Fprintf(&manifest, "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: %s\n  namespace: team\ndata:\n  example: retained\n", name)
		f.objects[name] = metav1.PartialObjectMetadata{TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "PartialObjectMetadata"}, ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "team", UID: types.UID("uid-" + name), ResourceVersion: "rv-" + name,
			Labels: map[string]string{"app.kubernetes.io/managed-by": "Helm"}, Annotations: map[string]string{"meta.helm.sh/release-name": "example", "meta.helm.sh/release-namespace": "team"}}}
	}
	payload, err := json.Marshal(map[string]any{"name": "example", "namespace": "team", "version": 2, "info": map[string]any{"status": "deployed"}, "chart": map[string]any{"metadata": map[string]any{"name": "example-chart", "version": "1.2.3"}}, "manifest": manifest.String(), "config": map[string]any{"password": "PRIVATE_NATIVE_HELM_PAYLOAD"}})
	if err != nil {
		t.Fatal(err)
	}
	secret := helmfixture.Secret("team", "example", "deployed", 2)
	secret.Data["release"] = helmfixture.Stored(payload)
	f.wire, err = json.Marshal(secret)
	if err != nil {
		t.Fatal(err)
	}
	f.original = bytes.Clone(f.wire)
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(func() {
		f.server.Close()
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.bad) > 0 {
			t.Errorf("native Helm request invariant: %v", f.bad)
		}
	})
	return f
}

func (f *nativeHelmUninstallFixture) backend(t *testing.T) *kube.Backend {
	t.Helper()
	b, err := kube.New(&rest.Config{Host: f.server.URL, BearerToken: "native-helm-current", RateLimiter: flowcontrol.NewFakeAlwaysRateLimiter()})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f *nativeHelmUninstallFixture) harness(t *testing.T) *relationshipHarness {
	t.Helper()
	h := newRelationshipHarness(t, 25*time.Second)
	h.w.backend, h.w.namespace, h.w.sessionID = f.backend(t), "team", "native-helm-session"
	h.w.activeContext, h.w.currentContext, h.w.activeNamespace = "test-context", "test-context", "team"
	h.w.helmJournalPath = filepath.Join(t.TempDir(), "journal")
	h.tt.SetSize(1100, 700)
	h.tt.Frame()
	return h
}

func (f *nativeHelmUninstallFixture) setMode(mode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode = mode
}

func (f *nativeHelmUninstallFixture) snapshot(t *testing.T) (int, int, int, corev1.Secret, []string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var secret corev1.Secret
	if err := json.Unmarshal(f.wire, &secret); err != nil {
		t.Fatal(err)
	}
	deleted := 0
	for _, yes := range f.deleted {
		if yes {
			deleted++
		}
	}
	return f.dry, f.writes, deleted, secret, append([]string(nil), f.calls...)
}

func (f *nativeHelmUninstallFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	locked := true
	defer func() {
		if locked {
			f.mu.Unlock()
		}
	}()
	w.Header().Set("Content-Type", "application/json")
	f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
	if r.Header.Get("Authorization") != "Bearer native-helm-current" {
		f.bad = append(f.bad, "current credential changed")
	}
	path := r.URL.Path
	secretPath := "/api/v1/namespaces/team/secrets/sh.helm.release.v1.example.v2"
	if r.Method == http.MethodGet {
		switch path {
		case "/api/v1/namespaces/team/secrets":
			var secret corev1.Secret
			_ = json.Unmarshal(f.wire, &secret)
			_ = json.NewEncoder(w).Encode(metav1.PartialObjectMetadataList{TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "PartialObjectMetadataList"}, ListMeta: metav1.ListMeta{ResourceVersion: "snapshot"}, Items: []metav1.PartialObjectMetadata{{ObjectMeta: secret.ObjectMeta}}})
		case secretPath:
			if f.mode == "deny-content" {
				http.Error(w, "PRIVATE_NATIVE_HELM_ERROR", http.StatusForbidden)
				return
			}
			if f.mode == "block-content" && !f.blocked {
				f.blocked = true
				f.mu.Unlock()
				locked = false
				close(f.entered)
				<-r.Context().Done()
				close(f.cancelled)
				return
			}
			_, _ = w.Write(f.wire)
		case "/api/v1":
			_ = json.NewEncoder(w).Encode(metav1.APIResourceList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "APIResourceList"}, GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "configmaps", Kind: "ConfigMap", Namespaced: true, Verbs: metav1.Verbs{"get", "delete"}}}})
		default:
			name := strings.TrimPrefix(path, "/api/v1/namespaces/team/configmaps/")
			obj, exists := f.objects[name]
			if !exists {
				f.bad = append(f.bad, "unexpected GET "+path)
				w.WriteHeader(500)
				return
			}
			if f.deleted[name] {
				w.WriteHeader(404)
				_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: metav1.StatusFailure, Reason: metav1.StatusReasonNotFound, Code: 404, Details: &metav1.StatusDetails{Kind: "configmaps", Name: name}})
				return
			}
			_ = json.NewEncoder(w).Encode(obj)
		}
		return
	}
	dry := r.URL.Query().Encode() == "dryRun=All"
	if !dry && r.URL.RawQuery != "" {
		f.bad = append(f.bad, "unexpected mutation query")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, helmrelease.MaxSecretBytes+1))
	if err != nil || len(data) > helmrelease.MaxSecretBytes {
		f.bad = append(f.bad, "mutation body invalid")
		w.WriteHeader(500)
		return
	}
	defer clear(data)
	var response []byte
	kind := ""
	if r.Method == http.MethodPatch && path == secretPath {
		patch, decodeErr := jsonpatch.DecodePatch(data)
		if decodeErr == nil {
			response, decodeErr = patch.Apply(f.wire)
		}
		if decodeErr != nil {
			f.bad = append(f.bad, "conditional patch failed")
			w.WriteHeader(409)
			return
		}
		var after corev1.Secret
		_ = json.Unmarshal(response, &after)
		kind = "begin"
		if f.patches > 0 {
			kind = "finish"
		}
		if dry {
			after.ResourceVersion = "hypothetical"
		} else {
			f.patches++
			after.ResourceVersion = strconv.Itoa(7 + f.patches)
		}
		response, _ = json.Marshal(after)
		if !dry {
			f.wire = bytes.Clone(response)
		}
	} else if r.Method == http.MethodDelete {
		name := strings.TrimPrefix(path, "/api/v1/namespaces/team/configmaps/")
		obj, exists := f.objects[name]
		var options metav1.DeleteOptions
		if !exists || json.Unmarshal(data, &options) != nil || options.Preconditions == nil || options.Preconditions.UID == nil || options.Preconditions.ResourceVersion == nil || *options.Preconditions.UID != obj.UID || *options.Preconditions.ResourceVersion != obj.ResourceVersion || options.PropagationPolicy == nil || *options.PropagationPolicy != metav1.DeletePropagationOrphan {
			f.bad = append(f.bad, "exact DELETE preconditions changed")
			w.WriteHeader(409)
			return
		}
		if dry && !reflect.DeepEqual(options.DryRun, []string{metav1.DryRunAll}) || !dry && len(options.DryRun) != 0 {
			f.bad = append(f.bad, "DELETE dry-run disagreement")
		}
		kind = "delete"
		copy := obj.DeepCopy()
		now, zero := metav1.Now(), int64(0)
		copy.DeletionTimestamp, copy.DeletionGracePeriodSeconds = &now, &zero
		copy.Finalizers = []string{metav1.FinalizerOrphanDependents}
		copy.ResourceVersion = "deleting-" + obj.ResourceVersion
		response, _ = json.Marshal(copy)
		if !dry {
			f.deleted[name] = true
		}
	} else {
		f.bad = append(f.bad, "unexpected mutation "+r.Method+" "+path)
		w.WriteHeader(500)
		return
	}
	if dry {
		f.dry++
	} else {
		f.writes++
		if f.mode == "block-"+kind && !f.blocked {
			f.blocked = true
			f.mu.Unlock()
			locked = false
			close(f.entered)
			<-r.Context().Done()
			close(f.cancelled)
			return
		}
		if f.mode == "lose-"+kind && !f.blocked {
			f.blocked = true
			conn, _, hijackErr := w.(http.Hijacker).Hijack()
			if hijackErr == nil {
				_ = conn.Close()
			}
			return
		}
	}
	_, _ = w.Write(response)
}

func nativeHelmReach(h *relationshipHarness, label string) {
	h.t.Helper()
	h.tt.Frame()
	viewport, ok := h.tt.Find("Helm uninstall inventory")
	if !ok {
		h.t.Fatal("missing uninstall viewport")
	}
	h.tt.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, -100000)
	h.tt.Frame()
	for i := 0; i < 100; i++ {
		bounds, found := h.tt.Find(label)
		// Find returns the clipped visible rectangle, not the original layout
		// box. A positive fragment at an edge is insufficient: require vertical
		// space on both sides. Platform font metrics determine text height, so
		// a fixed minimum would reject fully visible 12-DIP Core Text labels.
		if found && bounds.H > 0 && bounds.W > 0 &&
			bounds.X >= viewport.X && bounds.X+bounds.W <= viewport.X+viewport.W &&
			bounds.Y >= viewport.Y+1 && bounds.Y+bounds.H <= viewport.Y+viewport.H-1 {
			return
		}
		h.tt.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, 90)
		h.tt.Frame()
	}
	saveNativeScreenshot(h.t, h.tt)
	bounds, found := h.tt.Find(label)
	h.t.Fatalf("unreachable Helm control %q: found=%t visible=%+v viewport=%+v", label, found, bounds, viewport)
}

func nativeHelmClick(h *relationshipHarness, label string) {
	h.t.Helper()
	nativeHelmReach(h, label)
	h.click(label)
}

func openNativeHelmReview(h *relationshipHarness) {
	h.t.Helper()
	h.click("Helm releases")
	h.click("Refresh Helm metadata")
	h.pump(func() bool { return !h.w.helmReleases.busy })
	h.click("Select sh.helm.release.v1.example.v2")
	h.click("Review restricted uninstall")
	if !h.w.helmUninstall.open {
		h.t.Fatal("review did not open")
	}
}

func prepareNativeHelmReview(h *relationshipHarness) {
	h.t.Helper()
	nativeHelmClick(h, helmPrepareConsentLabel)
	nativeHelmClick(h, "Prepare restricted uninstall")
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	if h.w.helmUninstall.preparation == nil {
		h.t.Fatal("preparation failed", h.w.helmUninstall.status)
	}
}

func preflightNativeHelmReview(h *relationshipHarness) {
	h.t.Helper()
	prepareNativeHelmReview(h)
	nativeHelmClick(h, helmDryRunConsentLabel)
	nativeHelmClick(h, "Run uninstall dry-run")
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	if h.w.helmUninstall.dryRun == nil {
		h.t.Fatal("dry-run failed", h.w.helmUninstall.status)
	}
}

func confirmNativeHelmExecution(h *relationshipHarness) {
	h.t.Helper()
	nativeHelmClick(h, helmExecuteConsentLabel)
	nativeHelmReach(h, "Helm uninstall release confirmation")
	h.click("Helm uninstall release confirmation")
	h.tt.Type("example")
	h.tt.Frame()
	if h.w.helmUninstall.confirmation != "example" {
		h.t.Fatal("exact confirmation not committed")
	}
	nativeHelmClick(h, "Execute restricted uninstall")
}
