package uiworkbench

import (
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNativeDiagnosticTogglePreservesEditorUndo(t *testing.T) {
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
	before, ok := h.tt.Find("Manifest editor")
	if !ok {
		t.Fatal("editor not visible before diagnostics")
	}
	h.click("Diagnostics")
	if err := h.tt.Click("Logs"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	dockBefore, _ := h.tt.Find("Log dock")
	share := h.w.diagnosticOverviewPercent
	h.click("Expand diagnostic")
	dockExpanded, ok := h.tt.Find("Log dock")
	if !ok || dockExpanded.H < dockBefore.H+50 {
		t.Fatalf("diagnostic did not expand: before=%+v after=%+v", dockBefore, dockExpanded)
	}
	if _, ok := h.tt.Find("Divider"); ok {
		t.Fatal("expanded diagnostic retains the hidden divider")
	}
	h.click("Restore split")
	dockRestored, _ := h.tt.Find("Log dock")
	if dockRestored.H != dockBefore.H || h.w.diagnosticOverviewPercent != share {
		t.Fatal("restoring diagnostic changed the split")
	}
	h.click("Close logs")
	if _, ok := h.tt.Find("Divider"); ok {
		t.Fatal("closed diagnostic retains a focusable divider")
	}
	if h.w.detailMode != "Edit" {
		h.click("YAML")
		h.click("Edit")
	}
	if h.w.editorText() != draft || h.w.editor != saved {
		saveNativeScreenshot(t, h.tt)
		t.Fatal("returning to a verified object discarded its draft buffer")
	}

	after, ok := h.tt.Find("Manifest editor")
	if !ok || after.H < before.H-1 {
		t.Fatalf("closing dock did not restore editor space: before=%+v after=%+v", before, after)
	}
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != original {
		t.Fatal("returning to the object did not retain its native Undo history")
	}
}

func TestNativeDiagnosticDividerPointerResize(t *testing.T) {
	w := New()
	t.Cleanup(w.Close)
	w.detail, w.detailKind, w.detailMode = modelObject("drag-uid", "payments"), catalog()[0], "Logs"
	w.containers = []string{"app"}
	w.logRows = []string{"log line"}
	tt := ui.NewTester(w.View, 1100, 700)
	dock, ok := tt.Find("Log dock")
	if !ok {
		t.Fatal("missing dock")
	}
	divider, ok := tt.Find("Divider")
	if !ok {
		t.Fatal("missing divider")
	}
	x, y := divider.X+divider.W/2, divider.Y+divider.H/2
	tt.Press(x, y)
	tt.Move(x, y+50)
	tt.Release(x, y+50)
	tt.Frame()
	resized, ok := tt.Find("Log dock")
	if !ok || resized.H > dock.H-20 {
		t.Fatalf("pointer did not resize dock: before=%+v after=%+v", dock, resized)
	}
	saveNativeScreenshot(t, tt)
}
