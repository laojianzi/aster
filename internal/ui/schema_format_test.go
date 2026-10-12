package uiworkbench

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/schemaassist"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const nativeFormatDocument = `{
 "openapi":"3.0.0","components":{"schemas":{
  "Pod":{"type":"object","x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"Pod"}],
   "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},
    "metadata":{"type":"object","additionalProperties":true},
    "spec":{"type":"object","properties":{
     "containers":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"}}}},
     "date":{"type":"string","format":"d-a-t-e"},
     "byte":{"type":"string","format":"b-y-t-e"},
     "uuid":{"type":"string","format":"u-u-i-d"},
     "nullable":{"type":"string","nullable":true,"format":"date"}
    }}
   }}
 }}}
`

const nativeFormatIncompleteDocument = `{
 "openapi":"3.0.0","components":{"schemas":{
  "Pod":{"type":"object","x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"Pod"}],
   "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},
    "metadata":{"type":"object","additionalProperties":true},
    "spec":{"type":"object","properties":{
     "containers":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"}}}},
     "checked":{"type":"string","format":"date"},
     "malformed":{"type":"string","format":%s}
    }}
   }}
 }}}
`

func TestNativeSchemaFormatPreservesDraftAndHistory(t *testing.T) {
	// Reuse the existing read-only Schema transport and the actual native editor.
	h, writes := nativeMultipleOfHarness(t, nativeFormatDocument)
	buffer, original := h.w.editor, h.w.editor.Snapshot()
	const selected = "中文 🚢\n# café\n"
	const validUUID = "123E4567E89B-02D3A456-426614174000"
	before := "# " + selected + schemaDraft + "  date: '2023-02-29'\n  byte: AB==\n  uuid: " + validUUID + "\n  nullable: null\n"
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(before)
	snapshot, identity := h.w.editor.Snapshot(), h.w.draftIdentity()
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Key(0, ui.KeyLeft)
	for range 2 {
		h.tt.Key(0, ui.KeyRight)
	}
	for range []rune(selected) {
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
	if !strings.Contains(h.w.schemaText, "/spec/date · format") || len(h.w.schemaLocations) != 1 || strings.Contains(h.w.schemaText, "Inspection is incomplete") {
		t.Fatal("invalid calendar date was missed or valid base64, optional UUID separators or nullable input was rejected")
	}
	for _, private := range []string{"2023-02-29", "AB==", validUUID, "d-a-t-e", "b-y-t-e", "u-u-i-d"} {
		if strings.Contains(h.w.schemaText+h.w.schemaStatus, private) {
			t.Fatal("format assistance echoed an input value or raw advertised format name")
		}
		for _, location := range h.w.schemaLocations {
			if strings.Contains(location.label, private) {
				t.Fatal("format source label echoed an input value or raw format name")
			}
		}
	}
	// Copy through the native menu at the selected endpoint; Tab focus would
	// select all and could not demonstrate that Check preserved this selection.
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
		t.Fatal("format inspection changed selection, input, snapshot, identity, plan or server")
	}
	target := chooseDiagnostic(h, "/spec/date")
	if !target.position.Exact || !strings.HasPrefix(string([]rune(before)[target.position.RuneOffset:]), "date:") {
		t.Fatal("format diagnostic lost its original Unicode source coordinate")
	}
	h.click("Go to location")
	if !h.tt.Focused("Manifest editor") || h.w.editorText() != before || h.w.plan != nil {
		t.Fatal("format navigation changed input or did not focus its actual location")
	}
	saveNativeScreenshot(t, h.tt)
	h.tt.Key(ui.Shift, ui.KeyEnd)
	h.tt.Type("date: '0000-02-29'")
	corrected := h.w.editorText()
	obj, err := manifest.Decode([]byte(corrected))
	if err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]string{"date": "0000-02-29", "byte": "AB==", "uuid": validUUID} {
		value, found, err := unstructured.NestedString(obj.Object, "spec", field)
		if err != nil || !found || value != want {
			t.Fatal("native correction did not preserve the actual decoded format string")
		}
	}
	nullable, found, err := unstructured.NestedFieldNoCopy(obj.Object, "spec", "nullable")
	if err != nil || !found || nullable != nil {
		t.Fatal("native correction lost the explicitly nullable field")
	}
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if len(h.w.schemaLocations) != 0 || !strings.Contains(h.w.schemaText, "No supported structural or constraint hints found.") || strings.Contains(h.w.schemaText, "Inspection is incomplete") || h.w.editorText() != corrected || h.w.plan != nil {
		t.Fatal("year-zero date, noncanonical base64 padding or generic UUID semantics were rejected")
	}
	// All history assertions keep the same source editor mounted.
	focusMultipleOfEditorByKeyboard(h)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != before {
		t.Fatal("format inspection reset Undo of the source correction")
	}
	h.tt.SetClipboard("correction selection was not restored")
	h.tt.Key(ui.Cmd, ui.KeyC)
	if h.tt.Clipboard() != "date: '2023-02-29'" {
		t.Fatal("Undo did not restore the original corrected-line selection")
	}
	h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if h.w.editorText() != corrected {
		t.Fatal("format inspection reset Redo of the source correction")
	}
	const invalidUUID = "123E4567E89B-02D3A456-42661417400g"
	unmatched := strings.Replace(corrected, "byte: AB==", "byte: AA=A", 1)
	unmatched = strings.Replace(unmatched, validUUID, invalidUUID, 1)
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(unmatched)
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/byte · format") || !strings.Contains(h.w.schemaText, "/spec/uuid · format") || len(h.w.schemaLocations) != 2 || strings.Contains(h.w.schemaText, "Inspection is incomplete") || strings.Contains(h.w.schemaText+h.w.schemaStatus, "AA=A") || strings.Contains(h.w.schemaText+h.w.schemaStatus, invalidUUID) || h.w.editorText() != unmatched || h.w.plan != nil {
		t.Fatal("native formats did not diagnose invalid padding and non-hex UUID without value disclosure")
	}
	focusMultipleOfEditorByKeyboard(h)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != corrected {
		t.Fatal("checking the second format draft broke its Undo")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != before {
		t.Fatal("second format inspection discarded the preceding correction history")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != schemaDraft || h.w.editor != buffer || original.String() != schemaDraft || snapshot.String() != before || h.w.plan != nil || writes.Load() != 0 {
		t.Fatal("format inspection discarded original history or mutated retained state")
	}
}

func TestNativeSchemaFormatMinimumWindowAndIncomplete(t *testing.T) {
	// Actual native input exceeds the decoded string budget using short lines.
	// This does not claim to solve upstream arbitrary Type/IME layout limits.
	longValue := strings.Repeat(strings.Repeat("x", 127)+"\n", schemaassist.MaxFormatInputBytes/128) + "x"
	const largeValueMarker = "PRIVATE_LARGE_FORMAT_DRAFT"
	longValue = largeValueMarker + longValue[len(largeValueMarker):]
	longField := "  malformed: |-\n    " + strings.ReplaceAll(longValue, "\n", "\n    ") + "\n"
	for _, tc := range []struct {
		name, metadata, field string
		wantType              bool
		wantInputBytes        int
	}{
		{"unknown-format", `"PRIVATE_SCHEMA_FORMAT"`, "  malformed: PRIVATE_DRAFT_VALUE\n", false, 0},
		{"non-string-metadata", "123456789", "  malformed: PRIVATE_DRAFT_VALUE\n", false, 0},
		{"type-and-malformed-format", `"PRIVATE_SCHEMA_FORMAT"`, "  malformed: 123456789\n", true, 0},
		{"input-budget", `"date"`, longField, false, schemaassist.MaxFormatInputBytes + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, writes := nativeMultipleOfHarness(t, fmt.Sprintf(nativeFormatIncompleteDocument, tc.metadata))
			h.tt.SetSize(1100, 700)
			buffer, original := h.w.editor, h.w.editor.Snapshot()
			draft := "# 中文 🚢 é — private formats and values stay in the editor\n" + schemaDraft + "  checked: '2023-02-29'\n" + tc.field
			h.click("Manifest editor")
			h.tt.Key(ui.Cmd, ui.KeyA)
			h.tt.Type(draft)
			snapshot, identity := h.w.editor.Snapshot(), h.w.draftIdentity()
			if tc.wantInputBytes != 0 {
				obj, err := manifest.Decode([]byte(h.w.editorText()))
				if err != nil {
					t.Fatal(err)
				}
				value, found, err := unstructured.NestedString(obj.Object, "spec", "malformed")
				if err != nil || !found || value != longValue || len(value) != tc.wantInputBytes {
					t.Fatal("native budget fixture did not retain the complete oversized decoded string")
				}
			}
			h.click("Check draft")
			h.pump(func() bool { return !h.w.schemaActive })
			wantLocations := 1
			if tc.wantType {
				wantLocations++
			}
			if !strings.Contains(h.w.schemaText, "/spec/checked · format") || !strings.Contains(h.w.schemaText, "Inspection is incomplete") || strings.Contains(h.w.schemaText, "/spec/malformed · format") || strings.Contains(h.w.schemaText, "/spec/malformed · type") != tc.wantType || len(h.w.schemaLocations) != wantLocations {
				t.Fatal("uninspected format lost incomplete disclosure or invented a secondary violation")
			}
			for _, private := range []string{"2023-02-29", "PRIVATE_SCHEMA_FORMAT", "PRIVATE_DRAFT_VALUE", largeValueMarker, "123456789"} {
				if strings.Contains(h.w.schemaText+h.w.schemaStatus, private) {
					t.Fatal("incomplete format assistance echoed metadata or a draft value")
				}
				for _, location := range h.w.schemaLocations {
					if strings.Contains(location.label, private) {
						t.Fatal("incomplete format source label echoed metadata or a draft value")
					}
				}
			}
			if h.w.editor != buffer || h.w.editorText() != draft || h.w.draftIdentity() != identity || snapshot.String() != draft || original.String() != schemaDraft || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("incomplete format inspection changed input, snapshot, identity, plan or server")
			}
			for _, label := range []string{"Manifest editor", "Check draft", "Clear schema", "Schema location", "Go to location", "Schema assistance", "Preview edit", "Apply", "Close detail"} {
				r, ok := h.tt.Find(label)
				if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 1088 || r.Y+r.H > 700 {
					t.Errorf("%s outside minimum window: %+v found=%v", label, r, ok)
				}
			}
			target := chooseDiagnostic(h, "/spec/checked")
			if !target.position.Exact || !strings.HasPrefix(string([]rune(draft)[target.position.RuneOffset:]), "checked:") {
				t.Fatal("incomplete format inspection lost the exact established mismatch location")
			}
			h.click("Go to location")
			caret, ok := h.tt.TextCaret()
			editor, _ := h.tt.Find("Manifest editor")
			if !ok || caret.Y < editor.Y || caret.Y > editor.Y+editor.H || !h.tt.Focused("Manifest editor") || h.w.editorText() != draft || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("minimum-window format navigation hid its target or changed input/server")
			}
			saveNativeScreenshot(t, h.tt)
			h.tt.Key(ui.Cmd, ui.KeyZ)
			if h.w.editorText() != schemaDraft {
				t.Fatal("incomplete format inspection broke mounted native Undo")
			}
			h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
			if h.w.editorText() != draft || h.w.editor != buffer || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("incomplete format inspection broke mounted Redo or mutated the server")
			}
		})
	}
}
