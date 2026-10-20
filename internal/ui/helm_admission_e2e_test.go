//go:build e2e

package uiworkbench

import (
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/testcluster"
)

func TestNativeHelmJournalAdmissionAgainstRealCluster(t *testing.T) {
	f := testcluster.NewHelmNativeUninstall(t)
	h := newRelationshipHarness(t, 90*time.Second)
	path := filepath.Join(t.TempDir(), "journal")
	h.w.helmJournalPath = path
	audit := &nativeHelmAudit{loseDelete: true}
	connectNativeHelmFixture(t, h, f, audit)
	selectNativeHelmFixture(h, f)
	h.click("Review restricted uninstall")
	if h.w.helmUninstall.admission != nil || helmAdmissionFiles(t, path) != nil {
		t.Fatal("opening native review checked or created local storage")
	}

	// Only the native button initiates this existing-only local read. Its
	// selected metadata is sufficient; no release-content grant is involved.
	check := func(reason helmjournal.AdmissionReason) helmjournal.Admission {
		t.Helper()
		beforeFiles := helmAdmissionFiles(t, path)
		beforeRequests, _ := audit.snapshot()
		nativeHelmE2EClick(h, helmAdmissionCheckLabel)
		h.pump(func() bool { return h.w.helmUninstall.busy == "" })
		review := h.w.helmUninstall.admission
		if review == nil || review.Report().Reason != reason || h.w.pendingWrites != 0 ||
			h.w.helmUninstall.prepareAck || h.w.helmUninstall.dryRunAck || h.w.helmUninstall.executeAck || h.w.helmUninstall.confirmation != "" {
			t.Fatal("native local explanation changed authority or lost its reason", h.w.helmUninstall.status)
		}
		afterRequests, _ := audit.snapshot()
		if !reflect.DeepEqual(beforeRequests, afterRequests) || !reflect.DeepEqual(beforeFiles, helmAdmissionFiles(t, path)) {
			t.Fatal("native local explanation sent HTTP or changed journal bytes or permissions")
		}
		return review.Report()
	}
	initial := check(helmjournal.AdmissionNotInitialized)
	if initial.InventoryComplete || helmAdmissionFiles(t, path) != nil || h.w.helmUninstall.preparation != nil {
		t.Fatal("uninitialized storage was created or presented as verified admission")
	}
	h.click("Close uninstall review")
	h.click("Close Helm metadata")

	// Retain an actual uncertain server effect, using the established native
	// confirmations and a response loss only after the real API admits DELETE.
	result := runNativeHelmUninstall(t, h, f, audit)
	_, lost := audit.snapshot()
	writes := audit.writes()
	if result.Outcome() != kube.HelmExecutionUnknown || !lost || len(writes) != 2 || writes[0].kind != "begin" || writes[1].kind != "delete" ||
		writes[1].status < 200 || writes[1].status >= 300 {
		t.Fatal("admission fixture did not retain the real accepted-response Unknown", h.w.helmUninstall.status)
	}
	assertNativeHelmTargets(t, h.ctx, f, 1)
	assertNativeHelmRetained(t, h.ctx, f, "uninstalling")
	h.click("Close uninstall review")
	h.click("Refresh Helm metadata")
	h.pump(func() bool { return !h.w.helmReleases.busy })
	h.click("Select " + f.Storage.Name)
	h.click("Review recovery history")
	if h.w.helmUninstall.row.Status != "uninstalling" {
		t.Fatal("local check did not use the current metadata selection")
	}
	report := check(helmjournal.AdmissionUnresolved)
	if report.RecordID != result.JournalID() || report.UnresolvedKind != helmjournal.UnresolvedUnknown || !report.InventoryComplete || report.Records != 1 {
		t.Fatal("native explanation lost the exact retained Unknown blocker", report)
	}

	files := helmAdmissionFiles(t, path)
	beforeHistory, _ := audit.snapshot()
	nativeHelmE2EClick(h, "Review Helm admission record "+report.RecordID)
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	recovered := h.w.helmUninstall.recovery
	afterHistory, _ := audit.snapshot()
	if recovered == nil || recovered.ID() != result.JournalID() || recovered.Outcome() != kube.HelmExecutionUnknown || !recovered.Complete() ||
		!reflect.DeepEqual(recovered.Targets(), result.Targets()) || !reflect.DeepEqual(recovered.Effects(), result.Effects()) ||
		h.w.helmUninstall.reconcileAck || h.w.helmUninstall.observation != nil ||
		!reflect.DeepEqual(beforeHistory, afterHistory) || !reflect.DeepEqual(files, helmAdmissionFiles(t, path)) {
		t.Fatal("blocker navigation changed history or silently observed the cluster")
	}

	// Independent, explicitly confirmed GET facts must not clear the journal's
	// Unknown guard or turn the next local explanation into execution consent.
	nativeHelmE2EClick(h, helmRecoveryConsentLabel)
	nativeHelmE2EClick(h, "Observe current metadata")
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	observation := h.w.helmUninstall.observation
	if observation == nil || observation.HistoryOutcome() != kube.HelmExecutionUnknown || h.w.helmUninstall.reconcileAck {
		t.Fatal("independent GET observation lost the retained historical outcome", h.w.helmUninstall.status)
	}
	items := observation.Items()
	if len(items) != 2 || items[0].State != kube.HelmRecoveryTargetAbsent || items[1].State != kube.HelmRecoveryTargetOriginal {
		t.Fatal("independent GET facts did not describe the actual accepted and unsubmitted targets")
	}
	afterObservation, _ := audit.snapshot()
	for _, request := range afterObservation[len(afterHistory):] {
		if request.method != http.MethodGet {
			t.Fatal("current recovery facts submitted a mutation")
		}
	}
	if !reflect.DeepEqual(files, helmAdmissionFiles(t, path)) {
		t.Fatal("current observations changed local history")
	}
	again := check(helmjournal.AdmissionUnresolved)
	if again.RecordID != report.RecordID || again.UnresolvedKind != helmjournal.UnresolvedUnknown ||
		recovered.Outcome() != kube.HelmExecutionUnknown || !reflect.DeepEqual(writes, audit.writes()) {
		t.Fatal("local explanation cleared uncertainty or replayed a retained operation")
	}
	nativeHelmE2EReach(h, "Helm admission not authority")
	saveNativeScreenshot(t, h.tt)
	h.click("Close uninstall review")
	h.click("Disconnect")
	if h.w.helmUninstall.admission != nil || !reflect.DeepEqual(files, helmAdmissionFiles(t, path)) || !reflect.DeepEqual(writes, audit.writes()) {
		t.Fatal("native teardown changed retained history or preserved an admission snapshot")
	}
}
