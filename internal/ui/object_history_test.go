package uiworkbench

import (
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeUndoAcrossNavigation(t *testing.T) {
	for _, route := range []string{"object-tabs", "resource-list", "connections", "diagnostic-toggle"} {
		t.Run(route, func(t *testing.T) {
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
			switch route {
			case "object-tabs":
				h.w.openResourceKind(catalog()[0], project(second))
				h.pump(func() bool { return h.w.detail != nil })
				if h.w.detail.GetUID() != "second-uid" || h.w.plan != nil || h.w.confirmation != "" {
					t.Fatal("navigation retained approval or opened wrong object")
				}
				h.w.openResourceKind(catalog()[0], project(first))
				h.pump(func() bool { return h.w.detail != nil })
			case "resource-list":
				h.click("Resource list")
				h.w.openResourceKind(catalog()[0], project(first))
				h.pump(func() bool { return h.w.detail != nil })
			case "connections":
				h.click("Connections")
				h.click("Resources")
			case "diagnostic-toggle":
				h.click("Diagnostics")
				if err := h.tt.Click("Logs"); err != nil {
					t.Fatal(err)
				}
				h.tt.Frame()
				h.click("Close logs")
			}
			if h.w.detailMode != "Edit" {
				h.click("YAML")
				h.click("Edit")
			}
			if h.w.editorText() != draft || h.w.editor != saved {
				saveNativeScreenshot(t, h.tt)
				t.Fatal("returning to a verified object discarded its draft buffer")
			}

			h.click("Manifest editor")
			h.tt.Key(ui.Cmd, ui.KeyZ)
			if h.w.editorText() != original {
				t.Fatal("returning to the object did not retain its native Undo history")
			}
		})
	}
}

func TestNativeRedoAfterReturningToCleanDraft(t *testing.T) {
	for _, scenario := range []string{"list", "object-tabs", "new-version", "refresh", "close"} {
		t.Run(scenario, func(t *testing.T) {
			first, second := modelObject("first-uid", "first"), modelObject("second-uid", "second")
			var version atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("unexpected write: %s", r.Method)
					http.Error(w, "unexpected write", 400)
					return
				}
				obj := second.DeepCopy()
				if r.URL.Path == "/api/v1/namespaces/team/pods/first" {
					obj = first.DeepCopy()
					if version.Load() > 0 {
						obj.SetResourceVersion("2")
					}
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(obj)
			}))
			t.Cleanup(server.Close)
			h := newRelationshipHarness(t, 15*time.Second)
			attachRelationshipBackend(t, h, server.URL)
			h.w.openResourceKind(catalog()[0], project(first))
			h.pump(func() bool { return h.w.detail != nil })
			h.click("YAML")
			h.click("Edit")
			h.click("Manifest editor")
			original := h.w.editorText()
			draft := original + "\n# redo across navigation\n"
			h.tt.Key(ui.Cmd, ui.KeyA)
			h.tt.Type(draft)
			h.tt.Key(ui.Cmd, ui.KeyZ)
			if h.w.editorText() != original {
				t.Fatal("fixture Undo did not restore clean text")
			}
			saved := h.w.editor
			switch scenario {
			case "refresh":
				h.click("Refresh detail")
			case "close":
				h.click("Close detail")
				h.w.openResourceKind(catalog()[0], project(first))
			default:
				if scenario == "object-tabs" {
					h.w.openResourceKind(catalog()[0], project(second))
					h.pump(func() bool { return h.w.detail != nil })
				} else {
					h.click("Resource list")
				}
				if scenario == "new-version" {
					version.Store(1)
				}
				h.w.openResourceKind(catalog()[0], project(first))
			}
			h.pump(func() bool { return h.w.detail != nil })
			if h.w.detailMode != "Edit" {
				h.click("YAML")
				h.click("Edit")
			}
			fresh := h.w.editorText()
			h.click("Manifest editor")
			h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
			if scenario == "list" || scenario == "object-tabs" {
				if h.w.editor != saved || h.w.editorText() != draft {
					t.Fatal("clean draft lost native Redo on return")
				}
			} else {
				if h.w.editor == saved || h.w.editorText() != fresh {
					t.Fatal("invalidated clean history was restored")
				}
				if scenario == "new-version" && (h.w.detail.GetResourceVersion() != "2" || fresh == original) {
					t.Fatal("clean buffer concealed the new server version")
				}
			}
			if h.w.plan != nil || h.w.confirmation != "" {
				t.Fatal("return restored mutation approval")
			}
		})
	}
}
