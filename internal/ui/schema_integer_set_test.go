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

const nativeIntegerSetField = `{"type":"array","x-kubernetes-list-type":"set","items":{"type":"integer"}}`

func TestNativeSchemaIntegerSetHistoryAndNavigation(t *testing.T) {
	doc := strings.Replace(nativeListSetDocument, nativeListSetFieldSchema, nativeIntegerSetField, 1)
	h, writes := nativeMultipleOfHarness(t, doc)
	buffer, original := h.w.editor, h.w.editor.Snapshot()
	const line = `values: [9007199254740992, 9007199254740993, 9007199254740993, -9223372036854775808, 9223372036854775807]`
	draft := "# 中文 🚢 é — integer identities\n" + schemaDraft + "  " + line + "\n"
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(draft)
	snapshot, identity := h.w.editor.Snapshot(), h.w.draftIdentity()
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "string/int64 set-list values") || strings.Contains(h.w.schemaText, "string set-list values") {
		t.Fatal("native report advertises an outdated set-list value scope")
	}
	if len(h.w.schemaLocations) != 1 || !strings.Contains(h.w.schemaText, "/spec/values · list-set") || strings.Contains(h.w.schemaText, "Inspection is incomplete") || strings.Count(h.w.schemaText, "List entries repeat the advertised integer value.") != 1 {
		t.Fatal("integer set inspection missed the repeated integer")
	}
	for _, private := range []string{"PRIVATE_", "9007199254740993", "9223372036854775807", "TCP", "UDP"} {
		if strings.Contains(h.w.schemaText+h.w.schemaStatus, private) {
			t.Fatal("integer identity report disclosed a value")
		}
	}
	if h.w.editor != buffer || h.w.editorText() != draft || h.w.draftIdentity() != identity || snapshot.String() != draft || h.w.plan != nil || writes.Load() != 0 {
		t.Fatal("integer inspection changed draft or server")
	}
	target := chooseDiagnostic(h, "/spec/values")
	if !target.position.Exact || !strings.HasPrefix(string([]rune(draft)[target.position.RuneOffset:]), "values:") {
		t.Fatal("integer identity lost Unicode parent source location")
	}
	h.click("Go to location")
	if !h.tt.Focused("Manifest editor") {
		t.Fatal("integer location did not focus source")
	}
	selectNativeSchemaEnumLine(t, h.tt, line)
	correctedLine := strings.Replace(line, "9007199254740993, -9223372036854775808", "9007199254740994, -9223372036854775808", 1)
	h.tt.Type(correctedLine)
	corrected := h.w.editorText()
	obj, err := manifest.Decode([]byte(corrected))
	if err != nil {
		t.Fatal(err)
	}
	values, found, err := unstructured.NestedSlice(obj.Object, "spec", "values")
	if err != nil || !found || values[0] != int64(9007199254740992) || values[1] != int64(9007199254740993) || values[3] != int64(-9223372036854775808) || values[4] != int64(9223372036854775807) {
		t.Fatal("native decoder lost exact signed integer identities")
	}
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "string/int64 set-list values") || strings.Contains(h.w.schemaText, "string set-list values") {
		t.Fatal("native report advertises an outdated set-list value scope")
	}
	if len(h.w.schemaLocations) != 0 || strings.Contains(h.w.schemaText, "Inspection is incomplete") || !strings.Contains(h.w.schemaText, "No supported structural or constraint hints found.") || h.w.plan != nil {
		t.Fatal("integer correction conflated adjacent integers")
	}
	focusMultipleOfEditorByKeyboard(h)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != draft {
		t.Fatal("integer inspection reset correction Undo")
	}
	h.tt.SetClipboard("selection not restored")
	h.tt.Key(ui.Cmd, ui.KeyC)
	if h.tt.Clipboard() != line {
		t.Fatal("integer correction Undo lost the original selection")
	}
	h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if h.w.editorText() != corrected {
		t.Fatal("integer correction Redo failed")
	}
	saveNativeScreenshot(t, h.tt)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != schemaDraft || h.w.editor != buffer || original.String() != schemaDraft || snapshot.String() != draft || h.w.plan != nil || writes.Load() != 0 {
		t.Fatal("integer assistance lost original mounted history or mutated the server")
	}
}

func TestNativeSchemaIntegerSetMinimumWindowIncomplete(t *testing.T) {
	const prefix = "  values: [8080, 8080, "
	many := "  values: ["
	for i := 0; i <= schemaassist.MaxListSetItems; i++ {
		many += fmt.Sprintf("%d,", i)
	}
	many += "]\n"
	for _, tc := range []struct{ name, field, schema string }{
		{"late-quoted-value", prefix + `"PRIVATE_INTEGER"]` + "\n", nativeIntegerSetField},
		{"late-null-value", prefix + "null]\n", nativeIntegerSetField},
		{"late-fraction", prefix + "8080.5]\n", nativeIntegerSetField},
		{"item-limit", many, nativeIntegerSetField},
		{"nullable-items", prefix + "9090]\n", strings.Replace(nativeIntegerSetField, `"type":"integer"`, `"type":"integer","nullable":true`, 1)},
		{"number-schema", prefix + "9090]\n", strings.Replace(nativeIntegerSetField, `"type":"integer"`, `"type":"number"`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, writes := nativeMultipleOfHarness(t, strings.Replace(nativeListSetDocument, nativeListSetFieldSchema, tc.schema, 1))
			h.tt.SetSize(1100, 700)
			buffer, original := h.w.editor, h.w.editor.Snapshot()
			draft := "# 中文 🚢\n" + schemaDraft + tc.field
			h.click("Manifest editor")
			h.tt.Key(ui.Cmd, ui.KeyA)
			h.tt.Type(draft)
			identity := h.w.draftIdentity()
			h.click("Check draft")
			h.pump(func() bool { return !h.w.schemaActive })
			if !strings.Contains(h.w.schemaText, "string/int64 set-list values") || strings.Contains(h.w.schemaText, "string set-list values") {
				t.Fatal("native report advertises an outdated set-list value scope")
			}
			if !strings.Contains(h.w.schemaText, "Inspection is incomplete") || strings.Contains(h.w.schemaText, "· list-set") || strings.Contains(h.w.schemaText+h.w.schemaStatus, "PRIVATE_INTEGER") {
				t.Fatal("incomplete integer list produced an early hint or disclosed input")
			}
			if h.w.editorText() != draft || h.w.draftIdentity() != identity || h.w.editor != buffer || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("incomplete integer inspection changed source or server")
			}
			for _, label := range []string{"Manifest editor", "Check draft", "Clear schema", "Schema assistance", "Preview edit", "Apply", "Close detail"} {
				r, ok := h.tt.Find(label)
				if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 1088 || r.Y+r.H > 700 {
					t.Errorf("%s outside minimum window: %+v", label, r)
				}
			}
			saveNativeScreenshot(t, h.tt)
			focusMultipleOfEditorByKeyboard(h)
			h.tt.Key(ui.Cmd, ui.KeyZ)
			if h.w.editorText() != schemaDraft {
				t.Fatal("incomplete integer inspection lost Undo")
			}
			h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
			if h.w.editorText() != draft || original.String() != schemaDraft || h.w.editor != buffer || writes.Load() != 0 {
				t.Fatal("incomplete integer inspection lost Redo or snapshot")
			}
		})
	}
}
