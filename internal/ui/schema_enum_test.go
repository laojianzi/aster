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

const nativeEnumDocument = `{
 "openapi":"3.0.0","components":{"schemas":{
  "Pod":{"type":"object","x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"Pod"}],
   "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},
    "metadata":{"type":"object","additionalProperties":true},
    "spec":{"type":"object","properties":{
     "containers":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"}}}},
     "objectChoice":{"type":"object","properties":{
      "label":{"type":"string"},"count":{"type":"integer"},"ratio":{"type":"number"},
      "enabled":{"type":"boolean"},"note":{"type":"string","nullable":true},
      "nested":{"type":"object","properties":{"steps":{"type":"array","items":{"type":"integer"}},
       "emptyObject":{"type":"object","additionalProperties":true},"emptyArray":{"type":"array","items":{"type":"string"}},
       "PRIVATE_ENUM_NESTED_KEY":{"type":"string"}}}
     },"enum":[{"label":"PRIVATE_ENUM_ALPHA","count":9007199254740993,"ratio":0.5,"enabled":true,"note":null,
       "nested":{"steps":[1,2,1],"emptyObject":{},"emptyArray":[],"PRIVATE_ENUM_NESTED_KEY":"保密🚢"}}]},
     "arrayChoice":{"type":"array","items":{"type":"integer"},"enum":[[1,2,1],[2,1]]},
     "nullableChoice":{"type":"object","nullable":true,"properties":{"mode":{"type":"string"}},
       "enum":[null,{"mode":"PRIVATE_NULL_ALT"}]}
    }}
   }}
 }}}
`

const nativeEnumIncompleteDocument = `{
 "openapi":"3.0.0","components":{"schemas":{
  "Pod":{"type":"object","x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"Pod"}],
   "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},
    "metadata":{"type":"object","additionalProperties":true},
    "spec":{"type":"object","properties":{
     "containers":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"}}}},
     "checked":{"type":"object","properties":{"code":{"type":"string"}},"enum":[{"code":"PRIVATE_SCHEMA_ALLOWED"}]},
     "malformed":{"type":"object","additionalProperties":true,"enum":%s}
    }}
   }}
 }}}
`

// End targets a visual row in a soft-wrapped native editor. Select the complete
// bounded source line through actual rune navigation, then verify its selection
// through Copy, before replacing an object/array at the original field pointer.
func selectNativeSchemaEnumLine(t *testing.T, tt *ui.Tester, line string) {
	t.Helper()
	runes := len([]rune(line))
	remaining, steps := runes, 0
	for remaining > 0 && steps+remaining <= 2*runes+1 {
		for range remaining {
			tt.Key(ui.Shift, ui.KeyRight)
			steps++
		}
		tt.SetClipboard("the complete enum line was not selected")
		tt.Key(ui.Cmd, ui.KeyC)
		selected := tt.Clipboard()
		if selected == line {
			return
		}
		// A soft-wrap boundary can expose two caret positions at one source
		// offset. Account for it using the actual copied source prefix, with
		// a fixed step bound, rather than assuming one key always advances.
		if selected == "" || !strings.HasPrefix(line, selected) {
			t.Fatal("native selection left the intended original enum source line")
		}
		remaining = runes - len([]rune(selected))
	}
	t.Fatal("native rune navigation did not select the complete bounded enum source line")
}

func TestNativeSchemaEnumPreservesDraftAndHistory(t *testing.T) {
	// Use the current-identity read-only transport and the actual mounted editor.
	h, writes := nativeMultipleOfHarness(t, nativeEnumDocument)
	buffer, original := h.w.editor, h.w.editor.Snapshot()
	const selected = "中文 🚢\n# café\n"
	const originalLine = "objectChoice: {label: PRIVATE_DRAFT_ENUM, count: 9007199254740993, ratio: 0.5, enabled: true, note: null, nested: {steps: [1, 2, 1], emptyObject: {}, emptyArray: [], PRIVATE_ENUM_NESTED_KEY: 保密🚢}}"
	before := "# " + selected + schemaDraft + "  " + originalLine + "\n  arrayChoice: [1, 2, 1]\n  nullableChoice: null\n"
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
	if !strings.Contains(h.w.schemaText, "/spec/objectChoice · enum") || len(h.w.schemaLocations) != 1 || strings.Contains(h.w.schemaText, "Inspection is incomplete") {
		t.Fatal("structured mismatch was missed or ordered array and local nullable enum equality were rejected")
	}
	for _, private := range []string{"PRIVATE_DRAFT_ENUM", "PRIVATE_ENUM_ALPHA", "PRIVATE_ENUM_NESTED_KEY", "PRIVATE_NULL_ALT", "保密🚢", "9007199254740993"} {
		if strings.Contains(h.w.schemaText+h.w.schemaStatus, private) {
			t.Fatal("enum assistance echoed an alternative, nested key or draft value")
		}
		for _, location := range h.w.schemaLocations {
			if strings.Contains(location.label, private) {
				t.Fatal("enum source label echoed an alternative, nested key or draft value")
			}
		}
	}
	// Native Copy at the retained endpoint proves the selection survived Check;
	// refocusing with Tab would select all and could not establish that fact.
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
		t.Fatal("enum inspection changed selection, input, snapshot, identity, plan or server")
	}
	target := chooseDiagnostic(h, "/spec/objectChoice")
	if !target.position.Exact || !strings.HasPrefix(string([]rune(before)[target.position.RuneOffset:]), "objectChoice:") {
		t.Fatal("enum diagnostic did not retain its original parent field and Unicode source coordinate")
	}
	h.click("Go to location")
	if !h.tt.Focused("Manifest editor") || h.w.editorText() != before || h.w.plan != nil {
		t.Fatal("enum navigation changed input or did not focus its original parent field")
	}
	saveNativeScreenshot(t, h.tt)
	const correctedLine = "objectChoice: {nested: {PRIVATE_ENUM_NESTED_KEY: 保密🚢, emptyArray: [], emptyObject: {}, steps: [1, 2, 1]}, note: null, enabled: true, ratio: 5e-1, count: 9007199254740993, label: PRIVATE_ENUM_ALPHA}"
	selectNativeSchemaEnumLine(t, h.tt, originalLine)
	h.tt.Type(correctedLine)
	corrected := h.w.editorText()
	obj, err := manifest.Decode([]byte(corrected))
	if err != nil {
		t.Fatal(err)
	}
	count, found, err := unstructured.NestedInt64(obj.Object, "spec", "objectChoice", "count")
	if err != nil || !found || count != 9007199254740993 {
		t.Fatal("native correction rounded the nested integer above 2^53")
	}
	ratio, found, err := unstructured.NestedFloat64(obj.Object, "spec", "objectChoice", "ratio")
	if err != nil || !found || ratio != 0.5 {
		t.Fatal("native correction changed the decoded fractional number")
	}
	for _, path := range [][]string{{"spec", "objectChoice", "note"}, {"spec", "nullableChoice"}} {
		value, found, err := unstructured.NestedFieldNoCopy(obj.Object, path...)
		if err != nil || !found || value != nil {
			t.Fatal("native correction lost an explicitly present null")
		}
	}
	steps, found, err := unstructured.NestedSlice(obj.Object, "spec", "objectChoice", "nested", "steps")
	if err != nil || !found || len(steps) != 3 || steps[0] != int64(1) || steps[1] != int64(2) || steps[2] != int64(1) {
		t.Fatal("native correction lost array order or repeated entries")
	}
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if len(h.w.schemaLocations) != 0 || !strings.Contains(h.w.schemaText, "No supported structural or constraint hints found.") || strings.Contains(h.w.schemaText, "Inspection is incomplete") || h.w.editorText() != corrected || h.w.plan != nil {
		t.Fatal("enum equality rejected reordered object keys, exact decoded numbers, nested empty containers or local null")
	}
	// Every history assertion uses the same mounted editor and original buffer.
	focusMultipleOfEditorByKeyboard(h)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != before {
		t.Fatal("enum inspection reset Undo of the source correction")
	}
	h.tt.SetClipboard("correction selection was not restored")
	h.tt.Key(ui.Cmd, ui.KeyC)
	if h.tt.Clipboard() != originalLine {
		t.Fatal("Undo did not restore the complete original corrected-line selection")
	}
	h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if h.w.editorText() != corrected {
		t.Fatal("enum inspection reset Redo of the source correction")
	}
	unmatched := strings.Replace(corrected, "arrayChoice: [1, 2, 1]", "arrayChoice: [1, 1, 2]", 1)
	unmatched = strings.Replace(unmatched, "count: 9007199254740993", "count: 9007199254740992", 1)
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(unmatched)
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/objectChoice · enum") || !strings.Contains(h.w.schemaText, "/spec/arrayChoice · enum") || len(h.w.schemaLocations) != 2 || strings.Contains(h.w.schemaText, "Inspection is incomplete") || strings.Contains(h.w.schemaText+h.w.schemaStatus, "9007199254740992") || h.w.editorText() != unmatched || h.w.plan != nil {
		t.Fatal("enum inspection lost exact integer or array-order distinctions, privacy or source immutability")
	}
	focusMultipleOfEditorByKeyboard(h)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != corrected {
		t.Fatal("checking the second enum draft broke its Undo")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != before {
		t.Fatal("second enum inspection discarded the preceding correction history")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != schemaDraft || h.w.editor != buffer || original.String() != schemaDraft || snapshot.String() != before || h.w.plan != nil || writes.Load() != 0 {
		t.Fatal("enum inspection discarded original history or mutated retained state")
	}
}

func TestNativeSchemaEnumMinimumWindowAndIncomplete(t *testing.T) {
	// Keep lines short while exceeding a decoded nested-string inspection cap.
	// This does not extend upstream arbitrary Type, IME or font coverage.
	longValue := strings.Repeat(strings.Repeat("x", 127)+"\n", schemaassist.MaxEnumTextBytes/128) + "x"
	const largeValueMarker = "PRIVATE_LARGE_ENUM_DRAFT"
	longValue = largeValueMarker + longValue[len(largeValueMarker):]
	longField := "  malformed:\n    message: |-\n      " + strings.ReplaceAll(longValue, "\n", "\n      ") + "\n"
	deepMetadata := "[{}," + strings.Repeat("[", schemaassist.MaxEnumValueDepth+1) + `"PRIVATE_SCHEMA_DEEP"` + strings.Repeat("]", schemaassist.MaxEnumValueDepth+1) + "]"
	for _, tc := range []struct {
		name, metadata, field string
		wantType              bool
		wantInputBytes        int
	}{
		{"non-array-metadata", `"PRIVATE_SCHEMA_ENUM"`, "  malformed: {message: PRIVATE_DRAFT_VALUE}\n", false, 0},
		{"type-and-late-invalid-metadata", `[{}, {"n":1e2147483648}]`, "  malformed: 123456789\n", true, 0},
		{"depth-budget-after-match", deepMetadata, "  malformed: {}\n", false, 0},
		{"nested-input-budget", `[{}]`, longField, false, schemaassist.MaxEnumTextBytes + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, writes := nativeMultipleOfHarness(t, fmt.Sprintf(nativeEnumIncompleteDocument, tc.metadata))
			h.tt.SetSize(1100, 700)
			buffer, original := h.w.editor, h.w.editor.Snapshot()
			draft := "# 中文 🚢 é — private enum alternatives stay in the editor\n" + schemaDraft + "  checked: {code: PRIVATE_DRAFT_CHECKED}\n" + tc.field
			h.click("Manifest editor")
			h.tt.Key(ui.Cmd, ui.KeyA)
			h.tt.Type(draft)
			snapshot, identity := h.w.editor.Snapshot(), h.w.draftIdentity()
			if tc.wantInputBytes != 0 {
				obj, err := manifest.Decode([]byte(h.w.editorText()))
				if err != nil {
					t.Fatal(err)
				}
				value, found, err := unstructured.NestedString(obj.Object, "spec", "malformed", "message")
				if err != nil || !found || value != longValue || len(value) != tc.wantInputBytes {
					t.Fatal("native budget fixture did not retain its complete oversized nested decoded string")
				}
			}
			h.click("Check draft")
			h.pump(func() bool { return !h.w.schemaActive })
			wantLocations := 1
			if tc.wantType {
				wantLocations++
			}
			if !strings.Contains(h.w.schemaText, "/spec/checked · enum") || !strings.Contains(h.w.schemaText, "Inspection is incomplete") || strings.Contains(h.w.schemaText, "/spec/malformed · enum") || strings.Contains(h.w.schemaText, "/spec/malformed · type") != tc.wantType || len(h.w.schemaLocations) != wantLocations {
				t.Fatal("incomplete enum metadata or input lost disclosure or invented a secondary mismatch")
			}
			for _, private := range []string{"PRIVATE_SCHEMA_ALLOWED", "PRIVATE_DRAFT_CHECKED", "PRIVATE_SCHEMA_ENUM", "PRIVATE_SCHEMA_DEEP", "PRIVATE_DRAFT_VALUE", largeValueMarker, "123456789", "2147483648"} {
				if strings.Contains(h.w.schemaText+h.w.schemaStatus, private) {
					t.Fatal("incomplete enum assistance echoed metadata or a draft value")
				}
				for _, location := range h.w.schemaLocations {
					if strings.Contains(location.label, private) {
						t.Fatal("incomplete enum source label echoed metadata or a draft value")
					}
				}
			}
			if h.w.editor != buffer || h.w.editorText() != draft || h.w.draftIdentity() != identity || snapshot.String() != draft || original.String() != schemaDraft || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("incomplete enum inspection changed input, snapshot, identity, plan or server")
			}
			for _, label := range []string{"Manifest editor", "Check draft", "Clear schema", "Schema location", "Go to location", "Schema assistance", "Preview edit", "Apply", "Close detail"} {
				r, ok := h.tt.Find(label)
				if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 1088 || r.Y+r.H > 700 {
					t.Errorf("%s outside minimum window: %+v found=%v", label, r, ok)
				}
			}
			target := chooseDiagnostic(h, "/spec/checked")
			if !target.position.Exact || !strings.HasPrefix(string([]rune(draft)[target.position.RuneOffset:]), "checked:") {
				t.Fatal("incomplete enum inspection lost the established mismatch's exact parent location")
			}
			h.click("Go to location")
			caret, ok := h.tt.TextCaret()
			editor, _ := h.tt.Find("Manifest editor")
			if !ok || caret.Y < editor.Y || caret.Y > editor.Y+editor.H || !h.tt.Focused("Manifest editor") || h.w.editorText() != draft || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("minimum-window enum navigation hid its target or changed input/server")
			}
			saveNativeScreenshot(t, h.tt)
			h.tt.Key(ui.Cmd, ui.KeyZ)
			if h.w.editorText() != schemaDraft {
				t.Fatal("incomplete enum inspection broke mounted native Undo")
			}
			h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
			if h.w.editorText() != draft || h.w.editor != buffer || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("incomplete enum inspection broke mounted Redo or mutated the server")
			}
		})
	}
}
