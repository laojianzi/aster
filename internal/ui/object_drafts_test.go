package uiworkbench

import (
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/operation"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeObjectNavigationRetainsDraft(t *testing.T) {
	first, second := modelObject("first-uid", "first"), modelObject("second-uid", "second")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected write: %s", r.Method)
			http.Error(w, "unexpected write", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/namespaces/team/pods/first":
			json.NewEncoder(w).Encode(first)
		case "/api/v1/namespaces/team/pods/second":
			json.NewEncoder(w).Encode(second)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	h := newRelationshipHarness(t, 10*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.w.openResourceKind(catalog()[0], project(first))
	h.pump(func() bool { return h.w.detail != nil })
	h.click("YAML")
	h.click("Edit")
	h.click("Manifest editor")
	original := h.w.editorText()
	draft := original + "\n# unsaved 工作草稿\n"
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(draft)
	saved := h.w.editor
	if h.w.editorText() != draft {
		t.Fatal("fixture did not enter the draft through native input")
	}
	h.w.openResourceKind(catalog()[0], project(second))
	h.pump(func() bool { return h.w.detail != nil })
	if h.w.detail.GetUID() != "second-uid" || h.w.plan != nil || h.w.confirmation != "" {
		t.Fatal("navigation retained approval or opened wrong object")
	}
	h.w.openResourceKind(catalog()[0], project(first))
	h.pump(func() bool { return h.w.detail != nil })
	if h.w.editorText() != draft || h.w.editor != saved {
		saveNativeScreenshot(t, h.tt)
		t.Fatal("returning to a verified object discarded its draft buffer")
	}
}

func TestNativeObjectDraftIdentityAndRefresh(t *testing.T) {
	for _, scenario := range []string{"refresh", "replacement", "new-version", "disconnect", "scope"} {
		t.Run(scenario, func(t *testing.T) {
			first, second := modelObject("first-uid", "first"), modelObject("second-uid", "second")
			var generation atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("unexpected write %s", r.Method)
					http.Error(w, "unexpected", 400)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				obj := second.DeepCopy()
				if r.URL.Path == "/api/v1/namespaces/team/pods/first" {
					obj = first.DeepCopy()
					if generation.Load() > 0 {
						if scenario == "replacement" {
							obj.SetUID("replacement-uid")
						} else {
							obj.SetResourceVersion("2")
						}
					}
				}
				json.NewEncoder(w).Encode(obj)
			}))
			t.Cleanup(server.Close)
			h := newRelationshipHarness(t, 10*time.Second)
			attachRelationshipBackend(t, h, server.URL)
			h.w.openResourceKind(catalog()[0], project(first))
			h.pump(func() bool { return h.w.detail != nil })
			h.click("YAML")
			h.click("Edit")
			h.click("Manifest editor")
			draft := h.w.editorText() + "\n# retained draft\n"
			h.tt.Key(ui.Cmd, ui.KeyA)
			h.tt.Type(draft)
			saved := h.w.editor
			h.w.openResourceKind(catalog()[0], project(second))
			h.pump(func() bool { return h.w.detail != nil })
			if scenario == "disconnect" {
				h.w.disconnect()
				if len(h.w.objectDrafts) != 0 {
					t.Fatal("disconnect retained drafts")
				}
				return
			}
			if scenario == "scope" {
				h.w.backend = nil
				h.w.namespace = "other"
				h.w.startScope()
				if len(h.w.objectDrafts) != 0 {
					t.Fatal("scope change retained drafts")
				}
				return
			}
			if scenario == "replacement" || scenario == "new-version" {
				generation.Store(1)
			}
			h.w.openResourceKind(catalog()[0], project(first))
			h.pump(func() bool { return h.w.detail != nil || h.w.errText != "" })
			if scenario == "replacement" {
				if h.w.detail != nil || h.w.plan != nil {
					t.Fatal("replaced UID accepted old draft")
				}
				replacement := first.DeepCopy()
				replacement.SetUID("replacement-uid")
				h.w.openResourceKind(catalog()[0], project(replacement))
				h.pump(func() bool { return h.w.detail != nil })
				if h.w.editor == saved || h.w.editorText() == draft {
					t.Fatal("draft transferred to replacement UID")
				}
				return
			}
			if h.w.editor != saved || h.w.editorText() != draft {
				t.Fatal("verified return discarded draft")
			}
			if h.w.plan != nil || h.w.confirmation != "" || h.w.applyAcknowledged {
				t.Fatal("restored draft retained approval")
			}
			if scenario == "new-version" {
				h.w.ops = operation.NewService(h.w.backend, h.w.sessionID)
				h.click("Preview edit")
				h.pump(func() bool { return !h.w.preparing })
				if h.w.plan != nil || !strings.Contains(h.w.detailMessage, operation.ErrConflict.Error()) {
					t.Fatal("old draft RV did not conflict against new server RV")
				}
				return
			}
			h.click("Refresh detail")
			h.pump(func() bool { return h.w.detail != nil })
			if h.w.editor == saved || h.w.editorText() == draft {
				t.Fatal("explicit refresh did not discard draft")
			}
		})
	}
}

func TestNativeObjectTabsNavigateCloseAndKeepDraft(t *testing.T) {
	first, second := modelObject("first-uid", "first"), modelObject("second-uid", "second")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected write %s", r.Method)
			http.Error(w, "unexpected", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/namespaces/team/pods/first" {
			json.NewEncoder(w).Encode(first)
		} else {
			json.NewEncoder(w).Encode(second)
		}
	}))
	t.Cleanup(server.Close)
	h := newRelationshipHarness(t, 10*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.w.clearDetail()
	h.w.rows = []resourceRow{project(first), project(second)}
	h.tt.Frame()
	h.click("first")
	h.pump(func() bool { return h.w.detail != nil })
	h.click("YAML")
	h.click("Edit")
	h.click("Manifest editor")
	draft := h.w.editorText() + "\n# tab draft\n"
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(draft)
	h.click("Resource list")
	if h.w.detail != nil {
		t.Fatal("list tab retained object view")
	}
	h.click("second")
	h.pump(func() bool { return h.w.detail != nil })
	h.click("Pod v1 / team/first")
	h.pump(func() bool { return h.w.detail != nil && h.w.detail.GetUID() == "first-uid" })
	if h.w.editorText() != draft || h.w.plan != nil {
		t.Fatal("object tab lost draft or retained approval")
	}
	saveNativeScreenshot(t, h.tt)
	h.click("Close detail")
	if _, ok := h.tt.Find("Pod v1 / team/first"); ok {
		t.Fatal("closed object tab remains")
	}
	h.click("Pod v1 / team/second")
	h.pump(func() bool { return h.w.detail != nil })
	if h.w.detail.GetUID() != "second-uid" {
		t.Fatal("closing one tab removed another")
	}
	h.click("Close detail")
	if _, ok := h.tt.Find("Open objects"); ok {
		t.Fatal("empty tab strip remains")
	}
}

func TestNativeObjectTabsScopeAndKeyboard(t *testing.T) {
	first, second := modelObject("first-uid", "first"), modelObject("second-uid", "second")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected write %s", r.Method)
			http.Error(w, "unexpected", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/pods/first"):
			json.NewEncoder(w).Encode(first)
		case strings.HasSuffix(r.URL.Path, "/pods/second"):
			json.NewEncoder(w).Encode(second)
		case strings.HasSuffix(r.URL.Path, "/configmaps"):
			if r.URL.Query().Get("watch") == "true" {
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				return
			}
			w.Write([]byte(`{"apiVersion":"v1","kind":"ConfigMapList","metadata":{"resourceVersion":"1"},"items":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	h := newRelationshipHarness(t, 10*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.w.namespace = "team"
	h.w.openResourceKind(catalog()[0], project(first))
	h.pump(func() bool { return h.w.detail != nil })
	h.click("YAML")
	h.click("Edit")
	h.click("Manifest editor")
	draft := h.w.editorText() + "\n# preserved across resource kinds\n"
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(draft)
	h.w.openResourceKind(catalog()[0], project(second))
	h.pump(func() bool { return h.w.detail != nil })
	h.selectSidebarResource("ConfigMaps")
	h.pump(func() bool { return h.w.status == "Live" })
	h.tt.SetPreferences(ui.Preferences{TextScale: 2, ReduceMotion: true})
	h.tt.Frame()
	h.click("Resource list")
	h.tt.Key(0, ui.KeyEnd)
	h.pump(func() bool { return h.w.detail != nil })
	if h.w.detail.GetUID() != "second-uid" {
		t.Fatal("End did not open the last object tab")
	}
	h.tt.Key(0, ui.KeyLeft)
	h.pump(func() bool { return h.w.detail != nil && h.w.detail.GetUID() == "first-uid" })
	if h.w.editorText() != draft {
		t.Fatal("resource kind navigation discarded draft")
	}
	saveNativeScreenshot(t, h.tt)
	h.w.namespace = "other"
	h.w.startScope()
	h.tt.Frame()
	if _, ok := h.tt.Find("Open objects"); ok {
		t.Fatal("namespace change retained object tabs")
	}
	if len(h.w.objectDrafts) != 0 {
		t.Fatal("namespace change retained drafts")
	}
}

func TestNativeObjectFailedTabRetryAndClose(t *testing.T) {
	first, second := modelObject("first-uid", "first"), modelObject("second-uid", "second")
	var unavailable atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected write %s", r.Method)
			http.Error(w, "unexpected", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/first") {
			if unavailable.Load() {
				http.Error(w, "object unavailable", http.StatusForbidden)
				return
			}
			json.NewEncoder(w).Encode(first)
		} else {
			json.NewEncoder(w).Encode(second)
		}
	}))
	t.Cleanup(server.Close)
	h := newRelationshipHarness(t, 10*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.w.openResourceKind(catalog()[0], project(first))
	h.pump(func() bool { return h.w.detail != nil })
	h.click("YAML")
	h.click("Edit")
	h.click("Manifest editor")
	draft := h.w.editorText() + "\n# keep after retry\n"
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(draft)
	h.w.openResourceKind(catalog()[0], project(second))
	h.pump(func() bool { return h.w.detail != nil })
	unavailable.Store(true)
	h.click("Pod v1 / team/first")
	h.pump(func() bool { return h.w.errText != "" })
	if h.w.detail != nil || h.w.plan != nil {
		t.Fatal("failed load exposed stale object or approval")
	}
	if _, ok := h.tt.Find("Retry object"); !ok {
		t.Fatal("failed object has no retry action")
	}
	saveNativeScreenshot(t, h.tt)
	unavailable.Store(false)
	h.click("Retry object")
	h.pump(func() bool { return h.w.detail != nil })
	if h.w.editorText() != draft {
		t.Fatal("retry discarded original draft")
	}
	h.click("Pod v1 / team/second")
	h.pump(func() bool { return h.w.detail != nil })
	unavailable.Store(true)
	h.click("Pod v1 / team/first")
	h.pump(func() bool { return h.w.errText != "" })
	h.click("Close detail")
	if h.w.errText != "" {
		t.Fatal("closed object error leaked into resource list")
	}
	if _, ok := h.tt.Find("Pod v1 / team/first"); ok {
		t.Fatal("failed tab not closed")
	}
	h.click("Pod v1 / team/second")
	h.pump(func() bool { return h.w.detail != nil })
	if h.w.detail.GetUID() != "second-uid" {
		t.Fatal("closing failed tab removed another object")
	}
}

func TestNativeObjectTabsOverflowKeyboard(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprint(scale), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("unexpected write %s", r.Method)
					http.Error(w, "unexpected", 400)
					return
				}
				parts := strings.Split(r.URL.Path, "/")
				name := parts[len(parts)-1]
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(modelObject(name+"-uid", name))
			}))
			t.Cleanup(server.Close)
			h := newRelationshipHarness(t, 15*time.Second)
			attachRelationshipBackend(t, h, server.URL)
			h.tt = ui.NewTester(h.w.View, 1100, 700)
			h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			for i := range 12 {
				name := fmt.Sprintf("service-%02d-production", i)
				h.w.openResourceKind(catalog()[0], project(modelObject(name+"-uid", name)))
				h.pump(func() bool { return h.w.detail != nil })
			}
			// Use the first tab through keyboard Home even when the strip has scrolled.
			h.tt.Frame()
			lastLabel := "Pod v1 / team/service-11-production"
			r, ok := h.tt.Find(lastLabel)
			if !ok || r.X < 0 || r.X+r.W > 1100 {
				saveNativeScreenshot(t, h.tt)
				t.Fatalf("new active tab is outside the visible workspace: found=%v rect=%+v", ok, r)
			}
			h.click(lastLabel)
			h.tt.Key(0, ui.KeyHome)
			h.tt.Frame()
			if h.w.detail != nil {
				t.Fatal("Home did not return to resource list")
			}
			h.tt.Key(0, ui.KeyEnd)
			h.pump(func() bool { return h.w.detail != nil })
			if h.w.detail.GetName() != "service-11-production" {
				t.Fatal("End selected the wrong object")
			}
			r, ok = h.tt.Find(lastLabel)
			if !ok || r.X < 0 || r.X+r.W > 1100 {
				t.Fatal("keyboard-selected tab is outside the visible workspace")
			}
			h.tt.Key(0, ui.KeyLeft)
			h.pump(func() bool { return h.w.detail != nil && h.w.detail.GetName() == "service-10-production" })
			saveNativeScreenshot(t, h.tt)
		})
	}
}
