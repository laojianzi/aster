package uiworkbench

import (
	"context"
	"errors"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/manifest"
)

// draftIdentity is an opaque UI-owned semantic revision, not a hash of all
// source bytes. It is cheap to compare; no worker dereferences live UI state.
type draftIdentity struct {
	connection, detail, document, revision uint64
}

// setEditor deliberately replaces a document, including its Undo/IME identity.
// Ordinary input must edit the stable TextBuffer, never call this setter.
func (w *Workbench) setEditor(text string) {
	w.editorRetainedPaste = ""
	w.editorPasteEpoch++
	w.editor = ui.NewTextBuffer(text)
	w.editorObserved = w.editor.Snapshot()
	w.editorIdentity++
	w.editorLastSafe, w.editorHasSafe = ui.TextSnapshot{}, false
	if manifestSnapshotWithinBudget(w.editorObserved) {
		w.editorLastSafe, w.editorHasSafe = w.editorObserved, true
	}
	w.invalidateDraft()
}
func (w *Workbench) invalidateDraft() {
	w.draftRevision++
	w.clearFind(false)
	w.clearSchema()
	w.stopPreview()
	w.plan = nil
	w.diff, w.confirmation = "", ""
}
func (w *Workbench) draftIdentity() draftIdentity {
	return draftIdentity{w.contextEpoch, w.detailEpoch, w.editorIdentity, w.draftRevision}
}

// syncEditor observes native or programmatic changes even before a render. MyGo
// can publish a different tree root for a preedit/no-op without changing text.
// Preserve that composition and pending navigation, but never restore the old
// snapshot into the widget (which would erase its native Undo/composition).
func (w *Workbench) syncEditor() {
	if w.editor == nil {
		w.editor = ui.NewTextBuffer("")
		w.editorObserved = w.editor.Snapshot()
		w.editorIdentity++
		return
	}
	current := w.editor.Snapshot()
	if current == w.editorObserved {
		return
	}
	equal := sameManifestText(w.editorObserved, current)
	w.editorObserved = current
	if manifestSnapshotWithinBudget(current) {
		w.editorLastSafe, w.editorHasSafe = current, true
	}
	if !equal {
		w.invalidateDraft()
	}
}

// Idle frames use the O(1) snapshot comparison above. Only changed roots with
// identical summaries need this bounded, chunked comparison. Oversized changes
// always invalidate; they are never flattened or mistaken for empty input.
func sameManifestText(a, b ui.TextSnapshot) bool {
	if a == b {
		return true
	}
	if a.ByteLen() > manifest.MaxBytes || a.ByteLen() != b.ByteLen() || a.Len() != b.Len() || a.LineCount() != b.LineCount() {
		return false
	}
	const chunkRunes = 1024
	for start := 0; start < a.Len(); start += chunkRunes {
		end := min(start+chunkRunes, a.Len())
		if a.Slice(start, end) != b.Slice(start, end) {
			return false
		}
	}
	return true
}

// Explicit consumers receive a captured immutable snapshot. Rendering,
// freshness checks and ordinary input do not call String or hash the source.
// Errors retain the actual draft; no caller may interpret a failed export as
// an empty, valid manifest or silently truncate it.
func manifestSnapshotText(ctx context.Context, snapshot ui.TextSnapshot) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !manifestSnapshotWithinBudget(snapshot) {
		return "", errors.New("manifest exceeds 256 KiB / 5000 newline budget; draft retained")
	}
	text := snapshot.String()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return text, nil
}
func (w *Workbench) schemaFresh() bool {
	w.syncEditor()
	return w.schemaSource != nil && w.schemaBinding == w.draftIdentity()
}

func manifestSnapshotWithinBudget(snapshot ui.TextSnapshot) bool {
	return snapshot.ByteLen() <= manifest.MaxBytes && snapshot.LineCount() <= 5001
}

// A native paste may briefly exceed the supported editing budget. Keep the
// entire draft without scheduling unbounded layout. Only an explicit action
// may restore the last supported version; never silently truncate/discard.
func (w *Workbench) manifestBudgetView(c *ui.Context) bool {
	if manifestSnapshotWithinBudget(w.editor.Snapshot()) {
		return false
	}
	ui.Column(c.Key("editor.over-budget")).Grow(1).Gap(8).Children(func() {
		ui.Text(c, "Draft exceeds the editing budget. Full text is retained; layout, Find and preview are suspended.").Label("Manifest budget exceeded")
		id := w.draftIdentity()
		ui.Button(c, "Restore prior draft").Disabled(!w.editorHasSafe).OnClick(func() {
			w.syncEditor()
			if id != w.draftIdentity() || !w.editorHasSafe {
				return
			}
			buffer := ui.NewTextBuffer("")
			buffer.Restore(w.editorLastSafe)
			w.editorRetainedPaste = ""
			w.editorPasteEpoch++
			w.editor, w.editorObserved = buffer, buffer.Snapshot()
			w.editorIdentity++
			w.invalidateDraft()
		})
		ui.Text(c, "Restore discards the oversized edit and any retained paste. Refresh detail discards all local edits.").FontSize(11)
	})
	return true
}
