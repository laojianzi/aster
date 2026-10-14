package uiworkbench

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/operation"
	"k8s.io/client-go/rest"
)

func writeHelmMetadata(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"apiVersion":"meta.k8s.io/v1","kind":"PartialObjectMetadataList","metadata":{"resourceVersion":"8"},"items":[{"metadata":{"name":"sh.helm.release.v1.example.v2","namespace":"team","uid":"uid2","resourceVersion":"7","labels":{"owner":"helm","name":"example","version":"2","status":"deployed"},"annotations":{"private":"PRIVATE_ANNOTATION"}}},{"metadata":{"name":"sh.helm.release.v1.example.v1","namespace":"team","uid":"uid1","resourceVersion":"6","labels":{"owner":"helm","name":"example","version":"1","status":"superseded"}}}]}`))
}
func helmMetadataHarness(t *testing.T, host string) *relationshipHarness {
	t.Helper()
	h := newRelationshipHarness(t, 15*time.Second)
	b, err := kube.New(&rest.Config{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	h.w.backend, h.w.namespace, h.w.sessionID, h.w.activeContext = b, "team", "helm-metadata-ui", "test-context"
	h.tt.Frame()
	return h
}
func TestNativeHelmMetadataReadOnlyAndMinimumWindow(t *testing.T) {
	var calls, bad atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/api/v1/namespaces/team/secrets" || r.Header.Get("Accept") != helmrelease.Accept {
			bad.Add(1)
			http.Error(w, "unexpected", 400)
			return
		}
		writeHelmMetadata(w)
	}))
	defer server.Close()
	h := helmMetadataHarness(t, server.URL)
	h.tt = ui.NewTester(h.w.View, 1100, 700)
	editor := ui.NewTextBuffer("raw\r\n中文 🚢\n")
	h.w.editor = editor
	h.w.editorObserved = editor.Snapshot()
	deliveryEditor := ui.NewTextBuffer("retained delivery")
	h.w.delivery.editor = deliveryEditor
	h.w.delivery.observed = deliveryEditor.Snapshot()
	plan := &operation.Prepared{}
	h.w.plan = plan
	h.click("Helm releases")
	if calls.Load() != 0 {
		t.Fatal("opening panel performed a read")
	}
	h.click("Refresh Helm metadata")
	h.pump(func() bool { return !h.w.helmReleases.busy })
	r := h.w.helmReleases.snapshot
	if len(r.Revisions) != 2 || r.Revisions[0].Number != 2 || r.Namespace != "team" || calls.Load() != 1 || bad.Load() != 0 {
		t.Fatalf("%+v %s", r, h.w.helmReleases.status)
	}
	projected, _ := json.Marshal(r)
	if strings.Contains(string(projected), "PRIVATE") {
		t.Fatal("arbitrary metadata retained")
	}
	for _, label := range []string{"Refresh Helm metadata", "Stop Helm metadata", "Close Helm metadata", "Helm metadata namespace", "Helm revision sh.helm.release.v1.example.v2"} {
		bounds, ok := h.tt.Find(label)
		if !ok || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > 1100 || bounds.Y+bounds.H > 700 {
			t.Fatalf("missing minimum-window control %s %+v", label, bounds)
		}
	}
	saveNativeScreenshot(t, h.tt)
	h.click("Close Helm metadata")
	if h.w.helmReleases.open || h.w.editor != editor || editor.Snapshot().String() != "raw\r\n中文 🚢\n" || h.w.delivery.editor != deliveryEditor || h.w.plan != plan || h.w.pendingWrites != 0 {
		t.Fatal("metadata view changed draft or approval state")
	}
	// A mounted detail editor cannot be replaced by this panel.
	h.w.detail = relatedPod()
	h.w.openHelmReleases()
	if h.w.helmReleases.open {
		t.Fatal("metadata view unmounted live detail")
	}
}
func TestNativeHelmMetadataScopeCancellationAndStaleResults(t *testing.T) {
	for _, scenario := range []string{"close", "namespace", "context", "backend", "disconnect", "teardown"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeHelmMetadata(w) }))
			defer server.Close()
			h := helmMetadataHarness(t, server.URL)
			h.click("Helm releases")
			h.click("Refresh Helm metadata")
			var completion func()
			select {
			case completion = <-h.updates:
			case <-h.ctx.Done():
				t.Fatal("no queued result")
			}
			switch scenario {
			case "close":
				h.click("Close Helm metadata")
			case "namespace":
				h.w.namespace = "other"
				h.tt.Frame()
			case "context":
				h.w.contextEpoch++
				h.tt.Frame()
			case "backend":
				h.w.backend = nil
				h.tt.Frame()
			case "disconnect":
				h.click("Disconnect")
			case "teardown":
				h.w.Close()
			}
			completion()
			h.tt.Frame()
			if h.w.helmReleases.open || h.w.helmReleases.busy || len(h.w.helmReleases.snapshot.Revisions) != 0 {
				t.Fatal("queued result restored stale scope")
			}
		})
	}
	started, cancelled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(cancelled) }))
	defer server.Close()
	h := helmMetadataHarness(t, server.URL)
	h.click("Helm releases")
	h.click("Refresh Helm metadata")
	select {
	case <-started:
	case <-h.ctx.Done():
		t.Fatal("read did not start")
	}
	h.click("Stop Helm metadata")
	select {
	case <-cancelled:
	case <-h.ctx.Done():
		t.Fatal("stop did not cancel transport")
	}
	if h.w.helmReleases.busy || len(h.w.helmReleases.snapshot.Revisions) != 0 {
		t.Fatal("cancelled read remained active")
	}
	h.click("Close Helm metadata")
	h.w.namespace = "*"
	h.tt.Frame()
	h.click("Helm releases")
	h.w.refreshHelmReleases()
	if h.w.helmReleases.busy || h.w.helmReleases.status != helmrelease.ErrNamespace.Error() {
		t.Fatal("wildcard namespace admitted")
	}
}
func TestNativeHelmMetadataFailureClearsPreviousSnapshot(t *testing.T) {
	var deny atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if deny.Load() {
			http.Error(w, "PRIVATE_RESPONSE", 403)
			return
		}
		writeHelmMetadata(w)
	}))
	defer server.Close()
	h := helmMetadataHarness(t, server.URL)
	h.click("Helm releases")
	h.click("Refresh Helm metadata")
	h.pump(func() bool { return !h.w.helmReleases.busy })
	if len(h.w.helmReleases.snapshot.Revisions) != 2 {
		t.Fatal("initial snapshot missing")
	}
	deny.Store(true)
	h.click("Refresh Helm metadata")
	h.pump(func() bool { return !h.w.helmReleases.busy })
	if len(h.w.helmReleases.snapshot.Revisions) != 0 || h.w.helmReleases.status != helmrelease.ErrForbidden.Error() {
		t.Fatal("failure retained data or leaked response")
	}
}
