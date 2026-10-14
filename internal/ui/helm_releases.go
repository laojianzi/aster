package uiworkbench

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
)

type helmReleaseState struct {
	open, busy                 bool
	epoch, contextEpoch        uint64
	cancel                     context.CancelFunc
	backend                    *kube.Backend
	namespace, session, status string
	snapshot                   helmrelease.Snapshot
	list                       ui.ListState
}

func (w *Workbench) clearHelmReleases() {
	r := &w.helmReleases
	if r.cancel != nil {
		r.cancel()
	}
	w.helmReleases = helmReleaseState{epoch: r.epoch + 1}
}

func (w *Workbench) syncHelmReleases() {
	r := &w.helmReleases
	if r.open && (r.backend != w.backend || r.contextEpoch != w.contextEpoch || r.session != w.sessionID || r.namespace != strings.TrimSpace(w.namespace)) {
		w.clearHelmReleases()
	}
}

func (w *Workbench) openHelmReleases() {
	// Do not unmount a live detail editor or interrupt an in-flight mutation.
	if w.backend == nil || w.detail != nil || w.delivery.open || w.pendingWrites != 0 {
		return
	}
	w.clearHelmReleases()
	r := &w.helmReleases
	r.open, r.backend, r.contextEpoch = true, w.backend, w.contextEpoch
	r.namespace, r.session = strings.TrimSpace(w.namespace), w.sessionID
	r.status = "No metadata requested. Choose Refresh Helm metadata to read this namespace."
	if !helmrelease.ValidNamespace(r.namespace) {
		r.status = helmrelease.ErrNamespace.Error()
	}
}

func (w *Workbench) stopHelmReleases() {
	r := &w.helmReleases
	r.epoch++
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.busy = false
	r.snapshot = helmrelease.Snapshot{}
	r.status = "Metadata read stopped; no cluster mutation was performed."
}

func (w *Workbench) refreshHelmReleases() {
	w.syncHelmReleases()
	r := &w.helmReleases
	if !r.open || r.busy || w.backend == nil {
		return
	}
	w.stopHelmReleases()
	if !helmrelease.ValidNamespace(r.namespace) {
		r.status = helmrelease.ErrNamespace.Error()
		return
	}
	epoch, contextEpoch, session, ns, backend := r.epoch, r.contextEpoch, r.session, r.namespace, r.backend
	ctx, cancel := context.WithCancel(w.operationContext())
	r.cancel, r.busy, r.status = cancel, true, "Reading bounded Helm revision metadata…"
	w.run(func(context.Context) {
		defer cancel()
		snapshot, err := backend.HelmReleaseMetadata(ctx, ns)
		w.emit(func() {
			current := &w.helmReleases
			if !current.open || current.epoch != epoch || w.contextEpoch != contextEpoch || w.sessionID != session || w.backend != backend || strings.TrimSpace(w.namespace) != ns {
				return
			}
			current.busy, current.cancel = false, nil
			if err != nil {
				current.snapshot = helmrelease.Snapshot{}
				switch {
				case errors.Is(err, context.Canceled):
					current.status = "Helm metadata read cancelled."
				case errors.Is(err, context.DeadlineExceeded):
					current.status = "Helm metadata read timed out; explicitly refresh to retry."
				default:
					current.status = err.Error() // Backend returns only fixed private errors.
				}
				return
			}
			current.snapshot = snapshot
			current.list = ui.ListState{}
			current.status = fmt.Sprintf("%d observed revision(s) · snapshot %s UTC", len(snapshot.Revisions), snapshot.ObservedAt.Format("15:04:05"))
		})
	})
}

func (w *Workbench) helmReleasesView(c *ui.Context) {
	r := &w.helmReleases
	ui.Column(c).Grow(1).Padding(12).Gap(8).Children(func() {
		ui.Text(c, "Helm revision metadata").Bold().FontSize(20)
		ui.Text(c, "Namespace: "+r.namespace).Label("Helm metadata namespace").SingleLine()
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, "Refresh Helm metadata").Disabled(r.busy || !helmrelease.ValidNamespace(r.namespace)).OnClick(w.refreshHelmReleases)
			ui.Button(c, "Stop Helm metadata").Disabled(!r.busy).OnClick(w.stopHelmReleases)
			ui.Button(c, "Close Helm metadata").OnClick(w.clearHelmReleases)
		})
		ui.Text(c, r.status).Label("Helm metadata status").FontSize(12)
		ui.Text(c, "Read-only Secret-driver labels · not decoded release state or workload health").FontSize(11)
		ui.Text(c, "No values/manifests, install, upgrade, rollback, uninstall or write approval.").FontSize(11)
		ui.Text(c, "Requires list secrets permission. ConfigMap/SQL drivers are not queried.").FontSize(11)
		rows := r.snapshot.Revisions
		ui.List(c.Key("helm-release-revisions"), &r.list, len(rows), func(i int) {
			row := rows[i]
			ui.Column(c).Padding(6).Gap(3).Children(func() {
				ui.Text(c, fmt.Sprintf("%s · revision %d · label: %s", row.Release, row.Number, row.Status)).Label("Helm revision " + row.StorageName).SingleLine()
				ui.Text(c, row.StorageName).FontSize(10).SingleLine()
			})
		}).Grow(1)
		if !r.busy && r.snapshot.ResourceVersion != "" && len(rows) == 0 {
			ui.Text(c, "No matching Secret-driver labels in this namespace snapshot. Other drivers were not checked.").FontSize(11)
		}
	})
}
