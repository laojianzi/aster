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

const nativeIntegerMapField = `{"type":"array","x-kubernetes-list-type":"map","x-kubernetes-list-map-keys":["port","protocol"],"items":{"type":"object","required":["port","protocol"],"properties":{"port":{"type":"integer"},"protocol":{"type":"string"},"value":{"type":"string"}}}}`

func TestNativeSchemaIntegerMapKeysHistoryAndNavigation(t *testing.T) {
	doc := strings.Replace(nativeListMapDocument, nativeListMapFieldSchema, nativeIntegerMapField, 1)
	h, writes := nativeMultipleOfHarness(t, doc)
	buffer, original := h.w.editor, h.w.editor.Snapshot()
	const line = `named: [{port: 9007199254740992, protocol: TCP, value: PRIVATE_A}, {port: 9007199254740993, protocol: TCP, value: PRIVATE_B}, {port: 9007199254740993, protocol: TCP, value: PRIVATE_C}, {port: -9223372036854775808, protocol: UDP}, {port: 9223372036854775807, protocol: UDP}]`
	draft := "# 中文 🚢 é — integer identities\n" + schemaDraft + "  " + line + "\n"
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(draft)
	snapshot, identity := h.w.editor.Snapshot(), h.w.draftIdentity()
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "required string/int64 map-list identities") || strings.Contains(h.w.schemaText, "string-key map-list identities") {
		t.Fatal("native report advertises an outdated map-list identity scope")
	}
	if len(h.w.schemaLocations) != 1 || !strings.Contains(h.w.schemaText, "/spec/named · list-map-keys") || strings.Contains(h.w.schemaText, "Inspection is incomplete") || strings.Count(h.w.schemaText, "List entries repeat the advertised key identity.") != 1 {
		t.Fatal("integer map inspection missed the repeated mixed tuple")
	}
	for _, private := range []string{"PRIVATE_", "9007199254740993", "9223372036854775807", "TCP", "UDP"} {
		if strings.Contains(h.w.schemaText+h.w.schemaStatus, private) {
			t.Fatal("integer identity report disclosed a value")
		}
	}
	if h.w.editor != buffer || h.w.editorText() != draft || h.w.draftIdentity() != identity || snapshot.String() != draft || h.w.plan != nil || writes.Load() != 0 {
		t.Fatal("integer inspection changed draft or server")
	}
	target := chooseDiagnostic(h, "/spec/named")
	if !target.position.Exact || !strings.HasPrefix(string([]rune(draft)[target.position.RuneOffset:]), "named:") {
		t.Fatal("integer identity lost Unicode parent source location")
	}
	h.click("Go to location")
	if !h.tt.Focused("Manifest editor") {
		t.Fatal("integer location did not focus source")
	}
	selectNativeSchemaEnumLine(t, h.tt, line)
	correctedLine := strings.Replace(line, "9007199254740993, protocol: TCP, value: PRIVATE_C", "9007199254740993, protocol: UDP, value: PRIVATE_C", 1)
	h.tt.Type(correctedLine)
	corrected := h.w.editorText()
	obj, err := manifest.Decode([]byte(corrected))
	if err != nil {
		t.Fatal(err)
	}
	values, found, err := unstructured.NestedSlice(obj.Object, "spec", "named")
	if err != nil || !found || values[0].(map[string]any)["port"] != int64(9007199254740992) || values[1].(map[string]any)["port"] != int64(9007199254740993) || values[3].(map[string]any)["port"] != int64(-9223372036854775808) || values[4].(map[string]any)["port"] != int64(9223372036854775807) {
		t.Fatal("native decoder lost exact signed integer identities")
	}
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "required string/int64 map-list identities") || strings.Contains(h.w.schemaText, "string-key map-list identities") {
		t.Fatal("native report advertises an outdated map-list identity scope")
	}
	if len(h.w.schemaLocations) != 0 || strings.Contains(h.w.schemaText, "Inspection is incomplete") || !strings.Contains(h.w.schemaText, "No supported structural or constraint hints found.") || h.w.plan != nil {
		t.Fatal("integer correction conflated adjacent integers or mixed tuples")
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

func TestNativeSchemaIntegerMapKeysMinimumWindowIncomplete(t *testing.T) {
	const prefix = "  named: [{port: 8080, protocol: TCP}, {port: 8080, protocol: TCP}, "
	many := "  named:\n"
	for i := 0; i <= schemaassist.MaxListMapItems; i++ {
		many += fmt.Sprintf("  - {port: %d, protocol: TCP}\n", i)
	}
	for _, tc := range []struct{ name, field, schema string }{
		{"late-quoted-key", prefix + `{port: "PRIVATE_INTEGER", protocol: TCP}]` + "\n", nativeIntegerMapField},
		{"late-null-key", prefix + "{port: null, protocol: TCP}]\n", nativeIntegerMapField},
		{"late-missing-key", prefix + "{protocol: TCP}]\n", nativeIntegerMapField},
		{"late-fraction", prefix + "{port: 8080.5, protocol: TCP}]\n", nativeIntegerMapField},
		{"item-limit", many, nativeIntegerMapField},
		{"optional-default-key", prefix + "{port: 9090, protocol: UDP}]\n", strings.Replace(strings.Replace(nativeIntegerMapField, `"required":["port","protocol"]`, `"required":["protocol"]`, 1), `"type":"integer"`, `"type":"integer","default":8080`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, writes := nativeMultipleOfHarness(t, strings.Replace(nativeListMapDocument, nativeListMapFieldSchema, tc.schema, 1))
			h.tt.SetSize(1100, 700)
			buffer, original := h.w.editor, h.w.editor.Snapshot()
			draft := "# 中文 🚢\n" + schemaDraft + tc.field
			h.click("Manifest editor")
			h.tt.Key(ui.Cmd, ui.KeyA)
			h.tt.Type(draft)
			identity := h.w.draftIdentity()
			h.click("Check draft")
			h.pump(func() bool { return !h.w.schemaActive })
			if !strings.Contains(h.w.schemaText, "required string/int64 map-list identities") || strings.Contains(h.w.schemaText, "string-key map-list identities") {
				t.Fatal("native report advertises an outdated map-list identity scope")
			}
			if !strings.Contains(h.w.schemaText, "Inspection is incomplete") || strings.Contains(h.w.schemaText, "· list-map-keys") || strings.Contains(h.w.schemaText+h.w.schemaStatus, "PRIVATE_INTEGER") {
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
