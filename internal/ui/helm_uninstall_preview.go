package uiworkbench

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

func (w *Workbench) expireHelmUninstall(now time.Time) {
	r := &w.helmReleases
	if r.preview != nil && !r.preview.Matches(w.sessionID, strings.TrimSpace(w.namespace), r.selected, now) {
		r.preview = nil
		r.live = nil
		r.liveAcknowledged = false
		r.liveStatus = ""
		if r.contentCancel != nil {
			r.contentCancel()
			r.contentCancel = nil
		}
		r.inspecting = false
		r.acknowledged = false
		r.contentEpoch++
		r.contentStatus = "Helm scope preview expired or changed; explicitly read again."
	}
	if r.live != nil && !r.live.Matches(r.preview, w.sessionID, strings.TrimSpace(w.namespace), r.selected, now) {
		r.live = nil
		r.liveAcknowledged = false
		r.contentEpoch++
		r.liveStatus = "Live metadata observation expired; explicitly check again."
	}
}

func (w *Workbench) helmUninstallPreviewView(c *ui.Context) {
	p := w.helmReleases.preview
	if p == nil {
		return
	}
	// Real wall/monotonic time bounds this observation. Toolkit time is only a
	// request for a repaint, never the authority for expiry or network deadlines.
	remaining := time.Until(p.ExpiresAt())
	if remaining > 0 {
		c.After(remaining)
	}
	items, summary := p.Items(), p.Summary()
	liveItems := w.helmReleases.live.Items()
	ui.Text(c, "Uninstall scope preview — NOT executable").Bold()
	// Keep only the nonexecution warning and read/discard controls pinned.
	// Observations and descriptors share one scroll viewport; fixed warning rows
	// must not squeeze glyphs to a near-zero height when controls wrap.
	ui.Scroll(c.Key("helm.uninstall.inventory." + p.ID())).Label("Helm uninstall inventory").Grow(1).Basis(0).MinHeight(0).Gap(6).Children(func() {
		ui.Text(c, fmt.Sprintf("Stored revision %d (%s) · %d object(s) · %d stored hook(s)", p.Revision().Number, p.Revision().Status, len(items), summary.HookCount)).Label("Helm uninstall preview counts").FontSize(12)
		ui.Text(c, "Not necessarily latest. Stored scope alone has no live checks. Dependents and dry-run remain unchecked.").FontSize(11)
		ui.Text(c, "Hooks/history and chart CRDs outside the stored manifest remain unplanned. Order below is NOT deletion order.").FontSize(11)
		if s := p.Revision().Status; s != "deployed" && s != "failed" {
			ui.Text(c, "Selected status is historical, pending or unknown; do not infer an uninstallable release.").FontSize(11)
		}
		ui.Text(c, "Expires: "+p.ExpiresAt().UTC().Format("15:04:05 UTC")+" · Refresh/discard invalidates this observation.").Label("Helm uninstall expiry").FontSize(11)

		w.helmLiveControlsView(c)

		for i, item := range items {
			declared := item.Namespace
			if declared == "" {
				declared = "(namespace/scope unresolved)"
			}
			flags := []string{}
			if item.Keep {
				flags = append(flags, "keep policy observed")
			}
			if item.Hook {
				flags = append(flags, "hook annotation observed")
			}
			if item.CRD {
				flags = append(flags, "CRD: separate lifecycle")
			}
			if item.ForeignNamespace {
				flags = append(flags, "foreign namespace")
			}
			ui.Column(c.Key(fmt.Sprintf("helm.uninstall.item.%d", i))).Padding(4).Gap(2).Shrink(0).Children(func() {
				ui.Text(c, fmt.Sprintf("%d. %s %s · %s / %s", i+1, item.APIVersion, item.Kind, declared, item.Name)).Label(fmt.Sprintf("Helm uninstall object %d", i+1)).SingleLine().FontSize(12).MinHeight(20).Shrink(0)
				if len(liveItems) == len(items) {
					helmLiveItemView(c, i, liveItems[i])
				}
				if len(flags) != 0 {
					ui.Text(c, strings.Join(flags, " · ")).FontSize(11)
				}
			})
		}
		if len(items) == 0 {
			ui.Text(c, "Empty stored manifest does not imply an empty uninstall.").FontSize(11)
		}
	})
}
