package uiworkbench

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/helmjournal"
)

func (w *Workbench) helmAdmissionView(c *ui.Context) {
	s := &w.helmUninstall
	if !w.helmAdmissionSelection() {
		ui.Text(c, "For local execution admission, connect and select current Helm metadata. Local history remains available while disconnected.").Label("Helm admission selection needed").FontSize(11)
		return
	}
	ui.Text(c, "Local journal check · current metadata selection").Label("Helm admission heading").Bold().FontSize(13)
	if s.mode == "history" {
		ui.Text(c, fmt.Sprintf("%s · %s/%s · revision %d · storage UID %s", s.contextName, s.namespace, s.row.Release, s.row.Number, s.row.UID)).Label("Helm admission scope").FontSize(11)
	}
	ui.Text(c, "Inspect existing local records only. No cluster reads, new journal records or execution permission.").Label("Helm admission boundary").FontSize(11)
	ui.Button(c, helmAdmissionCheckLabel).Disabled(!w.helmUninstallEligible() || s.journalPath == "").OnClick(w.reviewHelmAdmission)
	if s.admission == nil {
		return
	}
	r := s.admission.Report()
	ui.Text(c, helmAdmissionReasonText(r)).Label("Helm admission reason").Bold().FontSize(12)
	ui.Text(c, "Checked: "+s.admission.CheckedAt().UTC().Format("15:04:05 UTC")+" · historical local snapshot").Label("Helm admission checked at").FontSize(11)
	if r.InventoryComplete {
		ui.Text(c, fmt.Sprintf("Retained records: %d / %d · retained bytes: %d / %d", r.Records, helmjournal.MaxRecords, r.Bytes, helmjournal.MaxStoreBytes)).Label("Helm admission capacity").FontSize(11)
	} else {
		ui.Text(c, "Record count and retained bytes were not completely verified.").Label("Helm admission capacity unknown").FontSize(11)
	}
	ui.Text(c, fmt.Sprintf("A new operation requires one record slot and a %d KiB reservation within the %d MiB store budget. Completed history still uses capacity.", helmjournal.MaxOperationBytes>>10, helmjournal.MaxStoreBytes>>20)).Label("Helm admission reservation").FontSize(11)
	if helmAdmissionRecordID(r.RecordID) {
		id := r.RecordID
		ui.Text(c, "Blocking record found: "+id+" · this is not a complete blocker list").Label("Helm admission blocking record").FontSize(11)
		ui.Button(c, "Review blocking record").Label("Review Helm admission record " + id).Disabled(!w.helmUninstallEligible()).OnClick(func() { w.openHelmAdmissionHistory(id) })
	}
	if r.Reason == helmjournal.AdmissionNotInitialized {
		ui.Text(c, "Independent execution confirmation may initialize private storage; execution must still recheck it.").Label("Helm admission uninitialized boundary").FontSize(11)
	}
	ui.Text(c, "This snapshot grants no execution permission. Execution rechecks the journal under its lock; current observations do not clear retained uncertainty.").Label("Helm admission not authority").FontSize(11)
}
