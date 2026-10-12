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

const nativePatternDocument = `{
 "openapi":"3.0.0","components":{"schemas":{
  "Pod":{"type":"object","x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"Pod"}],
   "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},
    "metadata":{"type":"object","additionalProperties":true},
    "spec":{"type":"object","properties":{
     "containers":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"}}}},
     "slug":{"type":"string","pattern":"^[a-z][a-z0-9-]*$"},
     "contains":{"type":"string","pattern":"ok"},
     "unicode":{"type":"string","pattern":"船"},
     "empty":{"type":"string","pattern":"^$"},
     "nullable":{"type":"string","nullable":true,"pattern":"ok"}
    }}
   }}
 }}}
`

const nativePatternIncompleteDocument = `{
 "openapi":"3.0.0","components":{"schemas":{
  "Pod":{"type":"object","x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"Pod"}],
   "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},
    "metadata":{"type":"object","additionalProperties":true},
    "spec":{"type":"object","properties":{
     "containers":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"}}}},
     "checked":{"type":"string","pattern":"^PRIVATE_SCHEMA_CHECKED$"},
     "malformed":{"type":"string","pattern":%s}
    }}
   }}
 }}}
`

func TestNativeSchemaPatternPreservesDraftAndHistory(t *testing.T) {
	// Share the existing document-backed Schema transport and actual editor.
	h, writes := nativeMultipleOfHarness(t, nativePatternDocument)
	buffer, original := h.w.editor, h.w.editor.Snapshot()
	const selected = "中文 🚢\n# café\n"
	before := "# " + selected + schemaDraft + "  slug: PRIVATE_INVALID_SLUG\n  contains: prefix-ok-suffix\n  unicode: 中文船🚢\n  empty: ''\n  nullable: null\n"
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
	if !strings.Contains(h.w.schemaText, "/spec/slug · pattern") || len(h.w.schemaLocations) != 1 || strings.Contains(h.w.schemaText, "Inspection is incomplete") {
		t.Fatal("anchored mismatch was missed or valid unanchored, Unicode, empty or nullable input was rejected")
	}
	for _, private := range []string{"PRIVATE_INVALID_SLUG", "^[a-z][a-z0-9-]*$", "prefix-ok-suffix", "中文船🚢"} {
		if strings.Contains(h.w.schemaText+h.w.schemaStatus, private) {
			t.Fatal("pattern assistance echoed an input value or schema expression")
		}
		for _, location := range h.w.schemaLocations {
			if strings.Contains(location.label, private) {
				t.Fatal("pattern source label echoed an input value or expression")
			}
		}
	}
	// The native context menu preserves the selected endpoint. Returning by
	// Tab instead would select all and could not prove the previous selection.
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
		t.Fatal("pattern inspection changed selection, input, snapshot, identity, plan or server")
	}
	target := chooseDiagnostic(h, "/spec/slug")
	if !target.position.Exact || !strings.HasPrefix(string([]rune(before)[target.position.RuneOffset:]), "slug:") {
		t.Fatal("pattern diagnostic lost its original Unicode source coordinate")
	}
	h.click("Go to location")
	if !h.tt.Focused("Manifest editor") || h.w.editorText() != before || h.w.plan != nil {
		t.Fatal("pattern navigation changed input or did not focus its actual location")
	}
	saveNativeScreenshot(t, h.tt)
	h.tt.Key(ui.Shift, ui.KeyEnd)
	h.tt.Type("slug: native-fixed")
	corrected := h.w.editorText()
	obj, err := manifest.Decode([]byte(corrected))
	if err != nil {
		t.Fatal(err)
	}
	slug, found, err := unstructured.NestedString(obj.Object, "spec", "slug")
	if err != nil || !found || slug != "native-fixed" {
		t.Fatal("native source correction did not reach the real manifest decoder")
	}
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if len(h.w.schemaLocations) != 0 || !strings.Contains(h.w.schemaText, "No supported structural or constraint hints found.") || strings.Contains(h.w.schemaText, "Inspection is incomplete") || h.w.editorText() != corrected || h.w.plan != nil {
		t.Fatal("corrected pattern or valid unanchored, Unicode, empty or nullable value was rejected")
	}
	// These history assertions all keep the source editor mounted.
	focusMultipleOfEditorByKeyboard(h)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != before {
		t.Fatal("pattern inspection reset Undo of the source correction")
	}
	h.tt.SetClipboard("correction selection was not restored")
	h.tt.Key(ui.Cmd, ui.KeyC)
	if h.tt.Clipboard() != "slug: PRIVATE_INVALID_SLUG" {
		t.Fatal("Undo did not restore the original corrected-line selection")
	}
	h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if h.w.editorText() != corrected {
		t.Fatal("pattern inspection reset Redo of the source correction")
	}
	unmatched := strings.Replace(corrected, "contains: prefix-ok-suffix", "contains: PRIVATE_MISSING_TOKEN🚢", 1)
	unmatched = strings.Replace(unmatched, "unicode: 中文船🚢", "unicode: 中文🚢", 1)
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(unmatched)
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/contains · pattern") || !strings.Contains(h.w.schemaText, "/spec/unicode · pattern") || len(h.w.schemaLocations) != 2 || strings.Contains(h.w.schemaText, "Inspection is incomplete") || strings.Contains(h.w.schemaText+h.w.schemaStatus, "PRIVATE_MISSING_TOKEN") || h.w.editorText() != unmatched || h.w.plan != nil {
		t.Fatal("supported BMP patterns did not diagnose unmatched values containing supplementary Unicode")
	}
	focusMultipleOfEditorByKeyboard(h)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != corrected {
		t.Fatal("checking the second pattern draft broke its Undo")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != before {
		t.Fatal("second pattern inspection discarded the preceding correction history")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != schemaDraft || h.w.editor != buffer || original.String() != schemaDraft || snapshot.String() != before || h.w.plan != nil || writes.Load() != 0 {
		t.Fatal("pattern inspection discarded original history or mutated retained state")
	}
}

func TestNativeSchemaPatternMinimumWindowAndIncomplete(t *testing.T) {
	// Exercise the decoded string budget without a giant single native line.
	// This does not claim to solve upstream arbitrary Type/IME layout limits.
	longValue := strings.Repeat(strings.Repeat("x", 127)+"\n", schemaassist.MaxPatternInputBytes/128) + "x"
	const largeValueMarker = "PRIVATE_LARGE_DRAFT"
	longValue = largeValueMarker + longValue[len(largeValueMarker):]
	longField := "  malformed: |-\n    " + strings.ReplaceAll(longValue, "\n", "\n    ") + "\n"
	for _, tc := range []struct {
		name, metadata, field string
		wantType              bool
		wantInputBytes        int
	}{
		{"non-string-metadata", "123456789", "  malformed: PRIVATE_DRAFT_VALUE\n", false, 0},
		{"invalid-regex", `"[PRIVATE_SCHEMA_PATTERN"`, "  malformed: PRIVATE_DRAFT_VALUE\n", false, 0},
		{"unsupported-inline-flags", `"(?i)PRIVATE_SCHEMA_PATTERN"`, "  malformed: PRIVATE_DRAFT_VALUE\n", false, 0},
		{"program-budget", `"((ab){20}){20}"`, "  malformed: PRIVATE_DRAFT_VALUE\n", false, 0},
		{"type-and-malformed-pattern", `"[PRIVATE_SCHEMA_PATTERN"`, "  malformed: 123456789\n", true, 0},
		{"input-budget", `"^PRIVATE_SCHEMA_PATTERN$"`, longField, false, schemaassist.MaxPatternInputBytes + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, writes := nativeMultipleOfHarness(t, fmt.Sprintf(nativePatternIncompleteDocument, tc.metadata))
			h.tt.SetSize(1100, 700)
			buffer, original := h.w.editor, h.w.editor.Snapshot()
			draft := "# 中文 🚢 é — private expressions and values stay in the editor\n" + schemaDraft + "  checked: PRIVATE_CHECKED_DRAFT\n" + tc.field
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
			if !strings.Contains(h.w.schemaText, "/spec/checked · pattern") || !strings.Contains(h.w.schemaText, "Inspection is incomplete") || strings.Contains(h.w.schemaText, "/spec/malformed · pattern") || strings.Contains(h.w.schemaText, "/spec/malformed · type") != tc.wantType || len(h.w.schemaLocations) != wantLocations {
				t.Fatal("unsupported pattern lost incomplete disclosure or invented a secondary violation")
			}
			for _, private := range []string{"PRIVATE_SCHEMA_CHECKED", "PRIVATE_CHECKED_DRAFT", "PRIVATE_SCHEMA_PATTERN", "PRIVATE_DRAFT_VALUE", largeValueMarker, "123456789", "((ab){20}){20}"} {
				if strings.Contains(h.w.schemaText+h.w.schemaStatus, private) {
					t.Fatal("incomplete pattern assistance echoed an expression or draft value")
				}
				for _, location := range h.w.schemaLocations {
					if strings.Contains(location.label, private) {
						t.Fatal("incomplete pattern source label echoed an expression or draft value")
					}
				}
			}
			if h.w.editor != buffer || h.w.editorText() != draft || h.w.draftIdentity() != identity || snapshot.String() != draft || original.String() != schemaDraft || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("incomplete pattern inspection changed input, snapshot, identity, plan or server")
			}
			for _, label := range []string{"Manifest editor", "Check draft", "Clear schema", "Schema location", "Go to location", "Schema assistance", "Preview edit", "Apply", "Close detail"} {
				r, ok := h.tt.Find(label)
				if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 1088 || r.Y+r.H > 700 {
					t.Errorf("%s outside minimum window: %+v found=%v", label, r, ok)
				}
			}
			target := chooseDiagnostic(h, "/spec/checked")
			if !target.position.Exact || !strings.HasPrefix(string([]rune(draft)[target.position.RuneOffset:]), "checked:") {
				t.Fatal("incomplete pattern inspection lost the exact established mismatch location")
			}
			h.click("Go to location")
			caret, ok := h.tt.TextCaret()
			editor, _ := h.tt.Find("Manifest editor")
			if !ok || caret.Y < editor.Y || caret.Y > editor.Y+editor.H || !h.tt.Focused("Manifest editor") || h.w.editorText() != draft || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("minimum-window pattern navigation hid its target or changed input/server")
			}
			saveNativeScreenshot(t, h.tt)
			h.tt.Key(ui.Cmd, ui.KeyZ)
			if h.w.editorText() != schemaDraft {
				t.Fatal("incomplete pattern inspection broke mounted native Undo")
			}
			h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
			if h.w.editorText() != draft || h.w.editor != buffer || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("incomplete pattern inspection broke mounted Redo or mutated the server")
			}
		})
	}
}
