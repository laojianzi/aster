package uiworkbench

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/kube"
)

func TestNativeHelmUninstallParentContextRevokesDryRun(t *testing.T) {
	for _, parent := range []string{"connection", "application"} {
		t.Run(parent, func(t *testing.T) {
			f := newNativeHelmUninstallFixture(t, 1)
			h := f.harness(t)
			cancel := h.w.cancel
			if parent == "connection" {
				h.w.connectionCtx, cancel = context.WithCancel(h.w.ctx)
				defer cancel()
			}
			openNativeHelmReview(h)
			preflightNativeHelmReview(h)
			s := h.w.helmUninstall
			report := s.dryRun
			if !report.Matches(s.backend, s.session, s.namespace, s.row, time.Now()) {
				t.Fatal("successful dry-run did not survive its worker cleanup")
			}
			cancel()
			// Do not render a frame, run a queued callback, or invoke UI teardown.
			// A parent context ending must revoke the retained grant by itself.
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			for report.Matches(s.backend, s.session, s.namespace, s.row, time.Now()) {
				select {
				case <-deadline.C:
					t.Fatal("parent cancellation left a reusable dry-run report")
				case <-time.After(time.Millisecond):
				}
			}
			journal, err := kube.OpenHelmUninstallJournal(s.journalPath)
			if err != nil {
				t.Fatal(err)
			}
			defer journal.Close()
			grant, err := s.backend.AcknowledgeHelmUninstallExecution(report, s.session, s.namespace, s.row, true, journal)
			if grant != nil || !errors.Is(err, kube.ErrHelmExecutionConsent) {
				t.Fatal("closed parent minted execution with a fresh unrelated context", err)
			}
			ids, err := journal.IDs()
			_, writes, _, _, _ := f.snapshot(t)
			if err != nil || len(ids) != 0 || writes != 0 {
				t.Fatal("revoked report produced a journal operation or persistent request", err)
			}
		})
	}
}

func stageNativeHelmRecovery(t *testing.T, h *relationshipHarness) string {
	t.Helper()
	openNativeHelmReview(h)
	preflightNativeHelmReview(h)
	confirmNativeHelmExecution(h)
	h.pump(func() bool { return h.w.pendingWrites == 0 })
	result := h.w.helmUninstall.execution
	if result == nil || result.Outcome() != kube.HelmExecutionCompleted {
		t.Fatal("history fixture execution", h.w.helmUninstall.status)
	}
	id := result.JournalID()
	h.click("Close uninstall review")
	h.click("Refresh Helm metadata")
	h.pump(func() bool { return !h.w.helmReleases.busy })
	h.click("Select sh.helm.release.v1.example.v2")
	h.click("Review recovery history")
	nativeHelmClick(h, "Reload local history")
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	nativeHelmClick(h, "Select Helm recovery "+id)
	return id
}

func TestNativeHelmUninstallLatePhaseCallbacks(t *testing.T) {
	for _, phase := range []string{"dry-run", "recovery"} {
		for _, change := range []string{"new-pane", "session"} {
			t.Run(phase+"/"+change, func(t *testing.T) {
				f := newNativeHelmUninstallFixture(t, 1)
				h := f.harness(t)
				if phase == "dry-run" {
					openNativeHelmReview(h)
					prepareNativeHelmReview(h)
					nativeHelmClick(h, helmDryRunConsentLabel)
					nativeHelmClick(h, "Run uninstall dry-run")
				} else {
					stageNativeHelmRecovery(t, h)
					nativeHelmClick(h, helmRecoveryConsentLabel)
					nativeHelmClick(h, "Observe current metadata")
				}
				var completion func()
				select {
				case completion = <-h.updates:
				case <-h.ctx.Done():
					t.Fatal("phase did not produce its queued completion", phase)
				}
				if change == "new-pane" {
					h.click("Close uninstall review")
					h.click("Review recovery history")
				} else {
					h.w.sessionID = "replacement-session"
					h.tt.Frame()
				}
				before := h.w.helmUninstall
				_, writesBefore, _, _, _ := f.snapshot(t)
				completion()
				h.tt.Frame()
				after := h.w.helmUninstall
				_, writesAfter, _, _, _ := f.snapshot(t)
				if after.epoch != before.epoch || after.mode != before.mode || after.status != before.status ||
					after.preparation != nil || after.dryRun != nil || after.observation != nil || after.executeAck || after.reconcileAck ||
					h.w.pendingWrites != 0 || writesAfter != writesBefore {
					t.Fatal("late phase result changed a new view or restored authority", phase, change)
				}
			})
		}
	}
}

func TestNativeHelmUninstallWorkspaceJournalIsolation(t *testing.T) {
	f := newNativeHelmUninstallFixture(t, 2)
	a := f.harness(t)
	path := a.w.helmJournalPath
	openNativeHelmReview(a)
	preflightNativeHelmReview(a)
	f.setMode("block-begin")
	confirmNativeHelmExecution(a)
	select {
	case <-f.entered:
	case <-a.ctx.Done():
		t.Fatal("first workspace did not reach its committed effect")
	}
	b := newRelationshipHarness(t, 15*time.Second)
	b.w.helmJournalPath = path // The same fixed per-user store, not another copy.
	b.tt.Frame()
	b.click("Helm recovery")
	nativeHelmClick(b, "Reload local history")
	b.pump(func() bool { return b.w.helmUninstall.busy == "" })
	if len(b.w.helmUninstall.history) != 0 || b.w.backend != nil || b.w.pendingWrites != 0 {
		t.Fatal("another workspace consumed an active journal or connection")
	}
	b.w.Close()
	select {
	case <-f.cancelled:
		t.Fatal("closing the second workspace canceled the first")
	default:
	}
	if a.w.pendingWrites != 1 || a.w.helmUninstall.busy != "Executing" {
		t.Fatal("another workspace settled the first workspace's admission")
	}
	if journal, err := kube.OpenHelmUninstallJournal(path); journal != nil || !errors.Is(err, helmjournal.ErrBusy) {
		if journal != nil {
			_ = journal.Close()
		}
		t.Fatal("closing the second workspace released another worker's lock", err)
	}
	a.click("Stop Helm operation")
	a.pump(func() bool { return a.w.pendingWrites == 0 })
	entries, err := loadHelmJournalHistory(a.ctx, path)
	_, writes, deleted, _, _ := f.snapshot(t)
	if err != nil || len(entries) != 1 || entries[0].recovery.Outcome() != kube.HelmExecutionUnknown ||
		writes != 1 || deleted != 0 || a.w.helmUninstall.execution.Outcome() != kube.HelmExecutionUnknown {
		t.Fatal("owning workspace did not release its lock and retain its uncertain effect", err)
	}
}
