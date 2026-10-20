package uiworkbench

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/kube"
)

func TestNativeHelmAdmissionReview(t *testing.T) {
	f := newNativeHelmUninstallFixture(t, 1)
	h := f.harness(t)
	openNativeHelmReview(h)
	path := h.w.helmJournalPath
	if h.w.helmUninstall.admission != nil {
		t.Fatal("opening a review silently checked local storage")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("opening a review created the journal", err)
	}
	helmAdmissionClickCheck(t, h, f, helmjournal.AdmissionNotInitialized)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("checking an uninitialized store created it", err)
	}
	if h.w.helmUninstall.prepareAck || h.w.helmUninstall.dryRunAck || h.w.helmUninstall.executeAck || h.w.helmUninstall.confirmation != "" {
		t.Fatal("local explanation granted an operation acknowledgement")
	}
	nativeHelmReach(h, "Helm admission uninitialized boundary")
	helmAdmissionInitialize(t, path)
	helmAdmissionClickCheck(t, h, f, helmjournal.AdmissionNoBlocker)
	report := h.w.helmUninstall.admission.Report()
	if !report.InventoryComplete || report.Records != 0 || report.Bytes != 0 {
		t.Fatal("empty existing storage was not completely inspected", report)
	}
	nativeHelmReach(h, "Helm admission reservation")
	owner, err := helmjournal.Open(path)
	nativeHelmJournalMust(t, err)
	t.Cleanup(func() { _ = owner.Close() })
	helmAdmissionClickCheck(t, h, f, helmjournal.AdmissionBusy)
	if h.w.helmUninstall.admission.Report().InventoryComplete {
		t.Fatal("busy storage displayed a verified zero inventory")
	}
	other, err := helmjournal.Open(path)
	if other != nil || !errors.Is(err, helmjournal.ErrBusy) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatal("the local check released another owner's lock", err)
	}
	// Corruption in a different guard scope still blocks the whole store.
	writer, err := owner.Begin(nativeHelmJournalPlan("different-storage-uid"))
	nativeHelmJournalMust(t, err)
	id := writer.ID()
	nativeHelmJournalMust(t, writer.Abort())
	nativeHelmJournalMust(t, owner.Close())
	file, err := os.OpenFile(filepath.Join(path, id+".wal"), os.O_APPEND|os.O_WRONLY, 0600)
	nativeHelmJournalMust(t, err)
	_, err = file.Write([]byte{0, 1})
	nativeHelmJournalMust(t, err)
	nativeHelmJournalMust(t, file.Close())
	helmAdmissionClickCheck(t, h, f, helmjournal.AdmissionCorrupt)
	if h.w.helmUninstall.admission.Report().RecordID != id {
		t.Fatal("the damaged blocking record was not identified")
	}
	nativeHelmReach(h, "Helm admission blocking record")
	saveNativeScreenshot(t, h.tt)
	h.click("Close uninstall review")
	h.click("Disconnect")
	h.click("Helm recovery")
	h.tt.Frame()
	if _, found := h.tt.Find(helmAdmissionCheckLabel); found {
		t.Fatal("offline history offered a current-selection admission check")
	}
	nativeHelmReach(h, "Helm admission selection needed")
	files := helmAdmissionFiles(t, path)
	_, _, _, _, requests := f.snapshot(t)
	h.w.reviewHelmAdmission()
	if h.w.helmUninstall.admission != nil || h.w.backend != nil {
		t.Fatal("offline history acquired current connection authority")
	}
	helmAdmissionUnchanged(t, f, path, files, len(requests))
}

func TestNativeHelmAdmissionHistoryAndMinimumWindow(t *testing.T) {
	f := newNativeHelmUninstallFixture(t, 1)
	h := f.harness(t)
	h.tt.SetPreferences(ui.Preferences{TextScale: 1.5, HighContrast: true, ReduceMotion: true})
	h.tt.SetLocale("ar")
	h.tt.SetDirection(ui.RightToLeft)
	openNativeHelmReview(h)
	s := h.w.helmUninstall
	id := helmAdmissionUnknown(t, f, h.ctx, s.backend, s.journalPath, s.session, s.namespace, s.row, nil)
	h.click("Close uninstall review")
	h.click("Refresh Helm metadata")
	h.pump(func() bool { return !h.w.helmReleases.busy })
	h.click("Select sh.helm.release.v1.example.v2")
	h.click("Review recovery history")
	if h.w.helmUninstall.row.Status != "uninstalling" {
		t.Fatal("fixture did not select the current post-dispatch metadata")
	}
	helmAdmissionClickCheck(t, h, f, helmjournal.AdmissionUnresolved)
	r := h.w.helmUninstall.admission.Report()
	if r.RecordID != id || r.UnresolvedKind != helmjournal.UnresolvedUnknown || !r.InventoryComplete || r.Records != 1 {
		t.Fatal("Unknown history was not specifically explained", r)
	}
	files := helmAdmissionFiles(t, s.journalPath)
	_, _, _, _, requests := f.snapshot(t)
	nativeHelmClick(h, "Review Helm admission record "+id)
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	current := &h.w.helmUninstall
	if current.mode != "history" || len(current.history) != 1 || current.selectedID != id || current.recovery == nil ||
		current.recovery.Outcome() != kube.HelmExecutionUnknown || !current.recovery.Complete() || current.observation != nil || current.reconcileAck {
		t.Fatal("blocker link did not reuse inert existing recovery details", current.status)
	}
	helmAdmissionUnchanged(t, f, s.journalPath, files, len(requests))
	nativeHelmReach(h, "Helm recovery outcome")
	helmAdmissionClickCheck(t, h, f, helmjournal.AdmissionUnresolved)
	nativeHelmReach(h, "Helm admission reservation")
	viewport, found := h.tt.Find("Helm uninstall inventory")
	status, statusFound := h.tt.Find("Connection status")
	if !found || !statusFound || viewport.H < 96 || viewport.Y+viewport.H > status.Y {
		t.Fatal("admission details collapsed the minimum-window viewport", viewport, status)
	}
	for _, label := range []string{"Helm uninstall status", "Stop Helm operation", "Close uninstall review"} {
		bounds, ok := h.tt.Find(label)
		if !ok || bounds.W <= 0 || bounds.H <= 0 || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > 1100 || bounds.Y+bounds.H > status.Y {
			t.Fatal("fixed local check actions were clipped", label, bounds, status)
		}
	}
	saveNativeScreenshot(t, h.tt)
}
