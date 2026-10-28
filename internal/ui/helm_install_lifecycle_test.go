package uiworkbench

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/kube"
)

func TestNativeHelmInstallScopeRevocation(t *testing.T) {
	t.Parallel()
	in := nativeInstallCapture(t, "team", 1)
	for _, change := range []string{"backend", "session", "namespace", "context", "journal-path", "input", "stop", "close-pane", "disconnect", "connection-cancel", "expiry"} {
		t.Run(change, func(t *testing.T) {
			f := newNativeInstallFixture(t)
			h := newNativeInstallHarness(t, f, false)
			connection, connectionCancel := context.WithCancel(h.ctx)
			defer connectionCancel()
			h.w.connectionCtx = connection
			nativeInstallUseCapture(h, in)
			nativeInstallPreflight(h)
			flow := h.w.helmInstall.flowCtx
			before, _, _ := f.counts()
			switch change {
			case "backend":
				h.w.backend = f.backend(t, false) // Same endpoint and credentials, different concrete Backend.
			case "session":
				h.w.sessionID = "replacement-session"
			case "namespace":
				h.w.namespace = "other"
			case "context":
				h.w.contextEpoch++
			case "journal-path":
				h.w.helmJournalPath = filepath.Join(t.TempDir(), "other-journal")
			case "input":
				h.w.helmInstall.values += ".different"
				h.w.helmInstallInputsChanged()
			case "stop":
				h.w.stopHelmInstall()
			case "close-pane":
				h.w.clearHelmInstall()
			case "disconnect":
				h.w.disconnect()
			case "connection-cancel":
				connectionCancel()
			case "expiry":
				h.w.expireHelmInstallFlow(h.w.helmInstall.preparation.ExpiresAt())
			}
			h.w.syncHelmInstall()
			s := &h.w.helmInstall
			if flow.Err() == nil || s.flowCtx != nil || s.preparation != nil || s.dryRun != nil || s.executeAck || s.confirmation != "" {
				t.Fatal("scope/lifetime change retained an execution origin", change)
			}
			// A stale event may carry the booleans and exact text. They cannot
			// reconstruct the old opaque preparation, origin or current scope.
			s.executeAck, s.confirmation = true, "example"
			h.w.executeHelmInstall()
			if calls, _, writes := f.counts(); calls != before || writes != 0 || h.w.pendingWrites != 0 || s.attempted {
				t.Fatal("a revoked flow dispatched persistent work", change)
			}
			if _, err := os.Stat(f.path); !os.IsNotExist(err) {
				t.Fatal("revoked flow opened private recovery storage", change, err)
			}
		})
	}
}

func TestNativeHelmInstallLateCallbacks(t *testing.T) {
	t.Parallel()
	in := nativeInstallCapture(t, "team", 1)
	for _, phase := range []string{"capture", "prepare", "dry-run"} {
		t.Run(phase, func(t *testing.T) {
			f := newNativeInstallFixture(t)
			h := newNativeInstallHarness(t, f, false)
			nativeInstallUseCapture(h, in)
			var late func()
			switch phase {
			case "capture":
				// The cached frozen candidate may outlive its original files.
				// This deliberate recapture instead selects fresh private files.
				fresh := nativeInstallFiles(t, "team", 1, false)
				h.w.clearHelmInstall()
				h.w.openHelmInstall()
				s := &h.w.helmInstall
				s.archive, s.chart, s.values, s.release = fresh.request.HelmArchive, fresh.request.ChartArchive, fresh.request.ValuesFile, fresh.request.Release
				s.trusted, s.capabilityAck = true, true
				nativeInstallCaptureStep(h, func() {
					h.w.captureHelmInstall() // The real approved capture worker runs again.
					late = nativeInstallNextCallback(h)
				})
			case "prepare":
				h.w.helmInstall.prepareAck = true
				h.w.prepareHelmInstall()
			case "dry-run":
				nativeInstallPrepare(h)
				h.w.helmInstall.dryRunAck = true
				h.w.preflightHelmInstall()
			}
			if late == nil {
				late = nativeInstallNextCallback(h)
			}
			flow := h.w.helmInstall.flowCtx
			oldEpoch := h.w.helmInstall.epoch
			h.w.clearHelmInstall()
			h.w.openHelmInstall()
			currentEpoch := h.w.helmInstall.epoch
			late()
			s := &h.w.helmInstall
			if currentEpoch <= oldEpoch || s.epoch != currentEpoch || !s.open || s.busy != "" || s.candidate != nil || s.preparation != nil || s.dryRun != nil || s.flowCtx != nil || flow.Err() == nil {
				t.Fatal("late worker callback published into a new review or revived its origin", phase)
			}
			if _, _, writes := f.counts(); writes != 0 || h.w.pendingWrites != 0 {
				t.Fatal("late pre-execution callback dispatched a write", phase)
			}
			if _, err := os.Stat(f.path); !os.IsNotExist(err) {
				t.Fatal("late callback opened a journal", err)
			}
		})
	}
}

func TestNativeHelmInstallUncertainResultsAndFreshRecovery(t *testing.T) {
	t.Parallel()
	nativeInstallSerialRenderer(t)
	in := nativeInstallCapture(t, "team", 1)
	for _, scenario := range []struct {
		name, mode, outcome string
		at                  int
		storage, target     kube.HelmInstallRecoveryTargetState
	}{
		{"pending-secret", "loss", helmjournal.Unknown, 1, kube.HelmInstallRecoveryUnattributed, kube.HelmInstallRecoveryAbsent},
		{"create-target", "loss", helmjournal.Unknown, 2, kube.HelmInstallRecoveryOriginal, kube.HelmInstallRecoveryUnattributed},
		{"final-patch", "block", helmjournal.Unknown, 3, kube.HelmInstallRecoveryModified, kube.HelmInstallRecoveryOriginal},
		{"rejected-target", "reject", "Partial", 2, kube.HelmInstallRecoveryOriginal, kube.HelmInstallRecoveryAbsent},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newNativeInstallFixture(t)
			h := newNativeInstallHarness(t, f, scenario.name == "pending-secret")
			nativeInstallUseCapture(h, in)
			nativeInstallPreflight(h)
			f.setFault(scenario.mode, scenario.at)
			var result *kube.HelmInstallExecution
			if scenario.mode == "block" {
				s := &h.w.helmInstall
				s.executeAck, s.confirmation = true, "example"
				h.w.executeHelmInstall()
				select {
				case <-f.entered:
				case <-h.ctx.Done():
					t.Fatal("final patch did not reach the real committed, unacknowledged point")
				}
				epoch := s.epoch
				// A stale input callback after dispatch must stop authority but
				// must not make the actual worker's historical result disappear.
				s.release = "late-input"
				h.w.helmInstallInputsChanged()
				if s.epoch != epoch || s.release != "example" || !s.attempted {
					t.Fatal("post-dispatch input invalidation detached the historical result callback")
				}
				nativeInstallDrain(h, func() bool { return s.busy == "" && h.w.pendingWrites == 0 })
				result = s.execution
			} else {
				result = nativeInstallExecute(h)
			}
			if result == nil || result.Outcome() != scenario.outcome || !result.JournalComplete() || result.JournalID() == "" {
				t.Fatal("uncertain or rejected server effects were erased by UI completion", h.w.helmInstall.status)
			}
			if _, _, writes := f.counts(); writes != scenario.at {
				t.Fatal("uncertainty caused retry, replay, rollback or later writes", writes)
			}
			snapshot := nativeInstallLoad(t, f.path, result.JournalID())
			if !snapshot.Closed || snapshot.Corrupt || snapshot.Install == nil || snapshot.Install.CandidateID != in.candidate.ID() {
				t.Fatal("finished uncertain operation did not retain its original v2 plan")
			}
			beforeDisk := nativeInstallDiskSnapshot(t, f.path)
			beforeCalls, _, _ := f.counts()
			if h.tt != nil {
				nativeInstallClick(h, "Review install recovery")
			} else {
				h.w.openHelmInstallHistory()
			}
			nativeInstallDrain(h, func() bool { return h.w.helmUninstall.busy == "" })
			u := &h.w.helmUninstall
			if u.installRecovery == nil || u.selectedID != result.JournalID() || u.installRecovery.Outcome() != scenario.outcome || u.installObservation != nil || u.reconcileAck {
				t.Fatal("recovery entry did not select the actual record without current-read permission", u.status)
			}
			h.w.observeHelmRecovery()
			if calls, _, _ := f.counts(); calls != beforeCalls {
				t.Fatal("opening retained history reused execution authority for current GETs")
			}
			if h.tt != nil {
				nativeHelmClick(h, helmRecoveryConsentLabel)
				nativeHelmClick(h, "Observe current metadata")
			} else {
				u.reconcileAck = true
				h.w.observeHelmRecovery()
			}
			nativeInstallDrain(h, func() bool { return u.busy == "" })
			observation := u.installObservation
			if observation == nil || observation.HistoryOutcome() != scenario.outcome || u.installRecovery.Outcome() != scenario.outcome ||
				observation.Storage().State != scenario.storage || len(observation.Items()) != 1 || observation.Items()[0].State != scenario.target ||
				u.reconcileAck || u.installReconcileConsent != nil {
				t.Fatal("fresh metadata observation reassigned an unknown UID or cleared historical uncertainty", u.status)
			}
			f.mu.Lock()
			for _, request := range f.calls[beforeCalls:] {
				if request.method != "GET" || !stringsContainsMetadata(request.accept) {
					f.bad = append(f.bad, "recovery escalated beyond explicit metadata-only GETs")
				}
			}
			f.mu.Unlock()
			nativeInstallAssertDiskUnchanged(t, f.path, beforeDisk)
			s, err := helmjournal.OpenExisting(f.path)
			nativeHelmJournalMust(t, err)
			admission, err := s.ReviewAdmission(context.Background(), helmjournal.AdmissionScope{Binding: f.binding(t), Namespace: "team", Release: "example"})
			nativeHelmJournalMust(t, s.Close())
			if !errors.Is(err, helmjournal.ErrUnresolved) || admission.Reason != helmjournal.AdmissionUnresolved || admission.RecordID != result.JournalID() {
				t.Fatal("fresh read-only recovery cleared the cross-operation unresolved guard", err)
			}
			beforeCalls, _, _ = f.counts()
			h.w.observeHelmRecovery()
			if calls, _, writes := f.counts(); calls != beforeCalls || writes != scenario.at {
				t.Fatal("observation or execution permission was silently reused")
			}
			if h.tt != nil {
				nativeHelmReach(h, "Helm install recovery current storage")
				saveNativeScreenshot(t, h.tt)
			}
		})
	}
}

func stringsContainsMetadata(accept string) bool {
	return accept == "application/json;as=PartialObjectMetadata;g=meta.k8s.io;v=v1" ||
		accept == "application/json;as=PartialObjectMetadataList;g=meta.k8s.io;v=v1"
}

func TestNativeHelmInstallClosedWindowPreservesWAL(t *testing.T) {
	t.Parallel()
	in := nativeInstallCapture(t, "team", 1)
	f := newNativeInstallFixture(t)
	h := newNativeInstallHarness(t, f, false)
	nativeInstallUseCapture(h, in)
	nativeInstallPreflight(h)
	f.setFault("block", 2)
	h.w.helmInstall.executeAck, h.w.helmInstall.confirmation = true, "example"
	h.w.executeHelmInstall()
	select {
	case <-f.entered:
	case <-h.ctx.Done():
		t.Fatal("actual target create did not commit before the window closed")
	}
	h.w.Close() // Joins the real worker without processing its completion callback.
	select {
	case <-f.cancelled:
	case <-time.After(time.Second):
		t.Fatal("window shutdown did not cancel the already dispatched request")
	}
	if h.w.pendingWrites != 0 {
		t.Fatal("closed window retained an in-flight persistent-worker count")
	}
	s, err := helmjournal.OpenExisting(f.path)
	nativeHelmJournalMust(t, err) // Also proves that the worker released the OS lock.
	defer s.Close()
	ids, err := s.List()
	nativeHelmJournalMust(t, err)
	if len(ids) != 1 {
		t.Fatal("closed window lost or duplicated the actual durable attempt")
	}
	snap, err := s.Load(ids[0])
	nativeHelmJournalMust(t, err)
	if !snap.Closed || snap.Corrupt || len(snap.Effects) != 3 || snap.Effects[0].Outcome != helmjournal.Completed || snap.Effects[1].Outcome != helmjournal.Unknown || snap.Effects[1].UID != "" || snap.Effects[2].Outcome != helmjournal.NotStarted {
		t.Fatal("closed-window callback loss erased the known prefix or assigned an unacknowledged target UID")
	}
	if _, _, writes := f.counts(); writes != 2 || len(f.object("configmaps/config-00")) == 0 {
		t.Fatal("closed-window cancellation retried or rolled back a committed target")
	}
	admission, err := s.ReviewAdmission(context.Background(), helmjournal.AdmissionScope{Binding: f.binding(t), Namespace: "team", Release: "example"})
	if !errors.Is(err, helmjournal.ErrUnresolved) || admission.Reason != helmjournal.AdmissionUnresolved || admission.RecordID != ids[0] {
		t.Fatal("window close silently cleared retained uncertainty")
	}
}
