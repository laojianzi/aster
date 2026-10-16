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

const helmLiveConsentLabel = "Allow metadata reads for this preview's live objects"

func (w *Workbench) observeHelmPreview() {
	w.syncHelmReleases()
	w.expireHelmUninstall(time.Now())
	r := &w.helmReleases
	p := r.preview
	if !r.open || r.busy || r.inspecting || !r.liveAcknowledged || p == nil || !p.Matches(w.sessionID, r.namespace, r.selected, time.Now()) {
		return
	}
	row, epoch, contextEpoch, session, ns, backend := r.selected, r.epoch, r.contextEpoch, r.session, r.namespace, r.backend
	r.contentEpoch++
	contentEpoch := r.contentEpoch
	r.liveAcknowledged, r.acknowledged = false, false
	r.live = nil
	r.inspecting = true
	r.liveStatus = "Checking bounded live metadata; no write is possible…"
	ctx, cancel := context.WithCancel(w.operationContext())
	r.contentCancel = cancel
	w.run(func(context.Context) {
		defer cancel()
		result, err := backend.ObserveHelmPreview(ctx, p, session, true)
		w.emit(func() {
			current := &w.helmReleases
			if !current.open || current.epoch != epoch || current.contentEpoch != contentEpoch || current.preview != p || current.selected != row || w.contextEpoch != contextEpoch || w.sessionID != session || w.backend != backend || strings.TrimSpace(w.namespace) != ns {
				return
			}
			current.inspecting, current.contentCancel = false, nil
			if err != nil {
				current.live = nil
				switch {
				case errors.Is(err, context.Canceled):
					current.liveStatus = "Live metadata check cancelled; no result retained."
				case errors.Is(err, context.DeadlineExceeded):
					current.liveStatus = "Live metadata check timed out; no result retained."
				default:
					current.liveStatus = err.Error() // backend only returns fixed errors
				}
				if errors.Is(err, helmrelease.ErrLiveStale) {
					w.clearHelmInspection()
					current.snapshot = helmrelease.Snapshot{}
					current.status = helmrelease.ErrLiveStale.Error()
				}
				return
			}
			if !result.Matches(p, session, ns, row, time.Now()) {
				current.liveStatus = "Live metadata result expired; explicitly check again."
				return
			}
			current.live = result
			current.liveStatus = "Latest stored revision agreed before/after the reads. NOT a lock or deletion approval."
		})
	})
}

func (w *Workbench) helmLiveControlsView(c *ui.Context) {
	r := &w.helmReleases
	ui.Column(c.Key("helm.live.controls")).Gap(4).Shrink(0).Children(func() {
		ui.Text(c, "Live check reads revision labels and object metadata only; foreign namespaces are not read.").FontSize(11)
		guardedCheckbox(c, fmt.Sprintf("helm.live.consent.%d", r.contentEpoch), &r.liveAcknowledged, helmLiveConsentLabel, !r.busy && !r.inspecting).Label("Helm live consent control").MinHeight(28).Shrink(0)
		ui.Button(c, "Check live metadata").Label("Helm live check action").MinHeight(32).Shrink(0).Disabled(!r.liveAcknowledged || r.busy || r.inspecting).OnClick(w.observeHelmPreview)
		if r.liveStatus != "" {
			ui.Text(c, r.liveStatus).Label("Helm live status").FontSize(11)
		}
		if r.live != nil {
			ui.Text(c, "Live sample expires: "+r.live.ExpiresAt().UTC().Format("15:04:05 UTC")).Label("Helm live expiry").FontSize(11)
			ui.Text(c, "Marker match is not authentic ownership. Stored manifests do not bind the originally installed UID. Objects may change immediately.").FontSize(11)
			if d := time.Until(r.live.ExpiresAt()); d > 0 {
				c.After(d)
			}
		}
	})
}

func helmLiveItemView(c *ui.Context, i int, item helmrelease.LiveItem) {
	text := item.State
	if item.State == "present" {
		text = "present · Helm markers: " + item.Markers
	}
	if item.State == "outside-namespace" {
		text = "foreign namespace: not requested"
	}
	if item.Terminating {
		text += " · terminating"
	}
	if item.Finalizers {
		text += " · finalizers present"
	}
	if item.Owners {
		text += " · owner references present"
	}
	if item.Keep || item.Hook {
		text += " · live lifecycle annotations present"
	}
	ui.Text(c, text).Label(fmt.Sprintf("Helm live object %d", i+1)).FontSize(11).MinHeight(18).Shrink(0)
}
