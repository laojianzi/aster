package uiworkbench

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// MyGo 0.4.8 shares the editor's rope with TextAreaBuffer. Exercise the actual
// mounted manifest across its 4 KiB chunks without adopting ui/editor, which
// has a different loaded-newline contract. Input remains comfortably bounded.
func TestNativeMyGo048ChunkedBufferPreservesMixedBytesAndHistory(t *testing.T) {
	h := schemaHarness(t, "https://example.invalid")
	const tail = "# café 船🚢Z"
	original := strings.Repeat("# ascii\n", 511) + "# mixed\r\n" + tail
	h.w.setEditor(original)
	h.tt.Frame()
	h.click("Manifest editor")
	buffer, snapshot := h.w.editor, h.w.editor.Snapshot()
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Key(0, ui.KeyRight)
	h.tt.Key(ui.Shift, ui.KeyLeft)
	h.tt.Key(ui.Shift, ui.KeyLeft)
	h.tt.Key(ui.Cmd, ui.KeyC)
	if h.tt.Clipboard() != "🚢Z" {
		t.Fatal("chunked buffer selection did not use rune coordinates")
	}

	// Losing the window's focus and painting another frame must not commit
	// text, change selection, or create a different document/history.
	identity := h.w.draftIdentity()
	h.tt.SetFocused(false)
	h.tt.Frame()
	h.tt.SetFocused(true)
	h.tt.Frame()
	h.tt.Key(ui.Cmd, ui.KeyC)
	if h.tt.Clipboard() != "🚢Z" || h.w.editorText() != original || h.w.draftIdentity() != identity {
		t.Fatal("focus change altered selection, loaded bytes, or document identity")
	}
	saveNativeScreenshot(t, h.tt)

	const inserted = "𠮷🚀"
	h.tt.Type(inserted)
	changed := strings.TrimSuffix(original, "🚢Z") + inserted
	if h.w.editorText() != changed || snapshot.String() != original || h.w.editor != buffer {
		t.Fatal("native edit changed mixed newlines, snapshot, or buffer identity")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != original {
		t.Fatal("Undo did not restore exact loaded mixed CRLF/LF bytes")
	}
	h.tt.Key(ui.Cmd, ui.KeyC)
	if h.tt.Clipboard() != "🚢Z" {
		t.Fatal("Undo did not restore the original Unicode selection")
	}
	h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if h.w.editorText() != changed {
		t.Fatal("Redo lost the chunked replacement")
	}
	h.tt.Type("!")
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != changed {
		t.Fatal("second edit did not have its own native history entry")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != original || h.w.editor != buffer || snapshot.String() != original {
		t.Fatal("earlier history or immutable snapshot was lost")
	}
	if h.w.plan != nil || h.w.find.active || h.w.find.busy {
		t.Fatal("dependency regression implicitly prepared an operation or search")
	}
}
