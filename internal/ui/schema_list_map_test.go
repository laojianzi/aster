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

const nativeListMapFieldSchema = `{"type":"array","x-kubernetes-list-type":"map","x-kubernetes-list-map-keys":["name"],
 "items":{"type":"object","required":["name"],"properties":{"name":{"type":"string"},"value":{"type":"string"}}}}`

const nativeListMapDocument = `{
 "openapi":"3.0.0","components":{"schemas":{
  "Pod":{"type":"object","x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"Pod"}],
   "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},
    "metadata":{"type":"object","additionalProperties":true},
    "spec":{"type":"object","properties":{
     "containers":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"}}}},
     "named":` + nativeListMapFieldSchema + `,
     "scoped":{"type":"array","x-kubernetes-list-type":"map","x-kubernetes-list-map-keys":["zone","name"],
      "items":{"type":"object","required":["zone","name"],"properties":{
       "zone":{"type":"string"},"name":{"type":"string"},"value":{"type":"string"}}}}
    }}
   }}
 }}}
`

const nativeListMapIncompleteDocument = `{
 "openapi":"3.0.0","components":{"schemas":{
  "Pod":{"type":"object","x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"Pod"}],
   "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},
    "metadata":{"type":"object","additionalProperties":true},
    "spec":{"type":"object","properties":{
     "containers":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"}}}},
     "checked":` + nativeListMapFieldSchema + `,
     "malformed":%s
    }}
   }}
 }}}
`

func TestNativeSchemaListMapPreservesDraftAndHistory(t *testing.T) {
	h, writes := nativeMultipleOfHarness(t, nativeListMapDocument)
	buffer, original := h.w.editor, h.w.editor.Snapshot()
	const selected = "中文 🚢\n# café\n"
	const originalLine = `named: [{name: PRIVATE_DUPLICATE_KEY, value: PRIVATE_NONKEY_A}, {name: PRIVATE_DUPLICATE_KEY, value: PRIVATE_NONKEY_B}, {name: PRIVATE_DUPLICATE_KEY, value: PRIVATE_NONKEY_C}]`
	// The first two tuples collide if joined with a delimiter. The last two
	// differ only by Unicode normalization; both exact strings must survive.
	const scopedLine = `scoped: [{zone: "a|b", name: "c", value: one}, {zone: "a", name: "b|c", value: two}, {zone: "", name: "", value: empty}, {zone: "é", name: "船🚢", value: composed}, {zone: "é", name: "船🚢", value: decomposed}]`
	before := "# " + selected + schemaDraft + "  " + originalLine + "\n  " + scopedLine + "\n"
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
	h.tt.SetClipboard("selection was not copied after list-map inspection")
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/named · list-map-keys") || len(h.w.schemaLocations) != 1 || strings.Contains(h.w.schemaText, "Inspection is incomplete") {
		t.Fatal("list-map inspection missed repeated identities or conflated distinct empty, Unicode or delimited tuples")
	}
	for _, private := range []string{"PRIVATE_DUPLICATE_KEY", "PRIVATE_NONKEY_A", "PRIVATE_NONKEY_B", "PRIVATE_NONKEY_C", "船🚢", "a|b", "b|c"} {
		if strings.Contains(h.w.schemaText+h.w.schemaStatus, private) {
			t.Fatal("list-map assistance echoed a key tuple or non-key value")
		}
		for _, location := range h.w.schemaLocations {
			if strings.Contains(location.label, private) {
				t.Fatal("list-map source label echoed a key tuple or non-key value")
			}
		}
	}
	// Copy from the retained selected endpoint. Tab would select all and could
	// not demonstrate that Check preserved the prior native selection.
	editorAfter, editorOK := h.tt.Find("Manifest editor")
	x := editorAfter.X + selectedCaret.X - editorBefore.X + 0.25
	y := editorAfter.Y + selectedCaret.Y - editorBefore.Y + selectedCaret.H/2
	if !editorOK || x < editorAfter.X || x >= editorAfter.X+editorAfter.W || y < editorAfter.Y || y >= editorAfter.Y+editorAfter.H {
		t.Fatal("list-map inspection did not retain a visible selected endpoint")
	}
	h.tt.RightClickAt(x, y)
	if err := h.tt.ChooseMenuItem("Copy"); err != nil {
		t.Fatal(err)
	}
	if h.tt.Clipboard() != selected || h.w.editor != buffer || h.w.editorText() != before || h.w.draftIdentity() != identity || snapshot.String() != before || original.String() != schemaDraft || h.w.plan != nil || writes.Load() != 0 {
		t.Fatal("list-map inspection changed selection, source, snapshot, identity, plan or server")
	}
	target := chooseDiagnostic(h, "/spec/named")
	if !target.position.Exact || !strings.HasPrefix(string([]rune(before)[target.position.RuneOffset:]), "named:") {
		t.Fatal("list-map diagnostic did not retain the parent array's original Unicode source coordinate")
	}
	h.click("Go to location")
	if !h.tt.Focused("Manifest editor") || h.w.editorText() != before || h.w.plan != nil {
		t.Fatal("list-map navigation changed source or did not focus its parent array")
	}
	saveNativeScreenshot(t, h.tt)
	const correctedLine = `named: [{name: PRIVATE_DUPLICATE_KEY, value: PRIVATE_NONKEY_A}, {name: "中文船🚢", value: PRIVATE_NONKEY_B}, {name: "", value: PRIVATE_NONKEY_C}]`
	// Reuse actual rune navigation with Copy verification, which also crosses
	// soft wraps. Shift+End would select only the current visual row.
	selectNativeSchemaEnumLine(t, h.tt, originalLine)
	h.tt.Type(correctedLine)
	corrected := h.w.editorText()
	obj, err := manifest.Decode([]byte(corrected))
	if err != nil {
		t.Fatal(err)
	}
	named, found, err := unstructured.NestedSlice(obj.Object, "spec", "named")
	wantNamed := []any{
		map[string]any{"name": "PRIVATE_DUPLICATE_KEY", "value": "PRIVATE_NONKEY_A"},
		map[string]any{"name": "中文船🚢", "value": "PRIVATE_NONKEY_B"},
		map[string]any{"name": "", "value": "PRIVATE_NONKEY_C"},
	}
	if err != nil || !found || !reflect.DeepEqual(named, wantNamed) {
		t.Fatal("native list-map correction lost empty/Unicode identities, order or non-key fields")
	}
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if len(h.w.schemaLocations) != 0 || !strings.Contains(h.w.schemaText, "No supported structural or constraint hints found.") || strings.Contains(h.w.schemaText, "Inspection is incomplete") || h.w.editorText() != corrected || h.w.plan != nil {
		t.Fatal("list-map correction was not rechecked using complete exact string tuples")
	}
	// All history assertions remain in the same mounted widget and buffer.
	focusMultipleOfEditorByKeyboard(h)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != before {
		t.Fatal("list-map inspection reset Undo of the source correction")
	}
	h.tt.SetClipboard("list-map correction selection was not restored")
	h.tt.Key(ui.Cmd, ui.KeyC)
	if h.tt.Clipboard() != originalLine {
		t.Fatal("list-map Undo did not restore the complete original source selection")
	}
	h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if h.w.editorText() != corrected {
		t.Fatal("list-map inspection reset Redo of the source correction")
	}
	duplicate := strings.Replace(corrected, `zone: "é"`, `zone: "é"`, 1)
	if duplicate == corrected {
		t.Fatal("test did not create an exact duplicate from distinct Unicode tuples")
	}
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(duplicate)
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/scoped · list-map-keys") || len(h.w.schemaLocations) != 1 || strings.Contains(h.w.schemaText, "Inspection is incomplete") || strings.Contains(h.w.schemaText+h.w.schemaStatus, "船🚢") || h.w.editorText() != duplicate || h.w.plan != nil {
		t.Fatal("list-map inspection missed an exact Unicode tuple duplicate or changed source/privacy")
	}
	focusMultipleOfEditorByKeyboard(h)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != corrected {
		t.Fatal("checking a second list-map draft broke its Undo")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != before {
		t.Fatal("second list-map inspection discarded the preceding correction history")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != schemaDraft || h.w.editor != buffer || original.String() != schemaDraft || snapshot.String() != before || h.w.plan != nil || writes.Load() != 0 {
		t.Fatal("list-map inspection discarded original history or mutated retained state")
	}
}

func TestNativeSchemaListMapMinimumWindowAndIncomplete(t *testing.T) {
	const prefix = "  malformed:\n  - {name: PRIVATE_LATE_DUPLICATE, value: first}\n  - {name: PRIVATE_LATE_DUPLICATE, value: second}\n"
	entryBudget := prefix
	for i := 2; i <= schemaassist.MaxListMapItems; i++ {
		entryBudget += fmt.Sprintf("  - {name: item-%d, value: PRIVATE_ENTRY_VALUE}\n", i)
	}
	// Exercise the decoded key byte cap using many short source lines. This
	// does not claim arbitrary giant non-clipboard Type/IME is now bounded.
	longKey := strings.Repeat(strings.Repeat("x", 127)+"\n", schemaassist.MaxListMapValueBytes/128) + "x"
	const longMarker = "PRIVATE_OVERSIZE_KEY"
	longKey = longMarker + longKey[len(longMarker):]
	keyBudget := prefix + "  - name: |-\n      " + strings.ReplaceAll(longKey, "\n", "\n      ") + "\n    value: PRIVATE_ENTRY_VALUE\n"
	malformedMetadata := strings.Replace(nativeListMapFieldSchema, `["name"]`, `["name", "PRIVATE_SCHEMA_LIST_KEY", 123456789]`, 1)
	for _, tc := range []struct {
		name, metadata, field string
		structural            []string
		wantItems             int
		wantKeyBytes          int
	}{
		{"late-invalid-identities-after-duplicate", nativeListMapFieldSchema, prefix + "  - {value: PRIVATE_MISSING_KEY}\n  - {name: null}\n  - {name: 123456789}\n  - PRIVATE_NONOBJECT\n", []string{"/spec/malformed/2/name · required", "/spec/malformed/3/name · type", "/spec/malformed/4/name · type", "/spec/malformed/5 · type"}, 6, 0},
		{"late-malformed-metadata-with-wrong-type", malformedMetadata, "  malformed: PRIVATE_WRONG_ARRAY_TYPE\n", []string{"/spec/malformed · type"}, 0, 0},
		{"entry-budget-after-duplicate", nativeListMapFieldSchema, entryBudget, nil, schemaassist.MaxListMapItems + 1, 0},
		{"key-byte-budget-after-duplicate", nativeListMapFieldSchema, keyBudget, nil, 3, schemaassist.MaxListMapValueBytes + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, writes := nativeMultipleOfHarness(t, fmt.Sprintf(nativeListMapIncompleteDocument, tc.metadata))
			h.tt.SetSize(1100, 700)
			buffer, original := h.w.editor, h.w.editor.Snapshot()
			draft := "# 中文 🚢 é — list identities stay in the editor\n" + schemaDraft + "  checked: [{name: PRIVATE_CHECKED_KEY, value: first}, {name: PRIVATE_CHECKED_KEY, value: second}]\n" + tc.field
			h.click("Manifest editor")
			h.tt.Key(ui.Cmd, ui.KeyA)
			h.tt.Type(draft)
			snapshot, identity := h.w.editor.Snapshot(), h.w.draftIdentity()
			if tc.wantItems != 0 {
				obj, err := manifest.Decode([]byte(h.w.editorText()))
				if err != nil {
					t.Fatal(err)
				}
				items, found, err := unstructured.NestedSlice(obj.Object, "spec", "malformed")
				if err != nil || !found || len(items) != tc.wantItems {
					t.Fatal("native incomplete fixture did not retain every advertised list entry")
				}
				if tc.wantKeyBytes != 0 {
					value, valid := items[2].(map[string]any)["name"].(string)
					if !valid || value != longKey || len(value) != tc.wantKeyBytes {
						t.Fatal("native list-map budget fixture lost the complete oversized decoded key")
					}
				}
			}
			h.click("Check draft")
			h.pump(func() bool { return !h.w.schemaActive })
			if !strings.Contains(h.w.schemaText, "/spec/checked · list-map-keys") || !strings.Contains(h.w.schemaText, "Inspection is incomplete") || strings.Contains(h.w.schemaText, "/spec/malformed · list-map-keys") || len(h.w.schemaLocations) != 1+len(tc.structural) {
				t.Fatal("late invalid metadata/identities or budget emitted a premature duplicate hint or lost incompleteness/structural checks")
			}
			for _, diagnostic := range tc.structural {
				if !strings.Contains(h.w.schemaText, diagnostic) {
					t.Fatalf("incomplete list identity lost structural diagnostic %s", diagnostic)
				}
			}
			for _, private := range []string{"PRIVATE_CHECKED_KEY", "PRIVATE_LATE_DUPLICATE", "PRIVATE_MISSING_KEY", "PRIVATE_NONOBJECT", "PRIVATE_WRONG_ARRAY_TYPE", "PRIVATE_SCHEMA_LIST_KEY", "PRIVATE_ENTRY_VALUE", longMarker, "123456789"} {
				if strings.Contains(h.w.schemaText+h.w.schemaStatus, private) {
					t.Fatal("incomplete list-map assistance echoed private metadata or a draft value")
				}
				for _, location := range h.w.schemaLocations {
					if strings.Contains(location.label, private) {
						t.Fatal("incomplete list-map location echoed private metadata or a draft value")
					}
				}
			}
			if h.w.editor != buffer || h.w.editorText() != draft || h.w.draftIdentity() != identity || snapshot.String() != draft || original.String() != schemaDraft || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("incomplete list-map inspection changed source, snapshot, identity, plan or server")
			}
			for _, label := range []string{"Manifest editor", "Check draft", "Clear schema", "Schema location", "Go to location", "Schema assistance", "Preview edit", "Apply", "Close detail"} {
				r, ok := h.tt.Find(label)
				if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 1088 || r.Y+r.H > 700 {
					t.Errorf("%s outside minimum window: %+v found=%v", label, r, ok)
				}
			}
			target := chooseDiagnostic(h, "/spec/checked")
			if !target.position.Exact || !strings.HasPrefix(string([]rune(draft)[target.position.RuneOffset:]), "checked:") {
				t.Fatal("incomplete list-map inspection lost the established duplicate's parent location")
			}
			h.click("Go to location")
			caret, ok := h.tt.TextCaret()
			editor, _ := h.tt.Find("Manifest editor")
			if !ok || caret.Y < editor.Y || caret.Y > editor.Y+editor.H || !h.tt.Focused("Manifest editor") || h.w.editorText() != draft || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("minimum-window list-map navigation hid its target or changed source/server")
			}
			saveNativeScreenshot(t, h.tt)
			h.tt.Key(ui.Cmd, ui.KeyZ)
			if h.w.editorText() != schemaDraft {
				t.Fatal("incomplete list-map inspection broke mounted native Undo")
			}
			h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
			if h.w.editorText() != draft || h.w.editor != buffer || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("incomplete list-map inspection broke mounted native Redo or mutated the server")
			}
		})
	}
}
