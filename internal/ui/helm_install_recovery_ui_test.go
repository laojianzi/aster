package uiworkbench

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/kube"
)

func assertNativeHelmInstallHistoryUnchanged(t *testing.T, f *nativeHelmInstallRecoveryFixture) {
	t.Helper()
	after, err := os.ReadFile(filepath.Join(f.path, f.id+".wal"))
	nativeHelmJournalMust(t, err)
	if !bytes.Equal(after, f.original) {
		t.Fatal("read-only installation history flow changed original WAL bytes")
	}
	s, err := helmjournal.OpenExisting(f.path)
	nativeHelmJournalMust(t, err)
	defer s.Close()
	ids, err := s.List()
	nativeHelmJournalMust(t, err)
	if len(ids) != 1 || ids[0] != f.id {
		t.Fatal("recovery minted or discarded a journal operation")
	}
	admission, err := s.ReviewAdmission(context.Background(), helmjournal.AdmissionScope{Binding: f.plan.Binding, Namespace: f.plan.Namespace, Release: f.plan.Release})
	if !errors.Is(err, helmjournal.ErrUnresolved) || admission.Reason != helmjournal.AdmissionUnresolved || admission.RecordID != f.id {
		t.Fatal("current metadata cleared retained unresolved installation protection")
	}
}

func TestNativeHelmInstallRecoveryCorruptHistoryCannotObserve(t *testing.T) {
	f := newNativeHelmInstallRecoveryFixture(t, helmjournal.Unknown)
	file := filepath.Join(f.path, f.id+".wal")
	wal, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0600)
	nativeHelmJournalMust(t, err)
	_, err = wal.Write([]byte{0, 1})
	nativeHelmJournalMust(t, err)
	nativeHelmJournalMust(t, wal.Close())
	before, err := os.ReadFile(file)
	nativeHelmJournalMust(t, err)
	h := f.harness(t, true)
	selectNativeHelmInstallHistory(t, h, f.id)
	r := h.w.helmUninstall.installRecovery
	if r == nil || !r.Corrupt() || r.Complete() || !errors.Is(h.w.helmUninstall.recoveryErr, helmjournal.ErrCorrupt) {
		t.Fatal("damaged v2 prefix became a valid selectable grant source")
	}
	nativeHelmClick(h, helmRecoveryConsentLabel)
	if h.w.helmUninstall.reconcileAck {
		t.Fatal("damaged installation history enabled read confirmation")
	}
	h.w.helmUninstall.reconcileAck = true // The action itself must also reject a stale/forged UI acknowledgement.
	h.w.observeHelmRecovery()
	if f.requestCount() != 0 || h.w.helmUninstall.installObservation != nil || h.w.helmUninstall.busy != "" {
		t.Fatal("damaged installation history authorized a metadata request")
	}
	after, err := os.ReadFile(file)
	nativeHelmJournalMust(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("viewing damaged installation history repaired the original")
	}
	nativeHelmReach(h, "Helm install recovery damaged")
	saveNativeScreenshot(t, h.tt)
}

func TestNativeHelmInstallRecoveryOfflineAndFreshObservation(t *testing.T) {
	f := newNativeHelmInstallRecoveryFixture(t, helmjournal.Unknown)
	h := f.harness(t, false)
	h.click("Helm recovery")
	nativeHelmClick(h, "Reload local history")
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	nativeHelmClick(h, "Select Helm recovery "+f.id)
	r := h.w.helmUninstall.installRecovery
	if r == nil || r.Outcome() != helmjournal.Unknown || !r.Complete() || !time.Now().After(r.ExpiresAt()) ||
		h.w.backend != nil || f.requestCount() != 0 || h.w.helmUninstall.installObservation != nil {
		t.Fatal("offline installation history recovered authority or changed historical facts")
	}
	h.w.observeHelmRecovery()
	if f.requestCount() != 0 {
		t.Fatal("offline selection made a cluster request")
	}
	nativeHelmReach(h, "Helm install recovery outcome")
	saveNativeScreenshot(t, h.tt)
	h.click("Close uninstall review")
	h.w.backend, h.w.namespace, h.w.sessionID = f.backend(t), "team", "independent-current-session"
	h.w.activeContext, h.w.currentContext = "test-context", "test-context"
	selectNativeHelmInstallHistory(t, h, f.id)
	if h.w.helmUninstall.row.StorageName != "" || h.w.helmReleases.selected.StorageName != "" {
		t.Fatal("fixture accidentally supplied a current revision")
	}
	h.w.observeHelmRecovery()
	if f.requestCount() != 0 || h.w.helmUninstall.installObservation != nil {
		t.Fatal("selected history silently acknowledged current reads")
	}
	nativeHelmClick(h, helmRecoveryConsentLabel)
	nativeHelmClick(h, "Observe current metadata")
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	o := h.w.helmUninstall.installObservation
	if o == nil || o.ID() != f.id || o.HistoryOutcome() != helmjournal.Unknown || h.w.helmUninstall.installRecovery.Outcome() != helmjournal.Unknown ||
		len(o.Items()) != 1 || string(o.Items()[0].State) != "unattributed-present" || string(o.Storage().State) != "unattributed-present" ||
		h.w.helmUninstall.reconcileAck || h.w.helmUninstall.installReconcileConsent != nil {
		t.Fatal("new metadata observation changed history, assigned an unrecorded UID, or retained consent", h.w.helmUninstall.status)
	}
	before := f.requestCount()
	h.w.observeHelmRecovery()
	if f.requestCount() != before || h.w.helmUninstall.installObservation != o {
		t.Fatal("observation confirmation was silently reused")
	}
	nativeHelmReach(h, "Helm install recovery current target 1")
	if strings.Contains(strings.Join(h.tt.Texts(), "\n"), "PRIVATE_INSTALL") {
		t.Fatal("private transport content escaped into installation recovery UI")
	}
	saveNativeScreenshot(t, h.tt)
	assertNativeHelmInstallHistoryUnchanged(t, f)
	h.w.selectHelmRecovery(f.id)
	if h.w.helmUninstall.reconcileAck || h.w.helmUninstall.installObservation != nil || f.requestCount() != before {
		t.Fatal("record reselection kept current facts or implicit confirmation")
	}
}

func TestNativeHelmInstallRecoveryHistoryAndCurrentFactsRemainSeparate(t *testing.T) {
	for _, outcome := range []string{helmjournal.NotStarted, helmjournal.AcceptedPending, "Partial", helmjournal.Unknown} {
		t.Run(outcome, func(t *testing.T) {
			f := newNativeHelmInstallRecoveryFixture(t, outcome)
			f.setMode("absent")
			h := f.harness(t, true)
			selectNativeHelmInstallHistory(t, h, f.id)
			h.tt.SetSize(640, 480)
			h.tt.SetScale(2)
			h.tt.Frame()
			nativeHelmReach(h, "Helm install recovery outcome")
			if h.w.helmUninstall.installRecovery.Outcome() != outcome || h.w.helmUninstall.recovery != nil {
				t.Fatal("v2 historical outcome was hidden or presented as uninstall history")
			}
			nativeHelmClick(h, helmRecoveryConsentLabel)
			nativeHelmClick(h, "Observe current metadata")
			h.pump(func() bool { return h.w.helmUninstall.busy == "" })
			o := h.w.helmUninstall.installObservation
			if o == nil || o.HistoryOutcome() != outcome || h.w.helmUninstall.installRecovery.Outcome() != outcome ||
				string(o.Storage().State) != "absent" || len(o.Items()) != 1 || string(o.Items()[0].State) != "absent" || len(o.Revisions()) != 0 {
				t.Fatal("current absence rewrote historical server outcomes", h.w.helmUninstall.status)
			}
			nativeHelmReach(h, "Helm install recovery current target 1")
			for _, label := range []string{"Stop Helm operation", "Close uninstall review", "Helm uninstall status"} {
				box, ok := h.tt.Find(label)
				if !ok || box.W <= 0 || box.H <= 0 {
					t.Fatal("small viewport hid a fixed operation control", label)
				}
			}
			if _, ok := h.tt.Find("Review local archive"); ok {
				t.Fatal("installation history acquired an unqualified archive action")
			}
			saveNativeScreenshot(t, h.tt)
			if outcome != helmjournal.NotStarted {
				assertNativeHelmInstallHistoryUnchanged(t, f)
			}
		})
	}
}

func TestNativeHelmInstallRecoveryObservationFailureKeepsHistory(t *testing.T) {
	f := newNativeHelmInstallRecoveryFixture(t, helmjournal.Unknown)
	f.setMode("deny")
	h := f.harness(t, true)
	selectNativeHelmInstallHistory(t, h, f.id)
	nativeHelmClick(h, helmRecoveryConsentLabel)
	nativeHelmClick(h, "Observe current metadata")
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	if h.w.helmUninstall.installObservation != nil || h.w.helmUninstall.installRecovery.Outcome() != helmjournal.Unknown ||
		h.w.helmUninstall.reconcileAck || strings.Contains(h.w.helmUninstall.status, "PRIVATE_INSTALL") {
		t.Fatal("failed observation published partial facts, rewrote history or exposed raw errors")
	}
	before := f.requestCount()
	h.w.observeHelmRecovery()
	if f.requestCount() != before {
		t.Fatal("failed observation retained a usable confirmation")
	}
	assertNativeHelmInstallHistoryUnchanged(t, f)
}

func TestNativeHelmInstallRecoveryObservationLifecycle(t *testing.T) {
	for _, change := range []string{"stop", "close-pane", "close-window", "backend", "session", "namespace", "context", "path", "connection"} {
		t.Run(change, func(t *testing.T) {
			f := newNativeHelmInstallRecoveryFixture(t, helmjournal.Unknown)
			f.setMode("block")
			h := f.harness(t, true)
			connectionCtx, connectionCancel := context.WithCancel(h.w.ctx)
			defer connectionCancel()
			h.w.connectionCtx = connectionCtx
			selectNativeHelmInstallHistory(t, h, f.id)
			nativeHelmClick(h, helmRecoveryConsentLabel)
			nativeHelmClick(h, "Observe current metadata")
			select {
			case <-f.entered:
			case <-h.ctx.Done():
				t.Fatal("metadata observation did not reach the blocked GET", h.w.helmUninstall.status)
			}
			s := h.w.helmUninstall
			switch change {
			case "stop":
				h.w.stopHelmUninstall()
			case "close-pane":
				h.w.clearHelmUninstall()
			case "close-window":
				h.w.Close()
			case "backend":
				h.w.backend = f.backend(t)
				h.w.syncHelmUninstall()
			case "session":
				h.w.sessionID = "replacement-session"
				h.w.syncHelmUninstall()
			case "namespace":
				h.w.namespace = "other"
				h.w.syncHelmUninstall()
			case "context":
				h.w.contextEpoch++
				h.w.syncHelmUninstall()
			case "path":
				h.w.helmJournalPath = filepath.Join(t.TempDir(), "other")
				h.w.syncHelmUninstall()
			case "connection":
				connectionCancel()
			}
			select {
			case <-f.cancelled:
			case <-h.ctx.Done():
				t.Fatal("lifecycle change did not cancel the in-flight metadata GET")
			}
			if change == "stop" || change == "connection" {
				h.pump(func() bool { return h.w.helmUninstall.busy == "" })
			}
			if h.w.helmUninstall.installObservation != nil || h.w.helmUninstall.reconcileAck {
				t.Fatal("cancelled lifecycle retained current facts or acknowledgement")
			}
			before := f.requestCount()
			out, err := s.backend.ObserveHelmInstallRecovery(context.Background(), s.installReconcileConsent, s.installRecovery, s.session, s.namespace)
			if out != nil || !errors.Is(err, kube.ErrHelmReconciliationConsent) || f.requestCount() != before {
				t.Fatal("revoked observation grant was reusable after lifecycle cancellation", err)
			}
			assertNativeHelmInstallHistoryUnchanged(t, f)
		})
	}
}

func TestNativeHelmInstallRecoveryLateObservation(t *testing.T) {
	for _, change := range []string{"new-pane", "session", "same-id-new-snapshot"} {
		t.Run(change, func(t *testing.T) {
			f := newNativeHelmInstallRecoveryFixture(t, helmjournal.Unknown)
			h := f.harness(t, true)
			selectNativeHelmInstallHistory(t, h, f.id)
			nativeHelmClick(h, helmRecoveryConsentLabel)
			nativeHelmClick(h, "Observe current metadata")
			var completion func()
			select {
			case completion = <-h.updates:
			case <-h.ctx.Done():
				t.Fatal("observation did not produce a queued completion")
			}
			switch change {
			case "new-pane":
				h.w.clearHelmUninstall()
				h.w.openHelmRecovery()
			case "session":
				h.w.sessionID = "new-session"
				h.w.syncHelmUninstall()
			case "same-id-new-snapshot":
				entries, err := loadHelmJournalHistory(context.Background(), f.path)
				nativeHelmJournalMust(t, err)
				// Inject a newly read snapshot without changing the ID/epoch to
				// isolate the additional exact-snapshot stale-result guard.
				h.w.helmUninstall.installRecovery = entries[0].installRecovery
			}
			before := h.w.helmUninstall
			calls := f.requestCount()
			completion()
			if h.w.helmUninstall.installObservation != nil || h.w.helmUninstall.reconcileAck ||
				h.w.helmUninstall.status != before.status || h.w.helmUninstall.epoch != before.epoch || f.requestCount() != calls {
				t.Fatal("late observation changed a replacement UI scope or snapshot")
			}
			assertNativeHelmInstallHistoryUnchanged(t, f)
		})
	}
}
