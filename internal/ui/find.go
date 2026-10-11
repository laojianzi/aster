package uiworkbench

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/manifest"
)

type findBinding struct {
	connection, detail, revision uint64
	session                      string
	draft                        draftIdentity
	query                        [32]byte
}
type findJump struct {
	epoch    uint64
	index    int
	deferred bool
	wrapped  bool
}
type manifestFind struct {
	open, active, busy bool
	epoch, inputEpoch  uint64
	query, status      string
	cancel             context.CancelFunc
	binding            findBinding
	result             *manifest.FindResult
	selected           int
	jump               *findJump
}
type findInputKey struct{ Epoch uint64 }

func (w *Workbench) findEligible() bool {
	return w.ctx.Err() == nil && w.backend != nil && w.detail != nil && w.detailMode == "Edit" &&
		w.detailKind.Kind != "Secret" && w.detail.GetKind() != "Secret" && !w.oidcOpen && !w.vaultOpen &&
		w.operationContext().Err() == nil
}
func (w *Workbench) findBinding() (findBinding, bool) {
	w.syncEditor()
	if !w.findEligible() || w.editor.ByteLen() > manifest.MaxBytes || len(w.find.query) > manifest.MaxFindQueryBytes {
		return findBinding{}, false
	}
	return findBinding{w.contextEpoch, w.detailEpoch, w.draftRevision, w.sessionID, w.draftIdentity(), sha256.Sum256([]byte(w.find.query))}, true
}
func (w *Workbench) findFresh() bool {
	binding, ok := w.findBinding()
	return ok && binding == w.find.binding
}

// Clear results without granting another worker admission before the old worker
// has joined. Input history is reset only when the Find panel is closed.
func (w *Workbench) clearFind(closePanel bool) {
	w.find.epoch++
	if w.find.cancel != nil {
		w.find.cancel()
		w.find.cancel = nil
	}
	w.find.active = false
	w.find.result, w.find.jump, w.find.status, w.find.selected = nil, nil, "", -1
	if closePanel {
		w.find.open, w.find.query = false, ""
		w.find.inputEpoch++
	}
}
func (w *Workbench) syncFind() {
	if !w.find.open {
		return
	}
	if !w.findEligible() {
		w.clearFind(true)
		return
	}
	if (w.find.active || w.find.result != nil || w.find.jump != nil) && !w.findFresh() {
		w.clearFind(false)
	}
}
func (w *Workbench) closeFind(epoch uint64) {
	if epoch == w.find.epoch {
		w.clearFind(true)
	}
}
func (w *Workbench) findButton(c *ui.Context) {
	connection, detail := w.contextEpoch, w.detailEpoch
	ui.Button(c, "Find").OnClick(func() {
		if connection == w.contextEpoch && detail == w.detailEpoch {
			w.openFind()
		}
	})
}
func (w *Workbench) openFind() {
	if !w.findEligible() {
		return
	}
	w.clearFind(true)
	w.find.open = true
	w.find.status = "Literal · case-sensitive · local draft only"
}
func (w *Workbench) startFind() {
	if !w.find.open || w.find.busy || !w.findEligible() {
		return
	}
	w.clearFind(false)
	binding, ok := w.findBinding()
	if !ok {
		w.find.status = "Find exceeds the document or query budget."
		return
	}
	w.find.binding = binding
	parent := w.operationContext()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	w.find.cancel, w.find.active, w.find.busy = cancel, true, true
	w.find.status = "Searching local draft…"
	epoch, snapshot, query := w.find.epoch, w.editor.Snapshot(), w.find.query
	w.run(func(context.Context) {
		source, err := manifestSnapshotText(ctx, snapshot)
		var result *manifest.FindResult
		if err == nil {
			result, err = manifest.FindLiteral(ctx, source, query)
		}
		requestErr := ctx.Err()
		cancel()
		w.emit(func() {
			// Single admitted worker: even a canceled/hidden request must release
			// admission, but it cannot clear or publish another request's state.
			w.find.busy = false
			if epoch != w.find.epoch {
				return
			}
			if !w.find.open || !w.findFresh() || parent.Err() != nil {
				w.clearFind(false)
				return
			}
			w.find.active, w.find.cancel = false, nil
			if err != nil || requestErr != nil {
				w.find.status = "Find unavailable: invalid text, budget, cancellation or timeout. Draft unchanged."
				return
			}
			w.find.result = result
			if result.Len() == 0 {
				w.find.status = "No literal matches · draft unchanged"
				return
			}
			w.queueFindJump(epoch, 0, false)
		})
	})
}
func (w *Workbench) findView(c *ui.Context) {
	if !w.find.open {
		return
	}
	ui.Row(c.Key("find.controls")).Gap(5).Children(func() {
		if ui.TextInput(c.Key(findInputKey{w.find.inputEpoch}), &w.find.query).Label("Find text").Placeholder("Literal text").Grow(1).Changed() {
			w.clearFind(false)
		}
		epoch := w.find.epoch
		ui.Button(c, "Search").Disabled(w.find.busy).OnClick(func() {
			if epoch == w.find.epoch {
				w.startFind()
			}
		})
		ui.Button(c, "Previous").Label("Previous match").Disabled(w.find.active || w.find.result.Len() == 0).OnClick(func() { w.stepFind(epoch, -1) })
		ui.Button(c, "Next").Label("Next match").Disabled(w.find.active || w.find.result.Len() == 0).OnClick(func() { w.stepFind(epoch, 1) })
		ui.Button(c, "Close").Label("Close find").OnClick(func() { w.closeFind(epoch) })
	})
	status := w.find.status
	if w.find.busy && !w.find.active {
		status = "Finishing previous search…"
	}
	if status == "" {
		status = "Literal · case-sensitive · local draft only"
	}
	ui.Text(c, status).Label("Find status").FontSize(11).SingleLine()
}
func (w *Workbench) stepFind(epoch uint64, delta int) {
	if epoch != w.find.epoch || !w.findFresh() || w.find.result.Len() == 0 {
		return
	}
	i, n := w.find.selected+delta, w.find.result.Len()
	wrap := i < 0 || i >= n
	if i < 0 {
		i = n - 1
	}
	if i >= n {
		i = 0
	}
	w.queueFindJump(epoch, i, wrap)
}
func (w *Workbench) queueFindJump(epoch uint64, index int, wrap bool) {
	if epoch != w.find.epoch || !w.find.open || w.find.active || !w.findFresh() {
		return
	}
	_, ok := w.find.result.At(index)
	if !ok {
		return
	}
	w.schemaJump = nil
	w.find.selected = index
	w.find.status = w.findMatchStatus(index, wrap)
	w.find.jump = &findJump{epoch: epoch, index: index, wrapped: wrap}
}

func (w *Workbench) findMatchStatus(index int, wrap bool) string {
	match, ok := w.find.result.At(index)
	if !ok {
		return "No current match"
	}
	status := fmt.Sprintf("%d / %d · L%d:C%d", index+1, w.find.result.Len(), match.Line, match.Column)
	if w.find.result.Truncated() {
		status += " · truncated to first 512"
	}
	if wrap {
		status += " · wrapped"
	}
	return status
}

// Use the build-scoped element synchronously; only immutable offsets/epochs are
// kept across frames. Composition defers the request while this editor retains
// focus; any commit or changed document invalidates it before applying it.
func (w *Workbench) applyFindJump(editor ui.Element) {
	request := w.find.jump
	if request == nil {
		return
	}
	if request.epoch != w.find.epoch || !w.findFresh() {
		w.find.jump = nil
		return
	}
	if editor.Composing() {
		request.deferred = true
		w.find.status = w.findMatchStatus(request.index, request.wrapped) + " · finish composition to navigate"
		return
	}
	w.find.jump = nil
	if request.deferred && !editor.Focused() {
		w.find.status = w.findMatchStatus(request.index, request.wrapped) + " · navigation canceled after focus change"
		return
	}
	match, ok := w.find.result.At(request.index)
	if !ok {
		return
	}
	editor.SetTextSelection(match.RuneStart, match.RuneEnd).Focus()
	if request.deferred {
		w.find.status = w.findMatchStatus(request.index, request.wrapped)
	}
}
