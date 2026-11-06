package uiworkbench

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestNativeAttentionFiltersLiveHealthAndPreservesSearch(t *testing.T) {
	ready := modelObject("ready-uid", "ready-pod")
	unstructured.SetNestedSlice(ready.Object, []any{map[string]any{"type": "Ready", "status": "True"}}, "status", "conditions")
	pending := modelObject("pending-uid", "pending-pod")
	unknown := modelObject("unknown-uid", "opaque-pod")
	unknown.Object["status"] = map[string]any{"phase": int64(123)}
	updates := make(chan *unstructured.Unstructured, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("unexpected mutation: %s", r.Method)
			http.Error(w, "read only", 405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("watch") == "true" {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			for {
				select {
				case <-r.Context().Done():
					return
				case obj := <-updates:
					json.NewEncoder(w).Encode(map[string]any{"type": "MODIFIED", "object": obj})
					w.(http.Flusher).Flush()
				}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "1"}, "items": []any{ready, pending, unknown}})
	}))
	t.Cleanup(server.Close)
	h := newRelationshipHarness(t, 20*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.w.namespace = "team"
	h.w.startScope()
	h.pump(func() bool { return h.w.status == "Live" && len(h.w.rows) == 3 })
	h.click("Needs attention")
	h.pump(func() bool { return len(h.w.rows) == 2 })
	for _, row := range h.w.rows {
		if row.UID == "ready-uid" {
			t.Fatal("Ready Pod remained in attention queue")
		}
	}
	h.click("Filter resources")
	h.tt.Type("unknown")
	h.pump(func() bool { return len(h.w.rows) == 1 })
	if h.w.rows[0].UID != "unknown-uid" {
		t.Fatal("unknown health was silently treated as healthy")
	}
	h.click("All")
	h.pump(func() bool { return len(h.w.rows) == 1 })
	if h.w.filter != "unknown" {
		t.Fatal("health filter discarded search")
	}
	h.click("Filter resources")
	h.tt.Key(0, ui.KeyEscape)
	h.pump(func() bool { return len(h.w.rows) == 3 })
	h.click("Needs attention")
	h.pump(func() bool { return len(h.w.rows) == 2 })
	recovered := pending.DeepCopy()
	recovered.SetResourceVersion("2")
	unstructured.SetNestedSlice(recovered.Object, []any{map[string]any{"type": "Ready", "status": "True"}}, "status", "conditions")
	updates <- recovered
	h.pump(func() bool { return len(h.w.rows) == 1 })
	if h.w.rows[0].UID != "unknown-uid" || h.w.total != 3 {
		t.Fatal("watch recovery lost total or kept recovered Pod")
	}
	failed := modelObject("failed-uid", "failed-pod")
	failed.Object["status"] = map[string]any{"phase": "Failed"}
	failed.SetResourceVersion("3")
	degraded := modelObject("degraded-uid", "image-pod")
	degraded.Object["status"] = map[string]any{"phase": "Pending", "containerStatuses": []any{map[string]any{"name": "app", "state": map[string]any{"waiting": map[string]any{"reason": "ImagePullBackOff"}}}}}
	degraded.SetResourceVersion("4")
	updates <- failed
	updates <- degraded
	h.pump(func() bool { return len(h.w.rows) == 3 })
	h.click("Sort resources")
	h.tt.Key(0, ui.KeyDown)
	h.tt.Key(0, ui.KeyDown)
	h.tt.Key(0, ui.KeyEnter)
	h.pump(func() bool { return h.w.sortBy == "Status" && h.w.rows[0].UID == "degraded-uid" })
	if h.w.rows[1].UID != "failed-uid" || h.w.rows[2].UID != "unknown-uid" {
		t.Fatal("Status ordering does not match displayed health")
	}
	h.tt.SetSize(1100, 700)
	h.tt.SetPreferences(ui.Preferences{TextScale: 2, ReduceMotion: true})
	for _, label := range []string{"All", "Needs attention", "Filter resources"} {
		r, ok := h.tt.Find(label)
		if !ok || r.X+r.W > 1100 || r.Y+r.H > 700 {
			t.Fatalf("unreachable filter %s: %+v", label, r)
		}
	}
	saveNativeScreenshot(t, h.tt)
}

func TestNativeFocusQueueKeepsIdentityHistoryAndLiveRecovery(t *testing.T) {
	pending := modelObject("pending-uid", "pending-pod")
	next := modelObject("next-uid", "next-pod")
	ready := modelObject("ready-uid", "ready-pod")
	unstructured.SetNestedSlice(ready.Object, []any{map[string]any{"type": "Ready", "status": "True"}}, "status", "conditions")
	stale := modelObject("stale-uid", "stale-pod")
	updates := make(chan *unstructured.Unstructured, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected write: %s", r.Method)
			http.Error(w, "read only", 405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/namespaces/team/pods", "/api/v1/pods":
			if r.URL.Query().Get("watch") == "true" {
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				for {
					select {
					case <-r.Context().Done():
						return
					case obj := <-updates:
						json.NewEncoder(w).Encode(map[string]any{"type": "MODIFIED", "object": obj})
						w.(http.Flusher).Flush()
					}
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "1"}, "items": []any{pending, next, ready, stale}})
		case "/api/v1/namespaces/team/pods/pending-pod":
			json.NewEncoder(w).Encode(pending)
		case "/api/v1/namespaces/team/pods/next-pod":
			json.NewEncoder(w).Encode(next)
		case "/api/v1/namespaces/team/pods/stale-pod":
			json.NewEncoder(w).Encode(modelObject("replacement-uid", "stale-pod"))
		default:
			t.Errorf("unexpected request %s", r.URL)
			http.Error(w, "unexpected", 400)
		}
	}))
	t.Cleanup(server.Close)
	h := newRelationshipHarness(t, 30*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.w.namespace = "team"
	h.w.startScope()
	h.pump(func() bool { return h.w.status == "Live" && len(h.w.rows) == 4 })
	h.click("Needs attention")
	h.pump(func() bool { return len(h.w.rows) == 3 })
	h.click("Focus diagnosis")
	if _, ok := h.tt.Find("pending-pod"); !ok {
		t.Fatal("single-namespace queue repeats scope instead of giving the name its space")
	}
	if _, ok := h.tt.Find("Resource navigation"); ok {
		t.Fatal("focus mode kept the resource catalog visible")
	}
	if _, ok := h.tt.Find("Diagnose team/ready-pod"); ok {
		t.Fatal("healthy resource entered diagnosis queue")
	}
	h.click("Diagnose team/pending-pod")
	h.pump(func() bool { return h.w.detail != nil && h.w.detail.GetUID() == "pending-uid" })
	h.click("Diagnostics")
	h.click("Logs")
	h.click("Diagnose team/next-pod")
	h.pump(func() bool { return h.w.detail != nil && h.w.detail.GetUID() == "next-uid" })
	if h.w.diagnostic == nil || h.w.diagnostic.target.UID != "pending-uid" {
		t.Fatal("queue navigation retargeted the log session")
	}
	h.click("Close logs")
	h.click("Diagnose team/stale-pod")
	h.pump(func() bool { return h.w.objectLoadError != "" })
	if h.w.detail != nil {
		t.Fatal("queue admitted an object whose UID changed")
	}
	h.click("Diagnose team/next-pod")
	h.pump(func() bool { return h.w.detail != nil })
	for _, obj := range []*unstructured.Unstructured{pending, next, stale} {
		recovered := obj.DeepCopy()
		recovered.SetResourceVersion("2")
		unstructured.SetNestedSlice(recovered.Object, []any{map[string]any{"type": "Ready", "status": "True"}}, "status", "conditions")
		updates <- recovered
	}
	h.pump(func() bool { return len(h.w.rows) == 0 })
	if _, ok := h.tt.Find("No resources need attention"); !ok || h.w.detail.GetUID() != "next-uid" {
		t.Fatal("live recovery lost the object or left a stale queue")
	}
	for _, scale := range []float32{1, 2} {
		h.tt.SetSize(1100, 700)
		h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
		queue, ok := h.tt.Find("Diagnosis queue")
		object, exists := h.tt.Find("Object workspace")
		if !ok || !exists || queue.W < 200 || object.W < 400 || queue.X+queue.W > object.X || object.X+object.W > 1100 {
			t.Fatalf("focus layout clipped: queue=%+v object=%+v", queue, object)
		}
	}
	saveNativeScreenshot(t, h.tt)
	h.tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	h.click("YAML")
	h.click("Edit")
	h.click("Manifest editor")
	original := h.w.editorText()
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(original + "\n# focus draft\n")
	h.click("Back to resources")
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != original {
		t.Fatal("leaving focus mode lost native Undo")
	}
	h.w.namespace = "*"
	h.w.startScope()
	h.pump(func() bool { return h.w.status == "Live" && len(h.w.rows) == 3 })
	h.click("Focus diagnosis")
	if _, ok := h.tt.Find("team/pending-pod"); !ok {
		t.Fatal("cross-namespace queue hid resource scope")
	}
	h.click("Disconnect")
	if _, ok := h.tt.Find("Diagnosis queue"); ok {
		t.Fatal("disconnect retained the old diagnosis queue")
	}
}

func TestNativeFocusQueueDoesNotPresentReadFailureAsHealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden fixture", http.StatusForbidden)
	}))
	t.Cleanup(server.Close)
	h := newRelationshipHarness(t, 15*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.w.namespace = "team"
	h.w.startScope()
	h.pump(func() bool { return h.w.status == "Unavailable" })
	h.click("Needs attention")
	h.click("Focus diagnosis")
	if _, ok := h.tt.Find("Diagnosis unavailable"); !ok {
		t.Fatal("failed list has no unavailable state")
	}
	if _, ok := h.tt.Find("No resources need attention"); ok {
		t.Fatal("forbidden list was presented as healthy")
	}
	if _, ok := h.tt.Find("Refresh diagnosis"); !ok {
		t.Fatal("failed diagnosis has no retry")
	}
	saveNativeScreenshot(t, h.tt)
}
