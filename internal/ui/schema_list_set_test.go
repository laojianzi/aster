package uiworkbench

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/schemaassist"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const nativeListSetFieldSchema = `{"type":"array","x-kubernetes-list-type":"set","items":{"type":"string"}}`

const nativeListSetDocument = `{
 "openapi":"3.0.0","components":{"schemas":{
  "Pod":{"type":"object","x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"Pod"}],
   "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},
    "metadata":{"type":"object","additionalProperties":true},
    "spec":{"type":"object","properties":{
     "containers":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"}}}},
     "values":` + nativeListSetFieldSchema + `,
     "checked":` + nativeListSetFieldSchema + `,
     "malformed":` + nativeListSetFieldSchema + `
    }}
   }}
 }}}
`

const nativeListSetIncompleteDocument = `{
 "openapi":"3.0.0","components":{"schemas":{
  "Pod":{"type":"object","x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"Pod"}],
   "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},
    "metadata":{"type":"object","additionalProperties":true},
    "spec":{"type":"object","properties":{
     "containers":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"}}}},
     "values":` + nativeListSetFieldSchema + `,
     "checked":` + nativeListSetFieldSchema + `,
     "malformed":%s
    }}
   }}
 }}}
`

func TestNativeSchemaListSetPreservesDraftAndHistory(t *testing.T) {
	h, writes := nativeMultipleOfHarness(t, nativeListSetDocument)
	buffer, original := h.w.editor, h.w.editor.Snapshot()
	const selected = "中文 🚢\n# café\n"
	// Source uses ordinary LF lines. YAML escape spellings, rather than raw
	// native CRLF input, produce distinct decoded CRLF/LF and embedded NUL values.
	const originalLine = `values: ["PRIVATE_DUPLICATE_VALUE", "PRIVATE_DUPLICATE_VALUE", "PRIVATE_DUPLICATE_VALUE", "", "\u0000", "中文船🚢", "é", "é", "a\u0000b", "a|b", "a\r\nb", "a\nb"]`
	before := "# " + selected + schemaDraft + "  " + originalLine + "\n"
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
	h.tt.SetClipboard("selection was not copied after list-set inspection")
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	const message = "List entries repeat the advertised string value."
	if !strings.Contains(h.w.schemaText, "/spec/values · list-set") || strings.Count(h.w.schemaText, message) != 1 || len(h.w.schemaLocations) != 1 || strings.Contains(h.w.schemaText, "Inspection is incomplete") {
		t.Fatal("list-set inspection missed repeated strings or emitted more than one parent hint")
	}
	for _, private := range []string{"PRIVATE_DUPLICATE_VALUE", "中文船🚢", "a|b", "a\x00b", `\u0000`} {
		if strings.Contains(h.w.schemaText+h.w.schemaStatus, private) {
			t.Fatal("list-set assistance echoed a draft value")
		}
		for _, location := range h.w.schemaLocations {
			if strings.Contains(location.label, private) {
				t.Fatal("list-set source label echoed a draft value")
			}
		}
	}
	// Restore focus at the retained selected endpoint. Tab explicitly selects
	// all and therefore cannot prove that Check preserved the prior selection.
	editorAfter, editorOK := h.tt.Find("Manifest editor")
	x := editorAfter.X + selectedCaret.X - editorBefore.X + 0.25
	y := editorAfter.Y + selectedCaret.Y - editorBefore.Y + selectedCaret.H/2
	if !editorOK || x < editorAfter.X || x >= editorAfter.X+editorAfter.W || y < editorAfter.Y || y >= editorAfter.Y+editorAfter.H {
		t.Fatal("list-set inspection did not retain a visible selected endpoint")
	}
	h.tt.RightClickAt(x, y)
	if err := h.tt.ChooseMenuItem("Copy"); err != nil {
		t.Fatal(err)
	}
	if h.tt.Clipboard() != selected || h.w.editor != buffer || h.w.editorText() != before || h.w.draftIdentity() != identity || snapshot.String() != before || original.String() != schemaDraft || h.w.plan != nil || writes.Load() != 0 {
		t.Fatal("list-set inspection changed selection, source, snapshot, identity, plan or server")
	}
	target := chooseDiagnostic(h, "/spec/values")
	if !target.position.Exact || !strings.HasPrefix(string([]rune(before)[target.position.RuneOffset:]), "values:") {
		t.Fatal("list-set diagnostic did not retain the parent array's original Unicode source coordinate")
	}
	h.click("Go to location")
	if !h.tt.Focused("Manifest editor") || h.w.editorText() != before || h.w.plan != nil {
		t.Fatal("list-set navigation changed source or did not focus its parent array")
	}
	saveNativeScreenshot(t, h.tt)
	const correctedLine = `values: ["PRIVATE_DUPLICATE_VALUE", "PRIVATE_UNIQUE_VALUE_B", "PRIVATE_UNIQUE_VALUE_C", "", "\u0000", "中文船🚢", "é", "é", "a\u0000b", "a|b", "a\r\nb", "a\nb"]`
	// Actual rune navigation and Copy verification cross soft wraps; Shift+End
	// would select only the current visual row of this flow-style array.
	selectNativeSchemaEnumLine(t, h.tt, originalLine)
	h.tt.Type(correctedLine)
	corrected := h.w.editorText()
	obj, err := manifest.Decode([]byte(corrected))
	if err != nil {
		t.Fatal(err)
	}
	values, found, err := unstructured.NestedSlice(obj.Object, "spec", "values")
	wantValues := []any{"PRIVATE_DUPLICATE_VALUE", "PRIVATE_UNIQUE_VALUE_B", "PRIVATE_UNIQUE_VALUE_C", "", "\x00", "中文船🚢", "é", "e\u0301", "a\x00b", "a|b", "a\r\nb", "a\nb"}
	if err != nil || !found || !reflect.DeepEqual(values, wantValues) {
		t.Fatal("native list-set correction lost exact strings, empty/NUL values or original order")
	}
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if len(h.w.schemaLocations) != 0 || !strings.Contains(h.w.schemaText, "No supported structural or constraint hints found.") || strings.Contains(h.w.schemaText, "Inspection is incomplete") || h.w.editorText() != corrected || h.w.plan != nil {
		t.Fatal("list-set correction conflated distinct empty, NUL, Unicode normalization or decoded line-ending values")
	}
	// These history assertions all use the same mounted widget and buffer.
	focusMultipleOfEditorByKeyboard(h)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != before {
		t.Fatal("list-set inspection reset Undo of the source correction")
	}
	h.tt.SetClipboard("list-set correction selection was not restored")
	h.tt.Key(ui.Cmd, ui.KeyC)
	if h.tt.Clipboard() != originalLine {
		t.Fatal("list-set Undo did not restore the complete original source selection")
	}
	h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if h.w.editorText() != corrected {
		t.Fatal("list-set inspection reset Redo of the source correction")
	}
	for _, tc := range []struct{ name, from, to string }{
		{"empty-string", `"PRIVATE_UNIQUE_VALUE_B"`, `""`},
		{"embedded-nul", `"a|b"`, `"a\u0000b"`},
		{"unicode", `"PRIVATE_UNIQUE_VALUE_C"`, `"中文船🚢"`},
		{"normalization-distinct-then-exact", `"é"`, `"é"`},
	} {
		duplicate := strings.Replace(corrected, tc.from, tc.to, 1)
		if duplicate == corrected {
			t.Fatalf("test did not create the intended %s duplicate", tc.name)
		}
		h.tt.Key(ui.Cmd, ui.KeyA)
		h.tt.Type(duplicate)
		duplicateIdentity := h.w.draftIdentity()
		h.click("Check draft")
		h.pump(func() bool { return !h.w.schemaActive })
		if !strings.Contains(h.w.schemaText, "/spec/values · list-set") || strings.Count(h.w.schemaText, message) != 1 || len(h.w.schemaLocations) != 1 || strings.Contains(h.w.schemaText, "Inspection is incomplete") || strings.Contains(h.w.schemaText+h.w.schemaStatus, "PRIVATE_") || strings.Contains(h.w.schemaText+h.w.schemaStatus, "中文船🚢") || h.w.editorText() != duplicate || h.w.draftIdentity() != duplicateIdentity || h.w.editor != buffer || h.w.plan != nil || writes.Load() != 0 {
			t.Fatalf("list-set inspection missed the %s duplicate or changed source/privacy", tc.name)
		}
		focusMultipleOfEditorByKeyboard(h)
		h.tt.Key(ui.Cmd, ui.KeyZ)
		if h.w.editorText() != corrected {
			t.Fatalf("checking the %s duplicate broke mounted Undo", tc.name)
		}
		h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
		if h.w.editorText() != duplicate {
			t.Fatalf("checking the %s duplicate broke mounted Redo", tc.name)
		}
		h.tt.Key(ui.Cmd, ui.KeyZ)
		if h.w.editorText() != corrected {
			t.Fatalf("repeated inspection lost the correction before %s", tc.name)
		}
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != before {
		t.Fatal("repeated list-set inspection discarded the preceding correction history")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != schemaDraft || h.w.editor != buffer || original.String() != schemaDraft || snapshot.String() != before || h.w.plan != nil || writes.Load() != 0 {
		t.Fatal("list-set inspection discarded original history or mutated retained state")
	}
}

func TestNativeSchemaListSetMinimumWindowAndIncomplete(t *testing.T) {
	const prefix = "  malformed:\n  - PRIVATE_LATE_DUPLICATE\n  - PRIVATE_LATE_DUPLICATE\n"
	entryBudget := prefix
	for i := 2; i <= schemaassist.MaxListSetItems; i++ {
		entryBudget += fmt.Sprintf("  - PRIVATE_ENTRY_VALUE_%d\n", i)
	}
	// Many short source lines retain the complete oversized decoded value.
	// This does not qualify arbitrary giant non-clipboard Type/IME segments.
	longValue := strings.Repeat(strings.Repeat("x", 127)+"\n", schemaassist.MaxListSetValueBytes/128) + "x"
	const longMarker = "PRIVATE_OVERSIZE_VALUE"
	longValue = longMarker + longValue[len(longMarker):]
	valueBudget := prefix + "  - |-\n      " + strings.ReplaceAll(longValue, "\n", "\n      ") + "\n"
	malformedMetadata := strings.Replace(nativeListSetFieldSchema, `"type":"string"`, `"type":"string","nullable":"PRIVATE_SCHEMA_NULLABLE"`, 1)
	for _, tc := range []struct {
		name, metadata, field string
		structural            []string
		wantItems             int
		wantValueBytes        int
	}{
		{"late-invalid-values-after-duplicate", nativeListSetFieldSchema, prefix + "  - null\n  - 123456789\n  - {value: PRIVATE_OBJECT}\n  - [PRIVATE_NESTED_ARRAY]\n", []string{"/spec/malformed/2 · type", "/spec/malformed/3 · type", "/spec/malformed/4 · type", "/spec/malformed/5 · type"}, 6, 0},
		{"late-malformed-metadata-with-wrong-type", malformedMetadata, "  malformed: PRIVATE_WRONG_ARRAY_TYPE\n", []string{"/spec/malformed · type"}, 0, 0},
		{"entry-budget-after-duplicate", nativeListSetFieldSchema, entryBudget, nil, schemaassist.MaxListSetItems + 1, 0},
		{"value-byte-budget-after-duplicate", nativeListSetFieldSchema, valueBudget, nil, 3, schemaassist.MaxListSetValueBytes + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, writes := nativeMultipleOfHarness(t, fmt.Sprintf(nativeListSetIncompleteDocument, tc.metadata))
			h.tt.SetSize(1100, 700)
			buffer, original := h.w.editor, h.w.editor.Snapshot()
			draft := "# 中文 🚢 é — set values stay in the editor\n" + schemaDraft + "  checked: [PRIVATE_CHECKED_VALUE, PRIVATE_CHECKED_VALUE]\n" + tc.field
			h.click("Manifest editor")
			h.tt.Key(ui.Cmd, ui.KeyA)
			h.tt.Type(draft)
			snapshot, identity := h.w.editor.Snapshot(), h.w.draftIdentity()
			obj, err := manifest.Decode([]byte(h.w.editorText()))
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantItems != 0 {
				items, found, err := unstructured.NestedSlice(obj.Object, "spec", "malformed")
				if err != nil || !found || len(items) != tc.wantItems {
					t.Fatal("native incomplete fixture did not retain every advertised set entry")
				}
				if tc.wantValueBytes != 0 {
					value, valid := items[2].(string)
					if !valid || value != longValue || len(value) != tc.wantValueBytes {
						t.Fatal("native list-set budget fixture lost the complete oversized decoded value")
					}
				}
			}
			h.click("Check draft")
			h.pump(func() bool { return !h.w.schemaActive })
			if !strings.Contains(h.w.schemaText, "/spec/checked · list-set") || strings.Count(h.w.schemaText, "List entries repeat the advertised string value.") != 1 || !strings.Contains(h.w.schemaText, "Inspection is incomplete") || strings.Contains(h.w.schemaText, "/spec/malformed · list-set") || len(h.w.schemaLocations) != 1+len(tc.structural) {
				t.Fatal("late invalid metadata/values or budget emitted a premature set hint or lost incompleteness/structural checks")
			}
			for _, diagnostic := range tc.structural {
				if !strings.Contains(h.w.schemaText, diagnostic) {
					t.Fatalf("incomplete list-set inspection lost structural diagnostic %s", diagnostic)
				}
			}
			for _, private := range []string{"PRIVATE_CHECKED_VALUE", "PRIVATE_LATE_DUPLICATE", "PRIVATE_OBJECT", "PRIVATE_NESTED_ARRAY", "PRIVATE_WRONG_ARRAY_TYPE", "PRIVATE_SCHEMA_NULLABLE", "PRIVATE_ENTRY_VALUE", longMarker, "123456789"} {
				if strings.Contains(h.w.schemaText+h.w.schemaStatus, private) {
					t.Fatal("incomplete list-set assistance echoed private metadata or a draft value")
				}
				for _, location := range h.w.schemaLocations {
					if strings.Contains(location.label, private) {
						t.Fatal("incomplete list-set location echoed private metadata or a draft value")
					}
				}
			}
			if h.w.editor != buffer || h.w.editorText() != draft || h.w.draftIdentity() != identity || snapshot.String() != draft || original.String() != schemaDraft || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("incomplete list-set inspection changed source, snapshot, identity, plan or server")
			}
			for _, label := range []string{"Manifest editor", "Check draft", "Clear schema", "Schema location", "Go to location", "Schema assistance", "Preview edit", "Apply", "Close detail"} {
				r, ok := h.tt.Find(label)
				if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 1088 || r.Y+r.H > 700 {
					t.Errorf("%s outside minimum window: %+v found=%v", label, r, ok)
				}
			}
			target := chooseDiagnostic(h, "/spec/checked")
			if !target.position.Exact || !strings.HasPrefix(string([]rune(draft)[target.position.RuneOffset:]), "checked:") {
				t.Fatal("incomplete list-set inspection lost the established duplicate's parent location")
			}
			h.click("Go to location")
			caret, ok := h.tt.TextCaret()
			editor, _ := h.tt.Find("Manifest editor")
			if !ok || caret.Y < editor.Y || caret.Y > editor.Y+editor.H || !h.tt.Focused("Manifest editor") || h.w.editorText() != draft || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("minimum-window list-set navigation hid its target or changed source/server")
			}
			saveNativeScreenshot(t, h.tt)
			h.tt.Key(ui.Cmd, ui.KeyZ)
			if h.w.editorText() != schemaDraft {
				t.Fatal("incomplete list-set inspection broke mounted native Undo")
			}
			h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
			if h.w.editorText() != draft || h.w.editor != buffer || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("incomplete list-set inspection broke mounted native Redo or mutated the server")
			}
		})
	}
}
