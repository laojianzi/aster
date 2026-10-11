package uiworkbench

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNativeMyGo045PhysicalShortcutsPreserveScopedHistory(t *testing.T) {
	h := schemaHarness(t, "https://example.invalid")
	h.click("Manifest editor")
	buffer := h.w.editor
	const draft = "# Кириллица 日本語 🚢\n" + schemaDraft
	// KeyAt sends KeyUnknown with an ANSI physical position, as on a
	// non-Latin layout. These are the actual Aster controls and histories.
	h.tt.KeyAt(ui.Cmd, ui.KeyA)
	h.tt.Type(draft)
	if h.w.editorText() != draft {
		t.Fatal("physical Select All did not replace the manifest")
	}
	h.tt.KeyAt(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != schemaDraft {
		t.Fatal("physical Undo did not restore the manifest")
	}
	h.tt.KeyAt(ui.Cmd|ui.Shift, ui.KeyZ)
	if h.w.editorText() != draft {
		t.Fatal("physical Redo did not restore the edit")
	}

	h.click("Find")
	h.click("Find text")
	const query, replacement = "сервис 日本語 🚢", "替换 café"
	h.tt.Type(query)
	h.tt.KeyAt(ui.Cmd, ui.KeyA)
	h.tt.KeyAt(ui.Cmd, ui.KeyC)
	if h.tt.Clipboard() != query {
		t.Fatal("physical Copy did not use the focused query")
	}
	h.tt.SetClipboard(replacement)
	h.tt.KeyAt(ui.Cmd, ui.KeyV)
	if h.w.find.query != replacement || h.w.editorText() != draft {
		t.Fatal("physical Paste changed the wrong field")
	}
	h.tt.KeyAt(ui.Cmd, ui.KeyZ)
	if h.w.find.query != query || h.w.editorText() != draft {
		t.Fatal("query Undo crossed into manifest history")
	}
	h.tt.KeyAt(ui.Cmd|ui.Shift, ui.KeyZ)
	if h.w.find.query != replacement || h.w.editorText() != draft {
		t.Fatal("query Redo crossed into manifest history")
	}
	// A physical position alone must neither type a US letter nor undo.
	h.tt.KeyAt(0, ui.KeyZ)
	h.tt.KeyAt(ui.Shift, ui.KeyA)
	h.w.detailMessage = "Unrelated status"
	h.tt.Frame()
	if !h.tt.Focused("Find text") || h.w.find.query != replacement || h.w.editorText() != draft {
		t.Fatal("unmodified physical keys or a redraw changed the focused field")
	}

	h.click("Manifest editor")
	h.tt.KeyAt(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != schemaDraft || h.w.find.query != replacement {
		t.Fatal("manifest Undo crossed into query history")
	}
	h.tt.KeyAt(ui.Cmd|ui.Shift, ui.KeyZ)
	if h.w.editorText() != draft || h.w.editor != buffer || h.w.find.query != replacement {
		t.Fatal("manifest Redo replaced buffer identity or query history")
	}
	if h.w.find.active || h.w.find.busy || h.w.find.result != nil || h.w.plan != nil {
		t.Fatal("editing shortcuts implicitly searched or prepared an operation")
	}
}

func TestNativeMyGo045UnicodeCrossLineSelectionPreservesUndo(t *testing.T) {
	h := schemaHarness(t, "https://example.invalid")
	h.click("Manifest editor")
	const prefix, selected, suffix = "# ", "中文 🚢\n# café\n", "# keep\n" + schemaDraft
	const draft = prefix + selected + suffix
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(draft)
	buffer, snapshot, identity := h.w.editor, h.w.editor.Snapshot(), h.w.draftIdentity()

	// Select from inside the first line through the second newline. The ship
	// occupies multiple UTF-8 bytes and UTF-16 units, but one caret position.
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Key(0, ui.KeyLeft)
	for range 2 {
		h.tt.Key(0, ui.KeyRight)
	}
	for range 12 {
		h.tt.Key(ui.Shift, ui.KeyRight)
	}
	h.tt.Key(ui.Cmd, ui.KeyC)
	if h.tt.Clipboard() != selected {
		t.Fatalf("cross-line selection copied %q, want %q", h.tt.Clipboard(), selected)
	}
	h.w.detailMessage = "Selection must survive an unrelated row and resize"
	h.tt.SetSize(1100, 700)
	h.tt.Frame()
	h.tt.Key(ui.Cmd, ui.KeyC)
	if h.tt.Clipboard() != selected || h.w.editorText() != draft || h.w.draftIdentity() != identity {
		t.Fatal("redraw changed selection or committed draft identity")
	}
	saveNativeScreenshot(t, h.tt)

	const replacement = "替换 🚀\n"
	h.tt.Type(replacement)
	if h.w.editorText() != prefix+replacement+suffix || h.w.editor != buffer || snapshot.String() != draft {
		t.Fatal("replacement changed the wrong byte range, buffer identity, or old snapshot")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != draft {
		t.Fatal("Undo did not restore the exact Unicode document")
	}
	h.tt.Key(ui.Cmd, ui.KeyC)
	if h.tt.Clipboard() != selected {
		t.Fatal("Undo did not restore the cross-line selection")
	}
	h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if h.w.editorText() != prefix+replacement+suffix {
		t.Fatal("Redo did not reapply the selected replacement")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != schemaDraft || h.w.editor != buffer {
		t.Fatal("selection or redraw reset the preceding native edit history")
	}
}
