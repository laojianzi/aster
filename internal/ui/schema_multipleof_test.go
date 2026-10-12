package uiworkbench

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/testschema"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Keep the divisor as a JSON token. A float64 fixture would erase the
// distinction between the adjacent large integer drafts before inspection.
const nativeMultipleOfDocument = `{
 "openapi":"3.0.0","components":{"schemas":{
  "Pod":{"type":"object","x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"Pod"}],
   "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},
    "metadata":{"type":"object","additionalProperties":true},
    "spec":{"type":"object","properties":{
     "containers":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"}}}},
     "count":{"type":"integer","multipleOf":9007199254740993},
     "fraction":{"type":"number","multipleOf":0.1}
    }}
   }}
 }}}
`

const nativeMultipleOfIncompleteDocument = `{
 "openapi":"3.0.0","components":{"schemas":{
  "Pod":{"type":"object","x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"Pod"}],
   "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},
    "metadata":{"type":"object","additionalProperties":true},
    "spec":{"type":"object","properties":{
     "containers":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"}}}},
     "checked":{"type":"number","multipleOf":7919},
     "malformed":{"type":"number","multipleOf":%s}
    }}
   }}
 }}}
`

func nativeMultipleOfHarness(t *testing.T, document string) (*relationshipHarness, *atomic.Int32) {
	t.Helper()
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
			http.Error(w, "read-only fixture", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/openapi/v3":
			fmt.Fprint(w, testschema.Index)
		case "/openapi/v3/api/v1":
			fmt.Fprint(w, document)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return schemaHarness(t, server.URL), &writes
}

// Return by keyboard for subsequent Undo/Redo. MyGo selects all on Tab focus;
// this helper is deliberately not used to measure a prior source selection.
func focusMultipleOfEditorByKeyboard(h *relationshipHarness) {
	h.t.Helper()
	for range 64 {
		if h.tt.Focused("Manifest editor") {
			return
		}
		h.tt.Key(ui.Shift, ui.KeyTab)
	}
	h.t.Fatal("keyboard traversal did not return to the mounted manifest editor")
}

func TestNativeSchemaMultipleOfPreservesNumbersAndHistory(t *testing.T) {
	h, writes := nativeMultipleOfHarness(t, nativeMultipleOfDocument)
	buffer, original := h.w.editor, h.w.editor.Snapshot()
	const selected = "中文 🚢\n# café\n"
	before := "# " + selected + schemaDraft + "  count: 9007199254740992\n  fraction: 0.3\n"
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(before)
	snapshot, identity := h.w.editor.Snapshot(), h.w.draftIdentity()
	// The selected range crosses a line and includes a non-BMP character.
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Key(0, ui.KeyLeft)
	for range 2 {
		h.tt.Key(0, ui.KeyRight)
	}
	for range 12 {
		h.tt.Key(ui.Shift, ui.KeyRight)
	}
	h.tt.Key(ui.Cmd, ui.KeyC)
	if h.tt.Clipboard() != selected {
		t.Fatal("native selection did not cover the intended Unicode source range")
	}
	selectedCaret, caretOK := h.tt.TextCaret()
	editorBefore, editorOK := h.tt.Find("Manifest editor")
	if !caretOK || !editorOK {
		t.Fatal("selected native source has no visible caret or editor")
	}
	h.tt.SetClipboard("selection was not copied after inspection")
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/count · multipleOf") || len(h.w.schemaLocations) != 1 || strings.Contains(h.w.schemaText, "Inspection is incomplete") {
		t.Fatal("exact large-integer mismatch was missed or the valid decimal was rejected")
	}
	// Right-click at the previously observed selected endpoint. A native text
	// menu preserves an existing selection when the press is inside its range;
	// returning with Tab would explicitly select all, independently of Check.
	editorAfter, editorOK := h.tt.Find("Manifest editor")
	x := editorAfter.X + selectedCaret.X - editorBefore.X + 0.25
	y := editorAfter.Y + selectedCaret.Y - editorBefore.Y + selectedCaret.H/2
	if !editorOK || x < editorAfter.X || x >= editorAfter.X+editorAfter.W || y < editorAfter.Y || y >= editorAfter.Y+editorAfter.H {
		t.Fatal("inspection did not retain a visible selected endpoint")
	}
	h.tt.RightClickAt(x, y)
	if err := h.tt.ChooseMenuItem("Copy"); err != nil {
		t.Fatal(err)
	}
	if h.tt.Clipboard() != selected || h.w.editor != buffer || h.w.editorText() != before || h.w.draftIdentity() != identity || snapshot.String() != before || original.String() != schemaDraft || h.w.plan != nil || writes.Load() != 0 {
		t.Fatalf("multipleOf inspection invariants: selection=%v clipboardBytes=%d buffer=%v input=%v identity=%v snapshot=%v original=%v planNil=%v writes=%d", h.tt.Clipboard() == selected, len(h.tt.Clipboard()), h.w.editor == buffer, h.w.editorText() == before, h.w.draftIdentity() == identity, snapshot.String() == before, original.String() == schemaDraft, h.w.plan == nil, writes.Load())
	}
	target := chooseDiagnostic(h, "/spec/count")
	if !target.position.Exact || !strings.HasPrefix(string([]rune(before)[target.position.RuneOffset:]), "count:") {
		t.Fatal("multipleOf diagnostic lost its original Unicode source coordinate")
	}
	h.click("Go to location")
	if !h.tt.Focused("Manifest editor") || h.w.editorText() != before || h.w.plan != nil {
		t.Fatal("multipleOf navigation changed input or did not focus its actual location")
	}
	saveNativeScreenshot(t, h.tt)
	h.tt.Key(ui.Shift, ui.KeyEnd)
	h.tt.Type("count: 9007199254740993")
	corrected := h.w.editorText()
	obj, err := manifest.Decode([]byte(corrected))
	if err != nil {
		t.Fatal(err)
	}
	count, found, err := unstructured.NestedInt64(obj.Object, "spec", "count")
	if err != nil || !found || count != 9007199254740993 {
		t.Fatal("native source correction lost the integer above 2^53")
	}
	fraction, found, err := unstructured.NestedFloat64(obj.Object, "spec", "fraction")
	encoded, encodeErr := json.Marshal(fraction)
	if err != nil || !found || encodeErr != nil || string(encoded) != "0.3" {
		t.Fatal("test did not use the real decoder's shortest JSON decimal contract")
	}
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if len(h.w.schemaLocations) != 0 || !strings.Contains(h.w.schemaText, "No supported structural or constraint hints found.") || strings.Contains(h.w.schemaText, "Inspection is incomplete") || h.w.editorText() != corrected || h.w.plan != nil {
		t.Fatal("equal large divisor or shortest-JSON 0.3/0.1 was not accepted exactly")
	}
	focusMultipleOfEditorByKeyboard(h)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != before {
		t.Fatal("inspection reset Undo of the source correction")
	}
	h.tt.SetClipboard("correction selection was not restored")
	h.tt.Key(ui.Cmd, ui.KeyC)
	if h.tt.Clipboard() != "count: 9007199254740992" {
		t.Fatal("Undo did not restore the original corrected-line selection")
	}
	h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if h.w.editorText() != corrected {
		t.Fatal("inspection reset Redo of the source correction")
	}
	neighbour := strings.Replace(corrected, "count: 9007199254740993", "count: 9007199254740994", 1)
	neighbour = strings.Replace(neighbour, "fraction: 0.3", "fraction: 0.31", 1)
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(neighbour)
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/count · multipleOf") || !strings.Contains(h.w.schemaText, "/spec/fraction · multipleOf") || len(h.w.schemaLocations) != 2 || h.w.editorText() != neighbour || h.w.plan != nil {
		t.Fatal("adjacent integer or inexact decimal was rounded into a multiple")
	}
	focusMultipleOfEditorByKeyboard(h)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != corrected {
		t.Fatal("checking the second draft broke its Undo")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != before {
		t.Fatal("second inspection discarded the preceding correction history")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != schemaDraft || h.w.editor != buffer || original.String() != schemaDraft || snapshot.String() != before || h.w.plan != nil || writes.Load() != 0 {
		t.Fatal("multipleOf inspection discarded original history or mutated retained state")
	}
}

func TestNativeSchemaMultipleOfMinimumWindowAndIncomplete(t *testing.T) {
	for _, tc := range []struct {
		name, divisor, value string
		wantType             bool
	}{
		{"string-divisor", `"PRIVATE_SCHEMA_DIVISOR"`, "123456789", false},
		{"type-and-malformed-divisor", `"PRIVATE_SCHEMA_DIVISOR"`, "PRIVATE_DRAFT_VALUE", true},
		{"zero-divisor", "0", "123456789", false},
		{"exponent-outside-budget", "1e2147483648", "123456789", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, writes := nativeMultipleOfHarness(t, fmt.Sprintf(nativeMultipleOfIncompleteDocument, tc.divisor))
			h.tt.SetSize(1100, 700)
			buffer, original := h.w.editor, h.w.editor.Snapshot()
			draft := "# 中文 🚢 é — private operands stay in the editor\n" + schemaDraft + "  checked: 7918\n  malformed: " + tc.value + "\n"
			h.click("Manifest editor")
			h.tt.Key(ui.Cmd, ui.KeyA)
			h.tt.Type(draft)
			snapshot, identity := h.w.editor.Snapshot(), h.w.draftIdentity()
			h.click("Check draft")
			h.pump(func() bool { return !h.w.schemaActive })
			wantLocations := 1
			if tc.wantType {
				wantLocations++
			}
			if !strings.Contains(h.w.schemaText, "/spec/checked · multipleOf") || !strings.Contains(h.w.schemaText, "Inspection is incomplete") || strings.Contains(h.w.schemaText, "/spec/malformed · multipleOf") || strings.Contains(h.w.schemaText, "/spec/malformed · type") != tc.wantType || len(h.w.schemaLocations) != wantLocations {
				t.Fatal("invalid divisor lost incomplete disclosure or invented a secondary violation")
			}
			for _, private := range []string{"7919", "7918", "123456789", "2147483648", "PRIVATE_SCHEMA_DIVISOR", "PRIVATE_DRAFT_VALUE"} {
				if strings.Contains(h.w.schemaText+h.w.schemaStatus, private) {
					t.Fatal("multipleOf assistance echoed a schema operand or draft value")
				}
				for _, location := range h.w.schemaLocations {
					if strings.Contains(location.label, private) {
						t.Fatal("multipleOf source label echoed an operand or draft value")
					}
				}
			}
			if h.w.editor != buffer || h.w.editorText() != draft || h.w.draftIdentity() != identity || snapshot.String() != draft || original.String() != schemaDraft || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("incomplete inspection changed input, snapshot, identity, plan or server")
			}
			for _, label := range []string{"Manifest editor", "Check draft", "Clear schema", "Schema location", "Go to location", "Schema assistance", "Preview edit", "Apply", "Close detail"} {
				r, ok := h.tt.Find(label)
				if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 1088 || r.Y+r.H > 700 {
					t.Errorf("%s outside minimum window: %+v found=%v", label, r, ok)
				}
			}
			target := chooseDiagnostic(h, "/spec/checked")
			if !target.position.Exact || !strings.HasPrefix(string([]rune(draft)[target.position.RuneOffset:]), "checked:") {
				t.Fatal("incomplete inspection lost the exact established mismatch location")
			}
			h.click("Go to location")
			caret, ok := h.tt.TextCaret()
			editor, _ := h.tt.Find("Manifest editor")
			if !ok || caret.Y < editor.Y || caret.Y > editor.Y+editor.H || !h.tt.Focused("Manifest editor") || h.w.editorText() != draft || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("minimum-window navigation hid its target or changed input/server")
			}
			saveNativeScreenshot(t, h.tt)
			h.tt.Key(ui.Cmd, ui.KeyZ)
			if h.w.editorText() != schemaDraft {
				t.Fatal("incomplete inspection broke mounted native Undo")
			}
			h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
			if h.w.editorText() != draft || h.w.editor != buffer || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("incomplete inspection broke mounted Redo or mutated the server")
			}
		})
	}
}
