package uiworkbench

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/helmrelease"
)

func (w *Workbench) clearHelmInspection() {
	w.clearHelmUninstall()
	w.clearHelmInstall()
	r := &w.helmReleases
	r.contentEpoch++
	if r.contentCancel != nil {
		r.contentCancel()
		r.contentCancel = nil
	}
	r.selected = helmrelease.Revision{}
	r.acknowledged, r.inspecting = false, false
	r.inspection = nil
	r.preview = nil
	r.live = nil
	r.liveAcknowledged = false
	r.liveStatus = ""
	r.contentStatus = ""
}
func (w *Workbench) selectHelmRevision(row helmrelease.Revision) {
	w.syncHelmReleases()
	r := &w.helmReleases
	if !r.open || r.busy || r.snapshot.ResourceVersion == "" {
		return
	}
	found := false
	for _, candidate := range r.snapshot.Revisions {
		if candidate == row {
			found = true
			break
		}
	}
	if !found {
		return
	}
	w.clearHelmInspection()
	r.selected = row
	r.contentStatus = "Selected only. No Secret content has been requested."
}
func (w *Workbench) inspectHelmRevision()  { w.readHelmContent(false) }
func (w *Workbench) previewHelmUninstall() { w.readHelmContent(true) }

func (w *Workbench) readHelmContent(preview bool) {
	w.syncHelmReleases()
	r := &w.helmReleases
	if !r.open || r.busy || r.inspecting || !r.acknowledged || !helmrelease.ValidSelection(r.namespace, r.selected) {
		return
	}
	row, epoch, contextEpoch, session, ns, backend := r.selected, r.epoch, r.contextEpoch, r.session, r.namespace, r.backend
	r.contentEpoch++
	contentEpoch := r.contentEpoch
	r.acknowledged = false // Single explicit attempt; failures never retain consent.
	r.inspection = nil
	r.preview = nil
	r.live = nil
	r.liveAcknowledged = false
	r.liveStatus = ""
	r.inspecting = true
	r.contentStatus = "Inspecting the selected Secret content; raw content is withheld…"
	ctx, cancel := context.WithCancel(w.operationContext())
	r.contentCancel = cancel
	w.run(func(context.Context) {
		defer cancel()
		var result helmrelease.Inspection
		var scope *helmrelease.UninstallPreview
		var err error
		if preview {
			scope, err = backend.PreviewHelmUninstall(ctx, ns, row, session, true)
		} else {
			result, err = backend.InspectHelmRelease(ctx, ns, row, true)
		}
		w.emit(func() {
			current := &w.helmReleases
			if !current.open || current.epoch != epoch || current.contentEpoch != contentEpoch || current.selected != row ||
				w.contextEpoch != contextEpoch || w.sessionID != session || w.backend != backend || strings.TrimSpace(w.namespace) != ns {
				return
			}
			current.inspecting, current.contentCancel = false, nil
			if err != nil {
				current.inspection = nil
				switch {
				case errors.Is(err, context.Canceled):
					current.contentStatus = "Helm content inspection cancelled."
				case errors.Is(err, context.DeadlineExceeded):
					current.contentStatus = "Helm content inspection timed out; no content is shown."
				default:
					current.contentStatus = err.Error() // Fixed backend/decoder errors only.
				}
				if errors.Is(err, helmrelease.ErrInspectionStale) {
					current.selected = helmrelease.Revision{}
					current.snapshot = helmrelease.Snapshot{}
					current.status = helmrelease.ErrInspectionStale.Error()
				}
				return
			}
			if preview {
				if !scope.Matches(session, ns, row, time.Now()) {
					current.contentStatus = "Helm scope preview expired; explicitly read again."
					return
				}
				current.preview = scope
				current.contentStatus = "Stored scope only. No live ownership check, dry-run, or deletion approval."
				return
			}
			current.inspection = &result
			current.contentStatus = "Selected object and payload identities agree. Consistency is NOT authenticity or write approval."
		})
	})
}
func (w *Workbench) helmInspectionView(c *ui.Context) {
	r := &w.helmReleases
	if r.selected.StorageName == "" {
		return
	}
	ui.Column(c).Gap(5).Children(func() {
		ui.Text(c, fmt.Sprintf("Selected: %s · revision %d", r.selected.Release, r.selected.Number)).Label("Helm inspection selection").SingleLine()
		ui.Text(c, "Content may contain secrets. Requires get secrets; raw values/manifests/notes stay hidden.").FontSize(11)
		guardedCheckbox(c, fmt.Sprintf("helm.inspection.consent.%d", r.contentEpoch), &r.acknowledged, "Allow reading this revision's sensitive Secret content", !r.busy && !r.inspecting)
		ui.Row(c).Gap(8).Wrap().Children(func() {
			ui.Button(c, "Inspect selected revision").Disabled(!r.acknowledged || r.busy || r.inspecting).OnClick(w.inspectHelmRevision)
			ui.Button(c, "Preview uninstall scope").Disabled(!r.acknowledged || r.busy || r.inspecting).OnClick(w.previewHelmUninstall)
			ui.Button(c, "Stop content inspection").Disabled(!r.inspecting).OnClick(w.clearHelmInspection)
			ui.Button(c, "Discard inspection").OnClick(w.clearHelmInspection)
		})
		ui.Text(c, r.contentStatus).Label("Helm inspection status").FontSize(11)
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, "Review restricted uninstall").Disabled(r.busy || r.inspecting || w.pendingWrites != 0 || r.selected.Status != "deployed").OnClick(w.openHelmUninstall)
			ui.Button(c, "Review recovery history").Disabled(r.busy || r.inspecting || w.pendingWrites != 0).OnClick(w.openHelmRecovery)
		})
		if s := r.inspection; s != nil {
			ui.Text(c, "Chart: "+s.ChartName+" · "+s.ChartVersion).Label("Helm inspection chart").SingleLine().FontSize(12)
			ui.Text(c, fmt.Sprintf("Withheld: %d manifest bytes · %d hooks · %d supplied values", s.ManifestBytes, s.HookCount, s.SuppliedValues)).Label("Helm inspection summary").FontSize(11)
		}
	})
}
