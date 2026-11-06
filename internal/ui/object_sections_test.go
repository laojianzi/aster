package uiworkbench

import (
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNativeObjectSectionNavigationPreservesUndo(t *testing.T) {
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

	h.click("Overview")
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != draft {
		t.Fatal("hidden editor received Undo outside Edit")
	}
	h.click("YAML")
	h.click("Read-only")
	h.click("YAML")
	h.click("Edit")
	if h.w.editor != saved || h.w.editorText() != draft {
		t.Fatal("section navigation discarded draft")
	}

	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != original {
		t.Fatal("section navigation did not retain native Undo history")
	}
	saveNativeScreenshot(t, h.tt)
}
