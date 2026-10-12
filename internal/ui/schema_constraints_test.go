package uiworkbench

import (
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

// Keep the large bounds as JSON tokens. Constructing this fixture through a
// float64 schema would round away the distinction that the native draft must
// preserve when manifest.Decode returns an int64.
const nativeConstraintsDocument = `{
 "openapi":"3.0.0","components":{"schemas":{
  "Pod":{"type":"object","x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"Pod"}],
   "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},
    "metadata":{"type":"object","additionalProperties":true},
    "spec":{"type":"object","properties":{
     "containers":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"}}}},
     "count":{"type":"integer","minimum":9007199254740993,"maximum":9007199254740993},
     "fraction":{"type":"number","minimum":0.1,"maximum":0.1},
     "mode":{"type":"string","enum":["approved","PRIVATE_ENUM_CHOICE"]},
     "title":{"type":"string","minLength":2,"maxLength":4},
     "items":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":2},
     "attributes":{"type":"object","additionalProperties":{"type":"string"},"minProperties":1,"maxProperties":2},
     "uninspected":{"type":"string","pattern":"PRIVATE_SCHEMA_PATTERN"}
    }}
   }}
 }}}
`

func nativeConstraintsHarness(t *testing.T) (*relationshipHarness, *atomic.Int32) {
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
			fmt.Fprint(w, nativeConstraintsDocument)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return schemaHarness(t, server.URL), &writes
}

func TestNativeSchemaConstraintsPreserveNumbersDraftAndHistory(t *testing.T) {
	h, writes := nativeConstraintsHarness(t)
	buffer, original := h.w.editor, h.w.editor.Snapshot()
	before := "# 中文 🚢 é — original source\n" + schemaDraft + "  count: 9007199254740992\n  fraction: 0.1\n  title: 中文🚢\n  mode: approved\n"
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(before)
	draft := h.w.editor.Snapshot()
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/count · minimum") || len(h.w.schemaLocations) != 1 {
		t.Fatalf("exact lower boundary missing or valid decimal/Unicode rejected: %s", h.w.schemaText)
	}
	if h.w.editor != buffer || h.w.editorText() != before || draft.String() != before || original.String() != schemaDraft || h.w.plan != nil || writes.Load() != 0 {
		t.Fatal("constraint inspection modified draft, snapshot, plan or server")
	}
	target := chooseDiagnostic(h, "/spec/count")
	if !target.position.Exact || !strings.HasPrefix(string([]rune(before)[target.position.RuneOffset:]), "count:") {
		t.Fatal("constraint diagnostic did not retain the original Unicode source position")
	}
	h.click("Go to location")
	if !h.tt.Focused("Manifest editor") || h.w.editorText() != before || h.w.plan != nil {
		t.Fatal("constraint navigation altered the draft or did not focus its source")
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
		t.Fatal("native correction lost the integer above 2^53")
	}
	fraction, found, err := unstructured.NestedFloat64(obj.Object, "spec", "fraction")
	if err != nil || !found || fraction != 0.1 {
		t.Fatal("test did not exercise the real manifest decimal decoding path")
	}
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if len(h.w.schemaLocations) != 0 || !strings.Contains(h.w.schemaText, "No supported structural or constraint hints found.") || strings.Contains(h.w.schemaText, "incomplete") || h.w.editorText() != corrected || h.w.plan != nil {
		t.Fatalf("equal large integer or 0.1 boundary was rejected: %s", h.w.schemaText)
	}
	// Exercise the next integer as well: both neighbours must remain distinct
	// after ordinary native input and the authoritative manifest decoder.
	above := strings.Replace(corrected, "count: 9007199254740993", "count: 9007199254740994", 1)
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(above)
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/count · maximum") || len(h.w.schemaLocations) != 1 || h.w.editorText() != above || h.w.plan != nil {
		t.Fatalf("exact upper boundary missing: %s", h.w.schemaText)
	}
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != corrected {
		t.Fatal("checking the upper bound broke Undo")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != before {
		t.Fatal("constraint navigation reset the preceding correction")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != schemaDraft || original.String() != schemaDraft || draft.String() != before || h.w.editor != buffer || h.w.plan != nil || writes.Load() != 0 {
		t.Fatal("constraint inspection reset native history or mutated retained state")
	}
}

func TestNativeSchemaConstraintsMinimumWindowAndNoValueDisclosure(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		codes        []string
	}{
		{"lower", "  title: 🚢\n  items: []\n  attributes: {}\n", []string{"minLength", "minItems", "minProperties"}},
		{"upper", "  title: 中文🚢ab\n  items: [a, b, c]\n  attributes: {a: x, b: x, c: x}\n", []string{"maxLength", "maxItems", "maxProperties"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, writes := nativeConstraintsHarness(t)
			h.tt.SetSize(1100, 700)
			draft := "# 中文 🚢 — local values stay in the editor\n" + schemaDraft + "  count: 9007199254740993\n  fraction: 0.1\n  mode: PRIVATE_DRAFT_CHOICE\n  uninspected: PRIVATE_UNCHECKED_VALUE\n" + tc.fields
			h.click("Manifest editor")
			h.tt.Key(ui.Cmd, ui.KeyA)
			h.tt.Type(draft)
			h.click("Check draft")
			h.pump(func() bool { return !h.w.schemaActive })
			for i, path := range []string{"/spec/title", "/spec/items", "/spec/attributes"} {
				if !strings.Contains(h.w.schemaText, path+" · "+tc.codes[i]) {
					t.Errorf("missing size constraint %s at %s", tc.codes[i], path)
				}
			}
			if !strings.Contains(h.w.schemaText, "/spec/mode · enum") || !strings.Contains(h.w.schemaText, "Inspection is incomplete") || len(h.w.schemaLocations) != 4 {
				t.Fatal("scalar choices or unsupported-constraint disclosure missing")
			}
			for _, private := range []string{"PRIVATE_DRAFT_CHOICE", "PRIVATE_ENUM_CHOICE", "PRIVATE_UNCHECKED_VALUE", "PRIVATE_SCHEMA_PATTERN"} {
				if strings.Contains(h.w.schemaText+h.w.schemaStatus, private) {
					t.Fatal("schema output echoed a draft value or advertised constraint value")
				}
				for _, location := range h.w.schemaLocations {
					if strings.Contains(location.label, private) {
						t.Fatal("source navigation label echoed a value")
					}
				}
			}
			for _, label := range []string{"Manifest editor", "Check draft", "Clear schema", "Schema location", "Go to location", "Schema assistance", "Preview edit", "Apply", "Close detail"} {
				r, ok := h.tt.Find(label)
				if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 1088 || r.Y+r.H > 700 {
					t.Errorf("%s outside minimum window: %+v found=%v", label, r, ok)
				}
			}
			chooseDiagnostic(h, "/spec/mode")
			h.click("Go to location")
			caret, ok := h.tt.TextCaret()
			editor, _ := h.tt.Find("Manifest editor")
			if !ok || caret.Y < editor.Y || caret.Y > editor.Y+editor.H || !h.tt.Focused("Manifest editor") || h.w.editorText() != draft || h.w.plan != nil || writes.Load() != 0 {
				t.Fatal("minimum-window navigation hid its target or changed the draft/server")
			}
			saveNativeScreenshot(t, h.tt)
		})
	}
}
