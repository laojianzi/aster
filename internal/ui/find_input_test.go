package uiworkbench

import (
	"github.com/egoist/mygo/ui"
	"strings"
	"testing"
)

// A real input event normalizes CRLF before Find starts. Search must use that
// actual document, not the pre-input string; direct core input still preserves
// CRLF positions. This reproduces the three-version E2E fixture failure.
func TestNativeFindTextInputNormalizesCRLFBeforeSearch(t *testing.T) {
	h := schemaHarness(t, "https://example.invalid")
	const image = "registry.k8s.io/e2e-test-images/busybox:1.37.0-2"
	input := "# 中文 🚢 é\r\n" + strings.ReplaceAll(schemaDraft, "\n", "\r\n") + "# " + image + "\r\n"
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(input)
	want := strings.ReplaceAll(input, "\r\n", "\n")
	if h.w.editorText() != want {
		t.Fatal("native input violated the LF normalization contract")
	}
	h.click("Find")
	h.click("Find text")
	h.tt.Type(image)
	findSearch(h)
	match, ok := h.w.find.result.At(0)
	if !ok || h.w.find.result.Len() != 1 || h.w.editorText() != want || !h.tt.Focused("Manifest editor") || h.w.plan != nil {
		t.Fatal("Find changed the normalized draft or failed to select")
	}
	h.tt.Type("local-only")
	if h.w.editorText() != want[:match.ByteStart]+"local-only"+want[match.ByteEnd:] {
		t.Fatal("normalized document selected a different range")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != want {
		t.Fatal("Undo failed to restore the exact normalized baseline")
	}
}
