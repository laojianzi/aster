package uiworkbench

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/schemaassist"
)

func diagnosticHarness(t *testing.T) *relationshipHarness {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(schemaServer))
	t.Cleanup(server.Close)
	h := schemaHarness(t, server.URL)
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("# 中文 🚢 é\n" + schemaDraft + "    image: 42\n")
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if len(h.w.schemaLocations) == 0 {
		t.Fatalf("missing source locations: %s %s", h.w.schemaStatus, h.w.schemaText)
	}
	return h
}
func chooseDiagnostic(h *relationshipHarness, path string) schemaLocation {
	h.t.Helper()
	for _, target := range h.w.schemaLocations {
		if target.position.Pointer == path {
			h.click("Schema location")
			if err := h.tt.ChooseMenuItem(target.label); err != nil {
				h.t.Fatal(err)
			}
			if h.w.schemaLocationChoice != target.label {
				h.t.Fatal("dropdown did not select diagnostic")
			}
			return target
		}
	}
	h.t.Fatalf("no location for %s", path)
	return schemaLocation{}
}
func TestNativeSchemaDiagnosticNavigationPreservesDraftAndHistory(t *testing.T) {
	h := diagnosticHarness(t)
	before := h.w.editorText()
	target := chooseDiagnostic(h, "/spec/containers/0/image")
	h.click("Go to location")
	if !h.tt.Focused("Manifest editor") || h.w.editorText() != before || h.w.plan != nil || h.w.schemaJump != nil {
		t.Fatal("navigation changed draft/plan or did not focus exactly once")
	}
	if !strings.Contains(h.w.schemaStatus, "L10:C5") {
		t.Fatal(h.w.schemaStatus)
	}
	h.w.detailMessage = "Unrelated status insertion"
	h.tt.Frame()
	if !h.tt.Focused("Manifest editor") {
		t.Fatal("status insertion lost focus")
	}
	h.tt.Type("MARK")
	runes := []rune(before)
	want := string(runes[:target.position.RuneOffset]) + "MARK" + string(runes[target.position.RuneOffset:])
	if h.w.editorText() != want {
		t.Fatal("caret used byte/UTF16 offsets instead of rune offsets")
	}
	if h.w.schemaSource != nil || len(h.w.schemaLocations) != 0 || h.w.schemaJump != nil {
		t.Fatal("typing kept stale source locations")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != before {
		t.Fatal("navigation broke current-document Undo")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != schemaDraft {
		t.Fatalf("navigation reset editor history: %q", h.w.editorText())
	}
}
func TestNativeSchemaMissingFieldNavigatesToLabeledParent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(schemaServer))
	defer server.Close()
	h := schemaHarness(t, server.URL)
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if len(h.w.schemaLocations) != 1 {
		t.Fatal(h.w.schemaText)
	}
	p := h.w.schemaLocations[0]
	if p.position.Exact || p.position.Pointer != "/spec/containers/0" || !strings.Contains(p.label, "enclosing") {
		t.Fatalf("invented missing field location %+v", p)
	}
	h.click("Go to location")
	if !strings.Contains(h.w.schemaStatus, "not the absent field") || h.w.editorText() != schemaDraft {
		t.Fatal("parent location not disclosed")
	}
	h.tt.Type("MARK")
	if !strings.Contains(h.w.editorText(), "- MARKname: app") {
		t.Fatal("missing field did not navigate to explicit parent")
	}
}
func TestNativeSchemaQueuedNavigationCannotCrossDraftIdentityOrTab(t *testing.T) {
	for _, change := range []string{"typing", "programmatic-draft", "detail", "connection", "pointer", "tab", "expiry", "close"} {
		t.Run(change, func(t *testing.T) {
			h := diagnosticHarness(t)
			h.click("Manifest editor")
			h.tt.Key(ui.Cmd, ui.KeyA)
			h.w.queueSchemaJump(h.w.schemaEpoch, h.w.schemaLocationChoice)
			if h.w.schemaJump == nil {
				t.Fatal("navigation was not queued")
			}
			switch change {
			case "typing":
				h.tt.Type("replacement document")
			case "programmatic-draft":
				h.w.setEditor("different draft")
			case "detail":
				h.w.clearDetail()
				h.w.detail = relatedPod()
				h.w.detailKind = catalog()[0]
				h.w.detailMode = "Edit"
				h.w.setEditor("new resource")
			case "connection":
				h.w.contextEpoch++
			case "pointer":
				h.w.schemaPointer = "/metadata"
			case "tab":
				h.w.detailMode = "YAML"
			case "expiry":
				h.w.expireConnection(h.w.contextEpoch)
			case "close":
				h.w.Close()
			}
			before := h.w.editorText()
			h.tt.Frame()
			if h.w.editorText() != before || h.w.schemaJump != nil || strings.Contains(h.w.schemaStatus, "field start") {
				t.Fatal("stale navigation crossed a boundary")
			}
		})
	}
}
func TestNativeSchemaNavigationRespectsCompositionAndIsOneShot(t *testing.T) {
	h := diagnosticHarness(t)
	h.click("Manifest editor")
	h.tt.Compose("未提交", 3)
	before := h.w.editorText()
	h.w.queueSchemaJump(h.w.schemaEpoch, h.w.schemaLocationChoice)
	h.tt.Frame()
	if h.w.editorText() != before || h.w.schemaJump != nil || !strings.Contains(h.w.schemaStatus, "Finish text composition") {
		t.Fatal("navigation altered active IME composition")
	}
	h.tt.Compose("", 0)
	h.click("Go to location")
	h.click("Schema field pointer")
	h.tt.Frame()
	if h.tt.Focused("Manifest editor") {
		t.Fatal("consumed navigation stole focus on another frame")
	}
}
func TestNativeSchemaLocationControlsAtMinimumWindow(t *testing.T) {
	h := diagnosticHarness(t)
	h.tt.SetSize(1100, 700)
	for _, label := range []string{"Manifest editor", "Schema location", "Go to location", "Schema assistance", "Preview edit", "Apply", "Close detail", "Preview delete"} {
		r, ok := h.tt.Find(label)
		if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 1088 || r.Y+r.H > 700 {
			t.Errorf("%s outside minimum window: %+v found=%v", label, r, ok)
		}
	}
	h.click("Go to location")
	caret, ok := h.tt.TextCaret()
	editor, _ := h.tt.Find("Manifest editor")
	if !ok || caret.Y < editor.Y || caret.Y > editor.Y+editor.H {
		t.Errorf("caret not revealed in editor: %+v %+v", caret, editor)
	}
	saveNativeScreenshot(t, h.tt)
}
func TestNativeSchemaLocationsUnavailableDoNotBlockReadOnlyHelp(t *testing.T) {
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes.Add(1)
		}
		schemaServer(w, r)
	}))
	defer server.Close()
	h := schemaHarness(t, server.URL)
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("invalid draft [")
	before := h.w.editorText()
	h.click("Field help")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "Container image reference") || !strings.Contains(h.w.schemaText, "navigation unavailable") || len(h.w.schemaLocations) != 0 || h.w.schemaSource != nil || h.w.editorText() != before || writes.Load() != 0 {
		t.Fatal("unsupported mapping blocked help or guessed a position")
	}
}
func TestNativeSchemaLocationLookupIsEpochBoundAndReadOnly(t *testing.T) {
	h := diagnosticHarness(t)
	epoch, choice := h.w.schemaEpoch, h.w.schemaLocationChoice
	h.w.clearSchema()
	h.w.queueSchemaJump(epoch, choice)
	if h.w.schemaJump != nil {
		t.Fatal("old menu event restored discarded locations")
	}
	x, err := manifest.IndexSource(context.Background(), []byte(schemaDraft))
	if err != nil {
		t.Fatal(err)
	}
	_, ls, _ := schemaLocations(context.Background(), schemaDraft, []schemaassist.Diagnostic{{Pointer: "/spec/containers/0/image", Code: "required"}})
	h.w.setEditor(schemaDraft)
	h.w.setSchemaLocations(x, ls)
	h.w.detail.SetKind("Secret")
	h.w.queueSchemaJump(h.w.schemaEpoch, h.w.schemaLocationChoice)
	if h.w.schemaJump != nil {
		t.Fatal("Secret acquired a navigation request")
	}
}

func TestSchemaLocationDisplayBudgetAndNoValueDisclosure(t *testing.T) {
	text := strings.Repeat("中文🚢", 1<<15)
	got := boundedSchemaOutput(text)
	if len(got) > 64<<10 || !utf8.ValidString(got) || !strings.Contains(got, "Display truncated") {
		t.Fatal("unbounded or invalid source assistance output")
	}
	_, ls, _ := schemaLocations(context.Background(), "field: PRIVATE-DRAFT-VALUE\n", []schemaassist.Diagnostic{{Pointer: "/field", Code: "type"}})
	out := sourceReportText(schemaassist.Report{}, ls)
	if strings.Contains(out, "PRIVATE-DRAFT-VALUE") || len(ls) != 1 {
		t.Fatal("source display copied a value")
	}
}

func TestNativeSchemaSourceCoordinatesAcrossJSONAndCRLF(t *testing.T) {
	texts := []string{
		strings.ReplaceAll("# 中文 🚢\n"+schemaDraft+"    image: 42\n", "\n", "\r\n"),
		`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"related-pod","namespace":"team","labels":{"note":"中文🚢"}},"spec":{"containers":[{"name":"app","image":42}]}}`,
	}
	for i, text := range texts {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(schemaServer))
			defer server.Close()
			h := schemaHarness(t, server.URL)
			// A programmatically loaded document, unlike a paste, retains CRLF.
			h.w.setEditor(text)
			h.tt.Frame()
			h.click("Check draft")
			h.pump(func() bool { return !h.w.schemaActive })
			target := chooseDiagnostic(h, "/spec/containers/0/image")
			h.click("Go to location")
			h.tt.Type("MARK")
			r := []rune(text)
			want := string(r[:target.position.RuneOffset]) + "MARK" + string(r[target.position.RuneOffset:])
			if h.w.editorText() != want {
				t.Fatal("native source units disagree with index")
			}
		})
	}
}

func TestNativeSchemaLocationMenuCannotApplyStaleSelection(t *testing.T) {
	h := diagnosticHarness(t)
	choice := h.w.schemaLocationChoice
	h.click("Schema location")
	h.w.clearSchema()
	if err := h.tt.ChooseMenuItem(choice); err != nil {
		t.Fatal(err)
	}
	if h.w.schemaLocationChoice != "" || h.w.schemaJump != nil {
		t.Fatal("stale platform menu restored locations")
	}
}
