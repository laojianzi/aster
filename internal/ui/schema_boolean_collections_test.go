package uiworkbench

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/schemaassist"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const nativeBooleanSetField = `{"type":"array","x-kubernetes-list-type":"set","items":{"type":"boolean"}}`
const nativeBooleanMapField = `{"type":"array","x-kubernetes-list-type":"map","x-kubernetes-list-map-keys":["active","port","zone"],"items":{"type":"object","required":["active","port","zone"],"properties":{"active":{"type":"boolean"},"port":{"type":"integer"},"zone":{"type":"string"},"value":{"type":"string"}}}}`

func TestNativeSchemaBooleanCollectionsHistoryAndNavigation(t *testing.T) {
	for _, tc := range []struct{ name, doc, field, line, corrected, code, message string }{
		{"set", strings.Replace(nativeListSetDocument, nativeListSetFieldSchema, nativeBooleanSetField, 1), "values", `values: [false, false]`, `values: [false, true]`, "list-set", "List entries repeat the advertised boolean value."},
		{"map", strings.Replace(nativeListMapDocument, nativeListMapFieldSchema, nativeBooleanMapField, 1), "named", `named: [{active: false, port: 9007199254740993, zone: PRIVATE_ZONE, value: PRIVATE_A}, {active: false, port: 9007199254740993, zone: PRIVATE_ZONE, value: PRIVATE_B}]`, `named: [{active: false, port: 9007199254740993, zone: PRIVATE_ZONE, value: PRIVATE_A}, {active: true, port: 9007199254740993, zone: PRIVATE_ZONE, value: PRIVATE_B}]`, "list-map-keys", "List entries repeat the advertised key identity."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, writes := nativeMultipleOfHarness(t, tc.doc)
			buffer, original := h.w.editor, h.w.editor.Snapshot()
			draft := "# 中文 🚢 é — boolean identities\n" + schemaDraft + "  " + tc.line + "\n"
			h.click("Manifest editor")
			h.tt.Key(ui.Cmd, ui.KeyA)
			h.tt.Type(draft)
			snapshot, identity := h.w.editor.Snapshot(), h.w.draftIdentity()
			h.click("Check draft")
			h.pump(func() bool { return !h.w.schemaActive })
			if len(h.w.schemaLocations) != 1 || !strings.Contains(h.w.schemaText, "/spec/"+tc.field+" · "+tc.code) || strings.Count(h.w.schemaText, tc.message) != 1 || strings.Contains(h.w.schemaText, "Inspection is incomplete") {
				t.Fatal("boolean identity did not produce one complete private parent hint")
			}
			if strings.Contains(h.w.schemaText+h.w.schemaStatus, "PRIVATE_") || strings.Contains(h.w.schemaText, "9007199254740993") {
				t.Fatal("identity report disclosed values")
			}
			if h.w.editor != buffer || h.w.editorText() != draft || h.w.draftIdentity() != identity || snapshot.String() != draft || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("inspection changed source, identity, snapshot or server")
			}
			target := chooseDiagnostic(h, "/spec/"+tc.field)
			if !target.position.Exact || !strings.HasPrefix(string([]rune(draft)[target.position.RuneOffset:]), tc.field+":") {
				t.Fatal("boolean source navigation lost exact Unicode location")
			}
			h.click("Go to location")
			if !h.tt.Focused("Manifest editor") {
				t.Fatal("source navigation did not focus editor")
			}
			selectNativeSchemaEnumLine(t, h.tt, tc.line)
			h.tt.Type(tc.corrected)
			corrected := h.w.editorText()
			obj, err := manifest.Decode([]byte(corrected))
			if err != nil {
				t.Fatal(err)
			}
			values, found, err := unstructured.NestedSlice(obj.Object, "spec", tc.field)
			if err != nil || !found || len(values) != 2 {
				t.Fatal("correction lost the collection")
			}
			if tc.name == "set" {
				if values[0] != false || values[1] != true {
					t.Fatal("boolean values were coerced")
				}
			} else {
				for i, raw := range values {
					v := raw.(map[string]any)
					if v["active"] != (i == 1) || v["port"] != int64(9007199254740993) || v["zone"] != "PRIVATE_ZONE" {
						t.Fatal("mixed tuple correction changed another component")
					}
				}
			}
			h.click("Check draft")
			h.pump(func() bool { return !h.w.schemaActive })
			if len(h.w.schemaLocations) != 0 || strings.Contains(h.w.schemaText, "Inspection is incomplete") || !strings.Contains(h.w.schemaText, "No supported structural or constraint hints found.") || h.w.plan != nil {
				t.Fatal("false and true were conflated")
			}
			focusMultipleOfEditorByKeyboard(h)
			h.tt.Key(ui.Cmd, ui.KeyZ)
			if h.w.editorText() != draft {
				t.Fatal("checking reset correction Undo")
			}
			h.tt.SetClipboard("not restored")
			h.tt.Key(ui.Cmd, ui.KeyC)
			if h.tt.Clipboard() != tc.line {
				t.Fatal("Undo lost original selection")
			}
			h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
			if h.w.editorText() != corrected {
				t.Fatal("correction Redo failed")
			}
			saveNativeScreenshot(t, h.tt)
			h.tt.Key(ui.Cmd, ui.KeyZ)
			h.tt.Key(ui.Cmd, ui.KeyZ)
			if h.w.editorText() != schemaDraft || h.w.editor != buffer || original.String() != schemaDraft || snapshot.String() != draft || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("boolean assistance lost mounted history or wrote to server")
			}
		})
	}
}

func TestNativeSchemaBooleanCollectionsMinimumWindowIncomplete(t *testing.T) {
	many := "values: [" + strings.Repeat("false,", schemaassist.MaxListSetItems) + "false]"
	for _, tc := range []struct{ name, line, field, doc string }{
		{"set-quoted", `values: [false, false, "PRIVATE_TRUE"]`, nativeBooleanSetField, nativeListSetDocument},
		{"set-number", `values: [false, false, 1]`, nativeBooleanSetField, nativeListSetDocument},
		{"set-null", `values: [false, false, null]`, nativeBooleanSetField, nativeListSetDocument},
		{"set-limit", many, nativeBooleanSetField, nativeListSetDocument},
		{"set-nullable", `values: [false, false]`, strings.Replace(nativeBooleanSetField, `"type":"boolean"`, `"type":"boolean","nullable":true`, 1), nativeListSetDocument},
		{"map-missing", `named: [{active: false, port: 0, zone: x}, {active: false, port: 0, zone: x}, {port: 0, zone: x}]`, nativeBooleanMapField, nativeListMapDocument},
		{"map-null", `named: [{active: false, port: 0, zone: x}, {active: false, port: 0, zone: x}, {active: null, port: 0, zone: x}]`, nativeBooleanMapField, nativeListMapDocument},
		{"map-number", `named: [{active: false, port: 0, zone: x}, {active: false, port: 0, zone: x}, {active: 0, port: 0, zone: x}]`, nativeBooleanMapField, nativeListMapDocument},
		{"map-default-only", `named: [{active: false, port: 0, zone: x}, {active: false, port: 0, zone: x}]`, strings.Replace(strings.Replace(nativeBooleanMapField, `"required":["active","port","zone"]`, `"required":["port","zone"]`, 1), `"type":"boolean"`, `"type":"boolean","default":true`, 1), nativeListMapDocument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := nativeListSetFieldSchema
			if strings.HasPrefix(tc.name, "map-") {
				old = nativeListMapFieldSchema
			}
			h, writes := nativeMultipleOfHarness(t, strings.Replace(tc.doc, old, tc.field, 1))
			h.tt.SetSize(1100, 700)
			buffer, original := h.w.editor, h.w.editor.Snapshot()
			draft := "# 中文 🚢\n" + schemaDraft + "  " + tc.line + "\n"
			h.click("Manifest editor")
			h.tt.Key(ui.Cmd, ui.KeyA)
			h.tt.Type(draft)
			identity := h.w.draftIdentity()
			h.click("Check draft")
			h.pump(func() bool { return !h.w.schemaActive })
			if !strings.Contains(h.w.schemaText, "Inspection is incomplete") || strings.Contains(h.w.schemaText, "· list-set") || strings.Contains(h.w.schemaText, "· list-map-keys") || strings.Contains(h.w.schemaText+h.w.schemaStatus, "PRIVATE_") {
				t.Fatal("incomplete boolean identity leaked an early duplicate or value")
			}
			if h.w.editor != buffer || h.w.editorText() != draft || h.w.draftIdentity() != identity || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("incomplete check changed source or server")
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
				t.Fatal("incomplete check lost Undo")
			}
			h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
			if h.w.editorText() != draft || original.String() != schemaDraft || h.w.editor != buffer || writes.Load() != 0 {
				t.Fatal("incomplete check lost Redo or immutable snapshot")
			}
		})
	}
}

func TestNativeSchemaBooleanCollectionsStaleResults(t *testing.T) {
	for _, isMap := range []bool{false, true} {
		for _, change := range []string{"draft", "detail", "expiry", "pointer", "tab"} {
			name := "set/" + change
			if isMap {
				name = "map/" + change
			}
			t.Run(name, func(t *testing.T) {
				doc, line := strings.Replace(nativeListSetDocument, nativeListSetFieldSchema, nativeBooleanSetField, 1), "  values: [false, false]\n"
				if isMap {
					doc = strings.Replace(nativeListMapDocument, nativeListMapFieldSchema, nativeBooleanMapField, 1)
					line = "  named: [{active: false, port: 0, zone: x}, {active: false, port: 0, zone: x}]\n"
				}
				h, writes := nativeMultipleOfHarness(t, doc)
				h.click("Manifest editor")
				h.tt.Key(ui.Cmd, ui.KeyA)
				h.tt.Type(schemaDraft + line)
				h.click("Check draft")
				var complete func()
				select {
				case complete = <-h.updates:
				case <-h.ctx.Done():
					t.Fatal("no boolean check completion")
				}
				switch change {
				case "draft":
					h.click("Manifest editor")
					h.tt.Type("\n")
				case "detail":
					h.click("Close detail")
				case "expiry":
					h.w.expireConnection(h.w.contextEpoch)
					h.tt.Frame()
				case "pointer":
					h.click("Schema field pointer")
					h.tt.Type("x")
				case "tab":
					h.click("YAML")
				}
				before := h.w.editorText()
				complete()
				h.tt.Frame()
				if h.w.schemaText != "" || h.w.schemaActive || h.w.schemaSource != nil || len(h.w.schemaLocations) != 0 || h.w.schemaJump != nil || h.w.editorText() != before || h.w.plan != nil || writes.Load() != 0 {
					t.Fatal("queued boolean result crossed its source/connection boundary")
				}
			})
		}
	}
}
