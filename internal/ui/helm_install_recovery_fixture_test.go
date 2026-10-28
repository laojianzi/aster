package uiworkbench

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

// This fixture serves only public metadata and creates inert history through
// the real shared Store. It has no Secret payload, install runner or write route.
type nativeHelmInstallRecoveryFixture struct {
	mu                  sync.Mutex
	server              *httptest.Server
	path, id            string
	plan                helmjournal.InstallPlan
	original            []byte
	namespace, storage  metav1.PartialObjectMetadata
	target              metav1.PartialObjectMetadata
	calls, bad          []string
	mode                string
	entered, cancelled  chan struct{}
	blockOnce, doneOnce sync.Once
}

func newNativeHelmInstallRecoveryFixture(t *testing.T, outcome string) *nativeHelmInstallRecoveryFixture {
	t.Helper()
	meta := func(name, ns, uid, rv string) metav1.PartialObjectMetadata {
		return metav1.PartialObjectMetadata{TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "PartialObjectMetadata"},
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: types.UID(uid), ResourceVersion: rv}}
	}
	f := &nativeHelmInstallRecoveryFixture{path: filepath.Join(t.TempDir(), "journal"),
		plan: nativeHelmInstallHistoryPlan("example"), entered: make(chan struct{}), cancelled: make(chan struct{})}
	f.namespace = meta("team", "", "namespace-uid", "100")
	f.storage = meta(f.plan.StorageName, "team", "storage-uid", "101")
	f.storage.Labels = map[string]string{"owner": "helm", "name": "example", "version": "1", "status": "pending-install"}
	f.target = meta("config-00", "team", "uid-config-00", "102")
	f.target.Labels = map[string]string{"app.kubernetes.io/managed-by": "Helm"}
	f.target.Annotations = map[string]string{"meta.helm.sh/release-name": "example", "meta.helm.sh/release-namespace": "team"}
	if outcome == helmjournal.Completed {
		f.storage.ResourceVersion = "103"
		f.storage.Labels["status"] = "deployed"
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(func() {
		f.server.Close()
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.bad) != 0 {
			t.Errorf("installation recovery made an unsafe request: %v", f.bad)
		}
	})
	// This is the documented inert connection-digest schema, not an authority
	// constructor. New read consent must still validate the real Backend/session.
	ca := sha256.Sum256(nil)
	binding, err := json.Marshal(struct {
		Purpose, Endpoint, ServerName, CA string
		Insecure                          bool
		NextProtos                        []string
		Impersonate                       rest.ImpersonationConfig
	}{"aster/helm-uninstall/connection/v1", f.server.URL, "", hex.EncodeToString(ca[:]), false, nil, rest.ImpersonationConfig{}})
	nativeHelmJournalMust(t, err)
	digest := sha256.Sum256(binding)
	f.plan.Binding = hex.EncodeToString(digest[:])
	s, err := helmjournal.Open(f.path)
	nativeHelmJournalMust(t, err)
	f.id = writeNativeHelmInstallHistory(t, s, f.plan, outcome)
	nativeHelmJournalMust(t, s.Close())
	f.original, err = os.ReadFile(filepath.Join(f.path, f.id+".wal"))
	nativeHelmJournalMust(t, err)
	return f
}

func (f *nativeHelmInstallRecoveryFixture) backend(t *testing.T) *kube.Backend {
	t.Helper()
	b, err := kube.New(&rest.Config{Host: f.server.URL, BearerToken: "native-install-current", RateLimiter: flowcontrol.NewFakeAlwaysRateLimiter()})
	nativeHelmJournalMust(t, err)
	return b
}

func (f *nativeHelmInstallRecoveryFixture) harness(t *testing.T, connected bool) *relationshipHarness {
	t.Helper()
	h := newRelationshipHarness(t, 20*time.Second)
	h.w.helmJournalPath = f.path
	if connected {
		h.w.backend, h.w.namespace, h.w.sessionID = f.backend(t), "team", "fresh-install-observation-session"
		h.w.activeContext, h.w.currentContext, h.w.activeNamespace = "test-context", "test-context", "team"
	}
	h.tt.SetSize(1100, 700)
	h.tt.Frame()
	return h
}

func selectNativeHelmInstallHistory(t *testing.T, h *relationshipHarness, id string) {
	t.Helper()
	h.w.openHelmRecovery()
	h.w.loadHelmRecovery()
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	h.w.selectHelmRecovery(id)
	h.tt.Frame()
	if h.w.helmUninstall.installRecovery == nil || h.w.helmUninstall.selectedID != id {
		t.Fatal("installation history did not become selected", h.w.helmUninstall.status)
	}
}

func (f *nativeHelmInstallRecoveryFixture) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *nativeHelmInstallRecoveryFixture) setMode(mode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode = mode
}

func (f *nativeHelmInstallRecoveryFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
	mode := f.mode
	ns, secret, target := f.namespace.DeepCopy(), f.storage.DeepCopy(), f.target.DeepCopy()
	bad := r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer native-install-current" || r.ContentLength > 0
	if r.URL.Path != "/api/v1" && !strings.Contains(r.Header.Get("Accept"), "PartialObjectMetadata") {
		bad = true
	}
	if bad {
		f.bad = append(f.bad, r.Method+" "+r.URL.RequestURI())
	}
	f.mu.Unlock()
	if bad {
		http.Error(w, "PRIVATE_INSTALL_RECOVERY_ERROR", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/v1":
		_ = json.NewEncoder(w).Encode(metav1.APIResourceList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "APIResourceList"}, GroupVersion: "v1", APIResources: []metav1.APIResource{
			{Name: "namespaces", Kind: "Namespace", Verbs: metav1.Verbs{"get"}},
			{Name: "secrets", Kind: "Secret", Namespaced: true, Verbs: metav1.Verbs{"get", "list"}},
			{Name: "configmaps", Kind: "ConfigMap", Namespaced: true, Verbs: metav1.Verbs{"get"}},
		}})
	case "/api/v1/namespaces/team":
		_ = json.NewEncoder(w).Encode(ns)
	case "/api/v1/namespaces/team/secrets":
		items := []metav1.PartialObjectMetadata{*secret}
		if mode == "absent" {
			items = []metav1.PartialObjectMetadata{}
		}
		_ = json.NewEncoder(w).Encode(metav1.PartialObjectMetadataList{TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "PartialObjectMetadataList"}, ListMeta: metav1.ListMeta{ResourceVersion: "snapshot"}, Items: items})
	case "/api/v1/namespaces/team/secrets/sh.helm.release.v1.example.v1":
		if mode == "absent" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404,"details":{"name":"sh.helm.release.v1.example.v1","kind":"secrets"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(secret)
	case "/api/v1/namespaces/team/configmaps/config-00":
		if mode == "block" {
			f.blockOnce.Do(func() { close(f.entered) })
			<-r.Context().Done()
			f.doneOnce.Do(func() { close(f.cancelled) })
			return
		}
		if mode == "deny" {
			http.Error(w, "PRIVATE_INSTALL_RECOVERY_ERROR", http.StatusForbidden)
			return
		}
		if mode == "absent" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404,"details":{"name":"config-00","kind":"configmaps"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(target)
	default:
		f.mu.Lock()
		f.bad = append(f.bad, "unexpected route "+r.URL.RequestURI())
		f.mu.Unlock()
		http.Error(w, "PRIVATE_INSTALL_RECOVERY_ERROR", http.StatusNotFound)
	}
}
