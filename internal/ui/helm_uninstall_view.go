package uiworkbench

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kube"
)

// The inventory and acknowledgements share one scroll viewport. Only the
// current operation state and its stop/close controls stay fixed, so a large
// reviewed target set or text scale cannot squeeze those controls out of view.
func (w *Workbench) helmUninstallView(c *ui.Context) {
	s := &w.helmUninstall
	history := s.mode == "history"
	title, closeLabel := "Restricted Helm uninstall", "Close uninstall review"
	if history {
		title = "Helm recovery history"
	}
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(12).Gap(8).Children(func() {
		ui.Text(c, title).Bold().FontSize(20)
		ui.Scroll(c.Key(fmt.Sprintf("helm.uninstall.flow.%d", s.epoch))).Label("Helm uninstall inventory").Grow(1).Basis(0).MinHeight(0).Gap(8).Children(func() {
			ui.Text(c, "Recovery directory: "+s.journalPath).Label("Helm journal directory").FontSize(11)
			if s.journalPath == "" {
				ui.Text(c, "Private recovery directory unavailable; execution and local history are unavailable.").Label("Helm journal unavailable").FontSize(12)
			}
			ui.Text(c, "Private operation metadata is retained here across restarts. No credentials or release contents are stored.").FontSize(11)
			if history {
				w.helmRecoveryView(c)
			} else {
				w.helmUninstallReviewView(c)
			}
		})
		ui.Column(c).Gap(5).Shrink(0).Children(func() {
			ui.Text(c, s.status).Label("Helm uninstall status").FontSize(12)
			ui.Row(c).Gap(8).Wrap().Children(func() {
				ui.Button(c, "Stop Helm operation").Disabled(s.busy == "" || s.stopRequested).OnClick(w.stopHelmUninstall)
				ui.Button(c, closeLabel).OnClick(w.clearHelmUninstall)
			})
		})
	})
}

func (w *Workbench) helmUninstallReviewView(c *ui.Context) {
	s := &w.helmUninstall
	editable := w.helmUninstallEligible() && !s.attempted
	ui.Text(c, fmt.Sprintf("Context: %s · Namespace: %s", s.contextName, s.namespace)).Label("Helm uninstall scope").Bold().FontSize(13)
	ui.Text(c, fmt.Sprintf("Release: %s · revision %d · %s", s.row.Release, s.row.Number, s.row.Status)).Label("Helm uninstall revision").FontSize(12)
	ui.Text(c, fmt.Sprintf("Storage: %s · UID %s · RV %s", s.row.StorageName, s.row.UID, s.row.ResourceVersion)).Label("Helm uninstall storage identity").FontSize(11)
	ui.Text(c, "This profile permits one deployed stored revision and 1–32 plain ConfigMaps in this namespace. Unsupported releases are rejected as a whole.").FontSize(12)
	ui.Text(c, "Execution retains Helm history, uses Orphan deletion, and may stop after a partial or uncertain effect. Cancellation does not undo an accepted request.").Label("Helm uninstall consequences").FontSize(12)
	w.helmAdmissionView(c)
	if s.preparation == nil && !s.attempted {
		guardedCheckbox(c, fmt.Sprintf("helm.prepare.ack.%d", s.epoch), &s.prepareAck, helmPrepareConsentLabel, editable).Label("Helm preparation consent")
		ui.Button(c, "Prepare restricted uninstall").Disabled(!editable || !s.prepareAck).OnClick(w.prepareHelmUninstall)
	}
	if p := s.preparation; p != nil {
		ui.Text(c, "Reviewed plan: "+p.ID()).Label("Helm uninstall preparation identity").FontSize(11)
		ui.Text(c, "Original review expires: "+p.ExpiresAt().UTC().Format("15:04:05 UTC")).Label("Helm uninstall review expiry").FontSize(12)
		if remaining := time.Until(p.ExpiresAt()); remaining > 0 {
			c.After(remaining)
		}
		ui.Text(c, "Ordered plan: mark the retained revision uninstalling, delete the exact objects below, then mark retained history uninstalled.").FontSize(12)
		for i, target := range p.Targets() {
			ui.Column(c.Key(fmt.Sprintf("helm.uninstall.review.target.%d", i))).Padding(4).Gap(2).Shrink(0).Children(func() {
				ui.Text(c, fmt.Sprintf("%d. %s %s/%s", i+1, target.Kind, target.Namespace, target.Name)).Label(fmt.Sprintf("Helm uninstall reviewed target %d", i+1)).FontSize(12).MinHeight(20).Shrink(0)
				ui.Text(c, "UID: "+target.UID+" · RV: "+target.ResourceVersion).Label(fmt.Sprintf("Helm uninstall reviewed identity %d", i+1)).FontSize(11).MinHeight(18).Shrink(0)
			})
		}
		if s.dryRun == nil && !s.attempted {
			ui.Text(c, "Server dry-run uses fresh sensitive reads and PATCH/DELETE permissions. Its result expires with this original review and does not authorize execution.").FontSize(12)
			guardedCheckbox(c, fmt.Sprintf("helm.dryrun.ack.%d", s.epoch), &s.dryRunAck, helmDryRunConsentLabel, editable).Label("Helm dry-run consent")
			ui.Button(c, "Run uninstall dry-run").Disabled(!editable || !s.dryRunAck).OnClick(w.preflightHelmUninstall)
		}
	}
	if s.dryRun != nil && !s.attempted {
		ui.Text(c, "Server dry-run passed for this exact review. Final execution performs fresh checks before conditional writes.").Label("Helm uninstall dry-run result").FontSize(12)
		ui.Text(c, "Confirm this release and the private history location above. Uncertain history remains retained and blocks another attempt for this release identity.").FontSize(12)
		guardedCheckbox(c, fmt.Sprintf("helm.execute.ack.%d", s.epoch), &s.executeAck, helmExecuteConsentLabel, editable).Label("Helm execution consent")
		s.confirmationComposing = guardedTextInput(c, fmt.Sprintf("helm.execute.confirm.%d", s.epoch), &s.confirmation, editable).Label("Helm uninstall release confirmation").Placeholder("Type exact release name: " + s.row.Release).Composing()
		ui.Button(c, "Execute restricted uninstall").Disabled(!editable || s.journalPath == "" || !s.executeAck || s.confirmationComposing || s.confirmation != s.row.Release).OnClick(w.executeHelmUninstall)
	}
	if result := s.execution; result != nil {
		ui.Text(c, "Server outcome: "+string(result.Outcome())).Label("Helm uninstall outcome").Bold().FontSize(14)
		ui.Text(c, fmt.Sprintf("Journal terminal saved: %t · record %s", result.JournalComplete(), result.JournalID())).Label("Helm uninstall journal outcome").FontSize(12)
		ui.Text(c, "The server outcome and saved history are separate. A saved terminal record can still contain an uncertain server effect.").FontSize(11)
		helmExecutionEffectsView(c, result.Effects(), "Helm uninstall effect")
	}
}

func helmExecutionEffectsView(c *ui.Context, effects []kube.HelmUninstallExecutionEffect, label string) {
	for i, effect := range effects {
		ui.Column(c.Key(fmt.Sprintf("%s.%d", label, i))).Padding(4).Gap(2).Shrink(0).Children(func() {
			ui.Text(c, fmt.Sprintf("%d. %s %s/%s · %s", i+1, effect.Kind, effect.Namespace, effect.Name, effect.Outcome)).Label(fmt.Sprintf("%s %d", label, i+1)).FontSize(12).MinHeight(20).Shrink(0)
			ui.Text(c, "UID: "+effect.UID+" · request RV: "+effect.ResourceVersion+" · observed RV: "+effect.ObservedResourceVersion).FontSize(11)
		})
	}
}

func (w *Workbench) helmRecoveryView(c *ui.Context) {
	s := &w.helmUninstall
	idle := s.busy == "" && w.pendingWrites == 0 && w.ctx.Err() == nil
	ui.Text(c, "Loading local history is offline. It restores recorded facts only; current cluster reads require a new connection, current revision selection and separate confirmation.").Label("Helm recovery boundary").FontSize(12)
	w.helmAdmissionView(c)
	ui.Button(c, "Reload local history").Disabled(!idle || s.journalPath == "").OnClick(w.loadHelmRecovery)
	ui.Column(c.Key("helm.recovery.history")).Label("Helm recovery records").Gap(4).Children(func() {
		for _, entry := range s.history {
			text := entry.id
			if r := entry.recovery; r != nil {
				text = fmt.Sprintf("%s · %s/%s · %s", entry.id, r.Namespace(), r.Revision().Release, r.Outcome())
			}
			ui.Button(c, text).Label("Select Helm recovery " + entry.id).Disabled(!idle).OnClick(func() { w.selectHelmRecovery(entry.id) })
		}
	})
	if r := s.recovery; r != nil {
		ui.Text(c, "Selected record: "+r.ID()).Label("Helm recovery identity").FontSize(12)
		ui.Text(c, fmt.Sprintf("Recorded release: %s/%s · revision %d · storage UID %s", r.Namespace(), r.Revision().Release, r.Revision().Number, r.Revision().UID)).Label("Helm recovery source").FontSize(12)
		ui.Text(c, "Historical outcome: "+string(r.Outcome())).Label("Helm recovery outcome").Bold().FontSize(14)
		ui.Text(c, fmt.Sprintf("Checked terminal record: %t · damaged history: %t", r.Complete(), r.Corrupt())).Label("Helm recovery completeness").FontSize(12)
		if r.Corrupt() || s.recoveryErr != nil {
			ui.Text(c, "Damaged history: only the checked prefix is shown. This record cannot authorize current cluster observation.").Label("Helm recovery damaged").FontSize(12)
		}
		helmExecutionEffectsView(c, r.Effects(), "Helm recovery effect")
		ui.Text(c, "Uncertain history stays uncertain. Current observations do not clear its protection against another execution or resume this operation.").Label("Helm recovery retained uncertainty").FontSize(12)
		if s.backend == nil || s.row.StorageName == "" {
			ui.Text(c, "For current facts, connect and select this release's current metadata, then open Review recovery history.").Label("Helm recovery connection needed").FontSize(12)
		} else {
			ui.Text(c, fmt.Sprintf("Current selection: %s · %s/%s · revision %d · UID %s · RV %s", s.contextName, s.namespace, s.row.Release, s.row.Number, s.row.UID, s.row.ResourceVersion)).Label("Helm recovery current selection").FontSize(12)
			allowed := w.helmUninstallEligible() && !r.Corrupt() && s.recoveryErr == nil
			guardedCheckbox(c, fmt.Sprintf("helm.recovery.ack.%d.%s", s.epoch, s.selectedID), &s.reconcileAck, helmRecoveryConsentLabel, allowed).Label("Helm recovery observation consent")
			ui.Button(c, "Observe current metadata").Disabled(!allowed || !s.reconcileAck).OnClick(w.observeHelmRecovery)
		}
	}
	if o := s.observation; o != nil {
		ui.Text(c, "Current metadata observation · historical outcome remains "+string(o.HistoryOutcome())).Label("Helm recovery observation").Bold().FontSize(14)
		ui.Text(c, "Observed: "+o.FinishedAt().UTC().Format("15:04:05 UTC")).Label("Helm recovery observed at").FontSize(12)
		for i, item := range o.Items() {
			flags := make([]string, 0, 7)
			for _, flag := range []struct {
				present bool
				text    string
			}{{item.Terminating, "terminating"}, {item.Finalizers, "finalizers"}, {item.Owners, "owners"}, {item.Keep, "keep"}, {item.Hook, "hook"}} {
				if flag.present {
					flags = append(flags, flag.text)
				}
			}
			if item.Markers != "" {
				flags = append(flags, "markers: "+item.Markers)
			}
			ui.Column(c.Key(fmt.Sprintf("helm.recovery.observed.%d", i))).Padding(4).Gap(2).Shrink(0).Children(func() {
				ui.Text(c, fmt.Sprintf("%d. %s/%s · %s", i+1, item.Target.Namespace, item.Target.Name, item.State)).Label(fmt.Sprintf("Helm recovery current target %d", i+1)).FontSize(12).MinHeight(20).Shrink(0)
				ui.Text(c, "Current UID: "+item.UID+" · RV: "+item.ResourceVersion).FontSize(11)
				if len(flags) > 0 {
					ui.Text(c, strings.Join(flags, " · ")).FontSize(11)
				}
			})
		}
	}
}
