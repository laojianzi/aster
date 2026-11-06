package uiworkbench

import (
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNativeTaskNavigationPreservesEditorUndo(t *testing.T) {
	first := modelObject("first-uid", "first")
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
	h.click("Connections")
	if _, ok := h.tt.Find("Manifest editor"); ok {
		t.Fatal("hidden resource editor remains accessible")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != draft {
		t.Fatal("hidden editor received Undo")
	}
	h.click("Resources")
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
}

func TestNativeHiddenResourceTaskDoesNotShowReview(t *testing.T) {
	w := New()
	t.Cleanup(w.Close)
	w.detail = modelObject("hidden-review", "hidden-review")
	w.detailKind = catalog()[0]
	w.connectionsOpen = true
	tt := ui.NewTester(w.View, 1100, 700)
	w.detailMode = "Diff"
	tt.Frame()
	if _, ok := tt.Find("Review change"); ok {
		t.Fatal("background resource review covered Connections")
	}
	if err := tt.Click("Resources"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tt.Find("Review change"); !ok {
		t.Fatal("returning to resources lost pending review")
	}
}
