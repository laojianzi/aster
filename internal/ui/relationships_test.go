package uiworkbench

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/relationship"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
)

type relationshipHarness struct {
	w       *Workbench
	tt      *ui.Tester
	ctx     context.Context
	updates chan func()
	t       *testing.T
}

func newRelationshipHarness(t *testing.T, timeout time.Duration) *relationshipHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	h := &relationshipHarness{w: New(), ctx: ctx, updates: make(chan func(), 128), t: t}
	h.w.attach(ctx, func(fn func()) {
		select {
		case h.updates <- fn:
		case <-ctx.Done():
		}
	})
	t.Cleanup(func() { cancel(); h.w.Close() })
	h.tt = ui.NewTester(h.w.View, 1440, 1000)
	return h
}
func (h *relationshipHarness) click(label string) {
	h.t.Helper()
	if _, visible := h.tt.Find(label); !visible && (label == "Deliver manifests" || label == "Helm releases" || label == "Helm recovery") {
		if err := h.tt.Click("Tools"); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := h.tt.Click(label); err != nil {
		saveNativeScreenshot(h.t, h.tt)
		h.t.Fatal(err)
	}
}
func (h *relationshipHarness) pump(done func() bool) {
	h.t.Helper()
	for {
		select {
		case fn := <-h.updates:
			fn()
		default:
		}
		h.tt.Frame()
		if done() {
			return
		}
		select {
		case <-h.ctx.Done():
			saveNativeScreenshot(h.t, h.tt)
			h.t.Fatalf("relationship UI timeout: %s %s", h.w.errText, h.w.relatedStatus)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
func relatedPod() *unstructured.Unstructured {
	pod := modelObject("pod-uid", "related-pod")
	controller := true
	pod.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "owner-rs", UID: "rs-uid", Controller: &controller}})
	return pod
}
func relatedReplicaSet(uid string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "apps/v1", "kind": "ReplicaSet", "metadata": map[string]interface{}{"name": "owner-rs", "namespace": "team", "uid": uid, "resourceVersion": "1"}}}
}
func attachRelationshipBackend(t *testing.T, h *relationshipHarness, host string) {
	t.Helper()
	b, err := kube.New(&rest.Config{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	h.w.backend = b
	h.w.sessionID = "relationship-ui"
	h.w.activeContext = "test-cluster"
	h.w.activeNamespace = "team"
	h.w.detail = relatedPod()
	h.w.detailKind = catalog()[0]
	h.tt.Frame()
}
func TestNativeRelationshipsNavigateAndRevalidateIdentity(t *testing.T) {
	var replaced atomic.Bool
	var mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations.Add(1)
			http.Error(w, "not read-only", 405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/namespaces/team/pods/related-pod":
			_ = json.NewEncoder(w).Encode(relatedPod())
		case "/apis/apps/v1/namespaces/team/replicasets/owner-rs":
			uid := "rs-uid"
			if replaced.Load() {
				uid = "new-rs-uid"
			}
			_ = json.NewEncoder(w).Encode(relatedReplicaSet(uid))
		case "/api/v1/namespaces/team/pods":
			_ = json.NewEncoder(w).Encode(&unstructured.UnstructuredList{Object: map[string]interface{}{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]interface{}{"resourceVersion": "1"}}, Items: []unstructured.Unstructured{*relatedPod()}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	h := newRelationshipHarness(t, 15*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.click("Related")
	h.pump(func() bool { return !h.w.relatedActive })
	if len(h.w.relatedResult.Links) != 1 || !h.w.relatedResult.Links[0].Navigable() {
		t.Fatalf("%+v %s", h.w.relatedResult, h.w.relatedStatus)
	}
	h.click("Controlled by")
	h.tt.Key(0, ui.KeyLeft)
	if _, visible := h.tt.Find("Open ReplicaSet/owner-rs"); visible {
		saveNativeScreenshot(t, h.tt)
		t.Fatal("relationship group cannot collapse with the keyboard")
	}
	h.tt.Key(0, ui.KeyRight)
	if _, visible := h.tt.Find("Open ReplicaSet/owner-rs"); !visible {
		t.Fatal("expanded relationship lost its owner")
	}
	for _, scale := range []float32{1, 2} {
		h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
		h.tt.Key(0, ui.KeyRight)
		h.tt.Key(0, ui.KeyLeft)
		tree, treeOK := h.tt.Find("Direct relationships")
		pixels := h.tt.Image()
		visible := false
		for y := int(tree.Y); y < int(tree.Y)+4; y++ {
			pixel := pixels.RGBAAt(int(tree.X+tree.W/2), y)
			if int(pixel.B) > int(pixel.R)+30 {
				visible = true
			}
		}
		if !treeOK || !visible {
			saveNativeScreenshot(t, h.tt)
			t.Fatalf("relationship top focus line clipped at scale %v: tree=%+v", scale, tree)
		}
	}
	h.tt.SetPreferences(ui.Preferences{TextScale: 1, ReduceMotion: true})
	saveNativeScreenshot(t, h.tt)
	h.tt.Key(0, ui.KeyRight)
	h.tt.Key(0, ui.KeyEnter)
	h.pump(func() bool { return h.w.detail != nil })
	if h.w.detailKind.Kind != "ReplicaSet" || h.w.currentKind.Kind != "Pod" {
		t.Fatal("related navigation corrupted the list scope")
	}
	h.click("Refresh detail")
	h.pump(func() bool { return h.w.detail != nil })
	if h.w.detailKind.Kind != "ReplicaSet" {
		t.Fatal("refresh used the list kind instead of the detail kind")
	}
	h.click("Related")
	h.pump(func() bool { return !h.w.relatedActive })
	h.click("Open Pod/related-pod")
	h.pump(func() bool { return h.w.detail != nil })
	h.click("Related")
	h.pump(func() bool { return !h.w.relatedActive })
	// A verified link must still be revalidated at the moment it is opened.
	replaced.Store(true)
	h.click("Open ReplicaSet/owner-rs")
	h.pump(func() bool { return h.w.errText != "" })
	if h.w.detail != nil {
		t.Fatal("stale link opened a replacement object")
	}
	h.w.openResourceKind(catalog()[0], project(relatedPod()))
	h.pump(func() bool { return h.w.detail != nil })
	h.click("Related")
	h.pump(func() bool { return !h.w.relatedActive })
	if len(h.w.relatedResult.Links) != 1 || h.w.relatedResult.Links[0].State != relationship.Replaced || h.w.relatedResult.Links[0].Navigable() {
		t.Fatal("stale owner was still navigable")
	}
	if mutations.Load() != 0 {
		t.Fatal("relationship navigation performed a mutation")
	}
}
func TestNativeRelationshipsDiscardQueuedResultsAfterDetailChange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "replicasets") {
			_ = json.NewEncoder(w).Encode(relatedReplicaSet("rs-uid"))
		} else {
			_ = json.NewEncoder(w).Encode(relatedPod())
		}
	}))
	defer server.Close()
	h := newRelationshipHarness(t, 10*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.click("Related")
	var completion func()
	select {
	case completion = <-h.updates:
	case <-h.ctx.Done():
		t.Fatal("relationship lookup did not finish")
	}
	h.click("Close detail")
	h.w.detail = modelObject("different-uid", "different-pod")
	h.w.detailKind = catalog()[0]
	completion()
	h.tt.Frame()
	if h.w.detail.GetUID() != "different-uid" || len(h.w.relatedResult.Links) != 0 || h.w.relatedActive || h.w.relatedStatus != "" {
		t.Fatal("old lookup contaminated a new detail")
	}
}
func TestNativeRelationshipsCloseCancelsOutstandingRead(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(canceled) }))
	defer server.Close()
	h := newRelationshipHarness(t, 10*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.click("Related")
	select {
	case <-started:
	case <-h.ctx.Done():
		t.Fatal("lookup did not start")
	}
	h.click("Close detail")
	select {
	case <-canceled:
	case <-h.ctx.Done():
		t.Fatal("detail close did not cancel the API read")
	}
	if h.w.relatedActive || h.w.relatedCancel != nil {
		t.Fatal("lookup lifecycle retained after closing")
	}
}

func (h *relationshipHarness) selectSidebarResource(label string) {
	h.t.Helper()
	nav, ok := h.tt.Find("Resource navigation")
	if !ok {
		h.t.Fatal("resource sidebar missing")
	}
	h.tt.Scroll(nav.X+nav.W/2, nav.Y+nav.H/2, 0, -100000)
	for range 100 {
		if row, ok := h.tt.Find(label); ok && row.Y >= nav.Y && row.Y+row.H <= nav.Y+nav.H {
			h.click(label)
			return
		}
		opened := false
		for group, title := range []string{"Workloads", "Network", "Configuration", "Storage", "Access", "Cluster", "Custom resources"} {
			if row, ok := h.tt.Find(title); ok && !h.w.resourceGroups[group] && row.Y >= nav.Y && row.Y+row.H <= nav.Y+nav.H {
				h.click(title)
				opened = true
				break
			}
		}
		if opened {
			h.tt.Scroll(nav.X+nav.W/2, nav.Y+nav.H/2, 0, -100000)
			continue
		}
		h.tt.Scroll(nav.X+nav.W/2, nav.Y+nav.H/2, 0, nav.H/2)
	}
	saveNativeScreenshot(h.t, h.tt)
	h.t.Fatalf("resource %q unreachable through sidebar", label)
}
