package uiworkbench

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/manifest"
)

const maxManifestClipboardLine = 8192
const manifestPastePendingMessage = "Clipboard paste retained separately; it was not inserted. Discard the retained paste before a new resource preview."

// Capture only document identity and a public buffer, never a build-scoped
// Element/Context. A same-document unrendered edit is allowed: the callback
// observes its current snapshot rather than trusting a stale revision/budget.
func (w *Workbench) manifestPasteHandler() func(string) bool {
	binding, editor := w.draftIdentity(), w.editor
	return func(text string) bool {
		current := w.draftIdentity()
		if editor == nil || editor != w.editor || binding.connection != current.connection || binding.detail != current.detail || binding.document != current.document || w.detail == nil || w.detailMode != "Edit" || w.detail.GetKind() == "Secret" || w.operationContext().Err() != nil {
			return true
		}
		w.syncEditor()
		if w.editorRetainedPaste != "" {
			w.detailMessage = manifestPastePendingMessage
			return true // Never replace the first retained input with another paste.
		}
		if !clipboardPasteExceedsBudget(editor.Snapshot(), text, manifest.MaxBytes, 5000, maxManifestClipboardLine) {
			return false // Native insertion owns normalization, selection and Undo.
		}
		w.editorRetainedPaste = text
		w.editorPasteEpoch++
		w.blockRetainedManifestPaste()
		return true
	}
}

// Revoke unsent work without changing the draft's semantic revision, immutable
// snapshots, read-only assistance or native input state. This cannot cancel or
// roll back an already submitted operation.
func (w *Workbench) blockRetainedManifestPaste() bool {
	if w.editorRetainedPaste == "" {
		return false
	}
	w.stopPreview()
	w.plan, w.diff, w.confirmation = nil, "", ""
	w.detailMessage = manifestPastePendingMessage
	return true
}

func (w *Workbench) discardManifestPaste(epoch uint64) {
	if epoch != w.editorPasteEpoch || w.editorRetainedPaste == "" {
		return
	}
	w.blockRetainedManifestPaste()
	w.editorRetainedPaste = ""
	w.editorPasteEpoch++
	w.detailMessage = "Retained clipboard paste discarded; the editor was not replaced. Prepare a fresh preview before submission."
}

func (w *Workbench) manifestPasteView(c *ui.Context) {
	if w.editorRetainedPaste == "" {
		return
	}
	epoch := w.editorPasteEpoch
	ui.Column(c.Key("manifest.retained-paste")).Gap(4).Children(func() {
		ui.Text(c, fmt.Sprintf("Paste retained separately (%d bytes); not inserted. Resource previews paused.", len(w.editorRetainedPaste))).Label("Manifest retained paste").FontSize(11)
		ui.Button(c, "Discard retained editor paste").OnClick(func() { w.discardManifestPaste(epoch) })
	})
}
