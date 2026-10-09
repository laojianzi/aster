package uiworkbench

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/resourcemetrics"
	"github.com/laojianzi/aster/internal/workspace"
	"k8s.io/client-go/rest"
)

func TestNativeNewWorkspaceDoesNotCopyIdentityOrPreparedState(t *testing.T) {
	h := newRelationshipHarness(t, 5*time.Second)
	h.w.workspaceNumber = 1
	h.w.path = "first-config"
	h.w.currentContext = "production"
	h.w.trustedFingerprint = "approved-first"
	h.w.editor = "private draft"
	h.w.sessionID = "first-session"
	var child *Workbench
	h.w.newWorkspace = func() error { child = New(); return nil }
	h.tt.Frame()
	h.click("New workspace")
	if child == nil || child.path != "" || child.backend != nil || child.ops != nil || child.plan != nil || child.sessionID != "" || child.editor != "" || child.trustedFingerprint != "" || child.currentContext != "" {
		t.Fatal("new workspace inherited privileged state")
	}
	h.w.newWorkspace = func() error { return workspace.ErrLimit }
	h.tt.Frame()
	h.click("New workspace")
	if h.w.notice != workspace.ErrLimit.Error() {
		t.Fatal("workspace capacity failure hidden")
	}
}
func TestNativeLoadingContextsRequiresExplicitConnect(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "config")
	data := fmt.Sprintf("apiVersion: v1\nkind: Config\ncurrent-context: test\nclusters:\n- name: test\n  cluster:\n    server: %s\ncontexts:\n- name: test\n  context:\n    cluster: test\n    user: test\nusers:\n- name: test\n  user:\n    token: synthetic-test-token\n", server.URL)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	h := newRelationshipHarness(t, 5*time.Second)
	h.w.path = path
	h.tt.Frame()
	h.click("Load contexts")
	h.pump(func() bool { return h.w.status == "Choose a context and connect" })
	if requests.Load() != 0 || h.w.backend != nil || h.w.trustRequired || h.w.trustedFingerprint != "" || h.w.currentContext != "test" {
		t.Fatal("loading contexts performed an implicit connection")
	}
}
func TestNativeWorkspacesKeepCredentialsDataAndCancellationSeparate(t *testing.T) {
	var requestsA, requestsB atomic.Int32
	makeServer := func(token string, cpu string, calls *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Error("credential crossed workspace")
				http.Error(w, "denied", 403)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/v1/namespaces/team/pods/related-pod":
				_ = json.NewEncoder(w).Encode(metricsPod())
			case "/apis/metrics.k8s.io":
				fmt.Fprint(w, `{"name":"metrics.k8s.io","versions":[{"version":"v1beta1","groupVersion":"metrics.k8s.io/v1beta1"}]}`)
			default:
				fmt.Fprintf(w, `{"apiVersion":"metrics.k8s.io/v1beta1","kind":"PodMetrics","metadata":{"name":"related-pod","namespace":"team"},"timestamp":%q,"window":"15s","containers":[{"name":"app","usage":{"cpu":%q,"memory":"64Mi"}}]}`, time.Now().UTC().Format(time.RFC3339Nano), cpu)
			}
		}))
	}
	aServer := makeServer("alpha", "100m", &requestsA)
	defer aServer.Close()
	bServer := makeServer("beta", "900m", &requestsB)
	defer bServer.Close()
	a, b := newRelationshipHarness(t, 10*time.Second), newRelationshipHarness(t, 10*time.Second)
	for i, h := range []*relationshipHarness{a, b} {
		token, host := "alpha", aServer.URL
		if i == 1 {
			token, host = "beta", bServer.URL
		}
		backend, err := kube.New(&rest.Config{Host: host, BearerToken: token})
		if err != nil {
			t.Fatal(err)
		}
		h.w.backend = backend
		h.w.sessionID = token
		h.w.detail = metricsPod()
		h.w.detailKind = catalog()[0]
		h.tt.Frame()
	}
	a.click("Metrics")
	b.click("Metrics")
	a.pump(func() bool { return !a.w.metricsLoading })
	b.pump(func() bool { return !b.w.metricsLoading })
	if a.w.metricsResult.State != resourcemetrics.Ready || b.w.metricsResult.State != resourcemetrics.Ready || a.w.metricsResult.Total.CPUCores != 0.1 || b.w.metricsResult.Total.CPUCores != 0.9 {
		t.Fatal("identically named resources crossed workspaces")
	}
	ran := false
	a.w.emit(func() { ran = true })
	queued := <-a.updates
	a.w.Close()
	queued()
	if ran {
		t.Fatal("queued result executed after workspace closed")
	}
	before := requestsB.Load()
	b.click("Refresh metrics")
	b.pump(func() bool { return !b.w.metricsLoading })
	if b.w.ctx.Err() != nil || b.w.metricsResult.Total.CPUCores != 0.9 || requestsB.Load() <= before {
		t.Fatal("closing first window canceled the other")
	}
	// An already queued result must do nothing once its own window has closed.
	a.w.emit(func() { b.w.notice = "crossed" })
	select {
	case fn := <-a.updates:
		fn()
	default:
	}
	if b.w.notice == "crossed" {
		t.Fatal("closed window emitted a callback")
	}
}
func TestNativeWorkspaceControlsAtMinimumWindow(t *testing.T) {
	w := New()
	w.workspaceNumber = 4
	w.newWorkspace = func() error { return nil }
	tt := ui.NewTester(w.View, 1100, 700)
	for _, label := range []string{"Workspace identity", "New workspace", "Load contexts"} {
		r, ok := tt.Find(label)
		if !ok || r.X < 0 || r.Y < 0 || r.W <= 0 || r.H <= 0 || r.X+r.W > 1100 || r.Y+r.H > 700 {
			t.Fatalf("%s not usable: %+v", label, r)
		}
	}
	saveNativeScreenshot(t, tt)
}
