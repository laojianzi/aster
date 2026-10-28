package uiworkbench

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kube"
)

func (w *Workbench) helmInstallRecoveryView(c *ui.Context) {
	s := &w.helmUninstall
	r := s.installRecovery
	if r == nil {
		return
	}
	location := "Active"
	if r.Archived() {
		location = "Archived"
	}
	ui.Text(c, "Installation record: "+r.ID()).Label("Helm install recovery identity").FontSize(12)
	ui.Text(c, "Record location: "+location+" · original bytes retained").Label("Helm install recovery location").FontSize(12)
	ui.Text(c, fmt.Sprintf("Recorded installation: %s/%s · first revision · storage %s", r.Namespace(), r.Release(), r.StorageName())).Label("Helm install recovery source").FontSize(12)
	uid, rv := r.NamespaceIdentity()
	ui.Text(c, "Recorded namespace UID: "+uid+" · RV: "+rv).Label("Helm install recovery namespace").FontSize(11)
	ui.Text(c, "Historical outcome: "+r.Outcome()).Label("Helm install recovery outcome").Bold().FontSize(14)
	ui.Text(c, fmt.Sprintf("Checked terminal record: %t · damaged history: %t", r.Complete(), r.Corrupt())).Label("Helm install recovery completeness").FontSize(12)
	if r.Corrupt() || s.recoveryErr != nil {
		ui.Text(c, "Damaged installation history: only the checked prefix is shown. This record cannot authorize current cluster observation.").Label("Helm install recovery damaged").FontSize(12)
	}
	for i, effect := range r.Effects() {
		ui.Column(c.Key(fmt.Sprintf("helm.install.recovery.effect.%d", i))).Padding(4).Gap(2).Shrink(0).Children(func() {
			ui.Text(c, fmt.Sprintf("%d. %s %s/%s · %s", i+1, effect.Kind, effect.Namespace, effect.Name, effect.Outcome)).Label(fmt.Sprintf("Helm install recovery effect %d", i+1)).FontSize(12).MinHeight(20).Shrink(0)
			if effect.UID == "" {
				ui.Text(c, "No accepted UID was recorded. A same-name object cannot be attributed to this installation.").Label(fmt.Sprintf("Helm install recovery unassigned %d", i+1)).FontSize(11)
			} else {
				ui.Text(c, "Recorded UID: "+effect.UID+" · request RV: "+effect.ResourceVersion+" · observed RV: "+effect.ObservedResourceVersion).FontSize(11)
			}
		})
	}
	ui.Text(c, "Historical uncertainty stays retained. Current metadata does not establish ownership, clear unresolved-operation protection, or resume installation.").Label("Helm install recovery retained uncertainty").FontSize(12)
	if s.backend == nil {
		ui.Text(c, "For current facts, connect to the recorded cluster and namespace, then reload and select this installation record.").Label("Helm install recovery connection needed").FontSize(12)
	} else {
		ui.Text(c, fmt.Sprintf("Current connection: %s · namespace %s", s.contextName, s.namespace)).Label("Helm install recovery current connection").FontSize(12)
		ui.Text(c, "This separate confirmation allows bounded metadata GETs for the recorded namespace, release storage and ConfigMaps. A current Helm revision is not required.").Label("Helm install recovery observation boundary").FontSize(12)
		allowed := w.helmUninstallEligible() && !r.Corrupt() && s.recoveryErr == nil && s.namespace == r.Namespace()
		guardedCheckbox(c, fmt.Sprintf("helm.install.recovery.ack.%d.%s", s.epoch, s.selectedID), &s.reconcileAck, helmRecoveryConsentLabel, allowed).Label("Helm install recovery observation consent")
		ui.Button(c, "Observe current metadata").Disabled(!allowed || !s.reconcileAck).OnClick(w.observeHelmRecovery)
	}
	if o := s.installObservation; o != nil {
		ui.Text(c, "Current installation metadata · historical outcome remains "+o.HistoryOutcome()).Label("Helm install recovery observation").Bold().FontSize(14)
		ui.Text(c, "Observed: "+o.FinishedAt().UTC().Format("15:04:05 UTC")).Label("Helm install recovery observed at").FontSize(12)
		uid, rv := o.NamespaceIdentity()
		ui.Text(c, "Current namespace UID: "+uid+" · RV: "+rv).Label("Helm install recovery current namespace").FontSize(11)
		helmInstallRecoveryItemView(c, o.Storage(), "Helm install recovery current storage")
		for i, item := range o.Items() {
			helmInstallRecoveryItemView(c, item, fmt.Sprintf("Helm install recovery current target %d", i+1))
		}
		ui.Text(c, fmt.Sprintf("Current Helm metadata: %d revision(s)", len(o.Revisions()))).Label("Helm install recovery current revisions").FontSize(12)
		for i, row := range o.Revisions() {
			ui.Text(c, fmt.Sprintf("Revision %d · %s · %s · UID %s · RV %s", row.Number, row.Status, row.StorageName, row.UID, row.ResourceVersion)).Label(fmt.Sprintf("Helm install recovery current revision %d", i+1)).FontSize(11)
		}
	}
}

func helmInstallRecoveryItemView(c *ui.Context, item kube.HelmInstallRecoveryItem, label string) {
	ui.Column(c.Key(label)).Padding(4).Gap(2).Shrink(0).Children(func() {
		ui.Text(c, fmt.Sprintf("%s %s/%s · %s", item.Kind, item.Namespace, item.Name, item.State)).Label(label).FontSize(12).MinHeight(20).Shrink(0)
		ui.Text(c, "Current UID: "+item.UID+" · RV: "+item.ResourceVersion).FontSize(11)
		flags := make([]string, 0, 6)
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
		if len(flags) > 0 {
			ui.Text(c, strings.Join(flags, " · ")).FontSize(11)
		}
	})
}
