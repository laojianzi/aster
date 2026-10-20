package uiworkbench

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/operation"
)

func TestNativeHelmUninstallExplicitConfirmations(t *testing.T) {
	f := newNativeHelmUninstallFixture(t, 2)
	h := f.harness(t)
	editor := ui.NewTextBuffer("retained\r\n中文\n")
	h.w.editor, h.w.editorObserved = editor, editor.Snapshot()
	plan := &operation.Prepared{}
	h.w.plan = plan
	openNativeHelmReview(h)
	_, _, _, _, before := f.snapshot(t)
	h.w.prepareHelmUninstall()
	h.w.preflightHelmUninstall()
	h.w.executeHelmUninstall()
	_, writes, _, _, after := f.snapshot(t)
	if len(before) != len(after) || writes != 0 {
		t.Fatal("opening or calling an unacknowledged action sent requests")
	}
	preflightNativeHelmReview(h)
	dry, writes, deleted, storage, _ := f.snapshot(t)
	if dry != 3 || writes != 0 || deleted != 0 || storage.Labels["status"] != "deployed" || h.w.helmUninstall.prepareAck || h.w.helmUninstall.dryRunAck || h.w.helmUninstall.executeAck {
		t.Fatal("separate read/dry-run consent did not remain nonpersistent")
	}
	if _, err := os.Stat(h.w.helmJournalPath); !os.IsNotExist(err) {
		t.Fatal("journal was opened before independent execution confirmation", err)
	}
	h.w.executeHelmUninstall()
	_, writes, _, _, _ = f.snapshot(t)
	if writes != 0 {
		t.Fatal("dry-run granted persistent execution")
	}
	confirmNativeHelmExecution(h)
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	result := h.w.helmUninstall.execution
	if result == nil || result.Outcome() != kube.HelmExecutionCompleted || !result.JournalComplete() || result.JournalID() == "" || h.w.pendingWrites != 0 {
		t.Fatal("native execution", h.w.helmUninstall.status)
	}
	dry, writes, deleted, storage, _ = f.snapshot(t)
	// The executor repeats begin/target preflight and separately dry-runs the
	// final retained-history update after all actual deletes have completed.
	if dry != 7 || writes != 4 || deleted != 2 || storage.Labels["status"] != "uninstalled" || len(result.Effects()) != 4 {
		t.Fatal("server retained state disagrees with native result", dry, writes, deleted, storage.Labels)
	}
	h.w.executeHelmUninstall()
	_, repeated, _, _, _ := f.snapshot(t)
	if repeated != writes || h.w.helmUninstall.executeAck || h.w.helmUninstall.confirmation != "" {
		t.Fatal("finished attempt replayed or retained confirmation")
	}
	if strings.Contains(strings.Join(h.tt.Texts(), "\n"), "PRIVATE_NATIVE") {
		t.Fatal("private server content appeared in native text")
	}
	nativeHelmReach(h, "Helm uninstall journal outcome")
	saveNativeScreenshot(t, h.tt)
	entries, err := loadHelmJournalHistory(h.ctx, h.w.helmJournalPath)
	if err != nil || len(entries) != 1 || entries[0].recovery.Outcome() != kube.HelmExecutionCompleted {
		t.Fatal("actual result did not leave readable history", err)
	}
	h.click("Close uninstall review")
	if h.w.editor != editor || editor.Snapshot().String() != "retained\r\n中文\n" || h.w.plan != plan {
		t.Fatal("Helm workflow changed an unrelated editor or prepared operation")
	}
}

func TestNativeHelmUninstallQueuedInputAndComposition(t *testing.T) {
	t.Run("busy-consent", func(t *testing.T) {
		f := newNativeHelmUninstallFixture(t, 1)
		h := f.harness(t)
		openNativeHelmReview(h)
		nativeHelmReach(h, helmPrepareConsentLabel)
		h.w.helmUninstall.busy = "fixture-busy"
		h.click(helmPrepareConsentLabel) // Last painted enabled, current state busy.
		if h.w.helmUninstall.prepareAck {
			t.Fatal("queued busy input committed preparation consent")
		}
		h.w.helmUninstall.busy = ""
		h.tt.Frame()
		h.tt.Frame()
		h.w.prepareHelmUninstall()
		if h.w.helmUninstall.prepareAck || h.w.helmUninstall.busy != "" {
			t.Fatal("unlock replayed rejected consent")
		}
	})
	t.Run("exact-text-and-composition", func(t *testing.T) {
		f := newNativeHelmUninstallFixture(t, 1)
		h := f.harness(t)
		openNativeHelmReview(h)
		preflightNativeHelmReview(h)
		nativeHelmClick(h, helmExecuteConsentLabel)
		nativeHelmClick(h, "Helm uninstall release confirmation")
		h.tt.Type("wrong-release")
		h.tt.Frame()
		nativeHelmClick(h, "Execute restricted uninstall")
		if h.w.helmUninstall.attempted {
			t.Fatal("inexact release confirmation admitted a write")
		}
		nativeHelmClick(h, "Helm uninstall release confirmation")
		h.tt.Key(ui.Cmd, ui.KeyA)
		h.tt.Type("")
		h.tt.Key(0, ui.KeyBackspace)
		h.tt.Compose("example", 7)
		h.tt.Frame()
		if !h.w.helmUninstall.confirmationComposing {
			t.Fatal("test did not establish a native composition")
		}
		h.w.executeHelmUninstall()
		_, writes, _, _, _ := f.snapshot(t)
		if writes != 0 || h.w.helmUninstall.attempted {
			t.Fatal("uncommitted composition authorized execution")
		}
		h.tt.Compose("", 0)
		h.tt.Frame()
		h.w.helmUninstall.busy = "fixture-busy"
		h.tt.Type("example")
		if h.w.helmUninstall.confirmation != "" {
			t.Fatal("queued busy text committed an execution confirmation")
		}
		h.w.helmUninstall.busy = ""
		h.tt.Frame()
		h.tt.Frame()
		if h.w.helmUninstall.confirmation != "" {
			t.Fatal("unlock replayed rejected confirmation text")
		}
	})
}

func TestNativeHelmUninstallStaleCallbacks(t *testing.T) {
	for _, scenario := range []string{"selection", "refresh", "close", "namespace", "context", "backend", "session", "directory", "disconnect", "credentials", "teardown"} {
		t.Run(scenario, func(t *testing.T) {
			f := newNativeHelmUninstallFixture(t, 1)
			h := f.harness(t)
			openNativeHelmReview(h)
			nativeHelmClick(h, helmPrepareConsentLabel)
			nativeHelmClick(h, "Prepare restricted uninstall")
			var completion func()
			select {
			case completion = <-h.updates:
			case <-h.ctx.Done():
				t.Fatal("preparation did not queue its result")
			}
			switch scenario {
			case "selection":
				h.w.selectHelmRevision(h.w.helmReleases.selected)
			case "refresh":
				h.w.refreshHelmReleases()
			case "close":
				h.click("Close uninstall review")
			case "namespace":
				h.w.namespace = "other"
			case "context":
				h.w.currentContext = "other"
			case "backend":
				h.w.backend = f.backend(t)
			case "session":
				h.w.sessionID = "different-current-session"
			case "directory":
				h.w.helmJournalPath = filepath.Join(t.TempDir(), "different")
			case "disconnect":
				h.click("Disconnect")
			case "credentials":
				h.w.expireConnection(h.w.contextEpoch)
			case "teardown":
				h.w.Close()
			}
			completion()
			h.tt.Frame()
			if h.w.helmUninstall.preparation != nil || h.w.helmUninstall.dryRun != nil || h.w.helmUninstall.executeAck || h.w.helmUninstall.prepareAck {
				t.Fatal("stale callback restored prepared authority")
			}
			_, writes, _, _, _ := f.snapshot(t)
			if writes != 0 || h.w.pendingWrites != 0 {
				t.Fatal("stale preparation authorized persistent writes")
			}
		})
	}
}

func TestNativeHelmUninstallCancellationAndJournalRelease(t *testing.T) {
	for _, scenario := range []string{"stop", "close", "disconnect", "teardown"} {
		t.Run(scenario, func(t *testing.T) {
			f := newNativeHelmUninstallFixture(t, 2)
			h := f.harness(t)
			path := h.w.helmJournalPath
			openNativeHelmReview(h)
			preflightNativeHelmReview(h)
			f.setMode("block-begin")
			confirmNativeHelmExecution(h)
			select {
			case <-f.entered:
			case <-h.ctx.Done():
				t.Fatal("persistent begin was not committed")
			}
			if h.w.pendingWrites != 1 {
				t.Fatal("write accounting not held while actual response is pending")
			}
			switch scenario {
			case "stop":
				h.click("Stop Helm operation")
				if !h.w.helmUninstall.stopRequested || h.w.helmUninstall.busy == "" || h.w.pendingWrites != 1 {
					t.Fatal("stop released admission before worker completion")
				}
			case "close":
				h.click("Close uninstall review")
			case "disconnect":
				h.click("Disconnect")
			case "teardown":
				h.w.Close()
			}
			select {
			case <-f.cancelled:
			case <-h.ctx.Done():
				t.Fatal("scope teardown did not cancel in-flight HTTP")
			}
			if scenario != "teardown" {
				h.pump(func() bool { return h.w.pendingWrites == 0 })
			} else if h.w.pendingWrites != 0 {
				t.Fatal("closed workspace did not settle joined writes")
			}
			_, writes, deleted, storage, _ := f.snapshot(t)
			if writes != 1 || deleted != 0 || storage.Labels["status"] != "uninstalling" {
				t.Fatal("cancel rolled back a fact or dispatched later effects", writes, deleted, storage.Labels)
			}
			entries, err := loadHelmJournalHistory(h.ctx, path)
			if err != nil || len(entries) != 1 || entries[0].recovery.Outcome() != kube.HelmExecutionUnknown || !entries[0].recovery.Complete() {
				t.Fatal("worker did not release its lock or retain uncertain history", err)
			}
			if scenario == "stop" && (h.w.helmUninstall.execution == nil || h.w.helmUninstall.execution.Outcome() != kube.HelmExecutionUnknown) {
				t.Fatal("cancel erased the actual Unknown result")
			}
		})
	}
}

func TestNativeHelmUninstallExpiryAndPrivateFailure(t *testing.T) {
	t.Run("expiry", func(t *testing.T) {
		f := newNativeHelmUninstallFixture(t, 1)
		h := f.harness(t)
		openNativeHelmReview(h)
		preflightNativeHelmReview(h)
		grant := h.w.helmUninstall.dryRun
		h.w.helmUninstall.executeAck, h.w.helmUninstall.confirmation = true, "example"
		h.w.expireHelmUninstallFlow(grant.ExpiresAt())
		h.tt.Frame()
		h.w.executeHelmUninstall()
		if h.w.helmUninstall.preparation != nil || h.w.helmUninstall.dryRun != nil || h.w.helmUninstall.executeAck || h.w.helmUninstall.confirmation != "" || grant.Matches(h.w.backend, h.w.sessionID, "team", h.w.helmReleases.selected, time.Now()) {
			t.Fatal("expiry retained authorization or extended original deadline")
		}
		_, writes, _, _, _ := f.snapshot(t)
		if writes != 0 {
			t.Fatal("expired review caused persistence")
		}
	})
	t.Run("private-failure", func(t *testing.T) {
		f := newNativeHelmUninstallFixture(t, 1)
		h := f.harness(t)
		openNativeHelmReview(h)
		f.setMode("deny-content")
		nativeHelmClick(h, helmPrepareConsentLabel)
		nativeHelmClick(h, "Prepare restricted uninstall")
		h.pump(func() bool { return h.w.helmUninstall.busy == "" })
		if h.w.helmUninstall.preparation != nil || h.w.helmUninstall.prepareAck || strings.Contains(strings.Join(h.tt.Texts(), "\n"), "PRIVATE_NATIVE") {
			t.Fatal("failure retained confirmation or exposed remote content")
		}
	})
}

func TestNativeHelmRecoveryOfflineAndFreshObservation(t *testing.T) {
	f := newNativeHelmUninstallFixture(t, 2)
	h := f.harness(t)
	path := h.w.helmJournalPath
	openNativeHelmReview(h)
	preflightNativeHelmReview(h)
	f.setMode("lose-begin")
	confirmNativeHelmExecution(h)
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	result := h.w.helmUninstall.execution
	if result == nil || result.Outcome() != kube.HelmExecutionUnknown || !result.JournalComplete() {
		t.Fatal("committed response loss not uncertain", h.w.helmUninstall.status)
	}
	id := result.JournalID()
	h.w.Close()
	offline := newRelationshipHarness(t, 20*time.Second)
	offline.w.helmJournalPath = path
	offline.tt.Frame()
	_, _, _, _, before := f.snapshot(t)
	offline.click("Helm recovery")
	nativeHelmClick(offline, "Reload local history")
	offline.pump(func() bool { return offline.w.helmUninstall.busy == "" })
	nativeHelmClick(offline, "Select Helm recovery "+id)
	r := offline.w.helmUninstall.recovery
	_, _, _, _, after := f.snapshot(t)
	if r == nil || r.ID() != id || r.Outcome() != kube.HelmExecutionUnknown || len(before) != len(after) || offline.w.backend != nil || offline.w.helmUninstall.observation != nil {
		t.Fatal("offline recovery restored network authority or changed uncertainty")
	}
	nativeHelmReach(offline, "Helm recovery outcome")
	saveNativeScreenshot(t, offline.tt)
	offline.click("Close uninstall review")
	offline.w.backend, offline.w.namespace, offline.w.sessionID = f.backend(t), "team", "fresh-native-recovery-session"
	offline.w.activeContext, offline.w.currentContext = "test-context", "test-context"
	offline.tt.Frame()
	offline.click("Helm releases")
	offline.click("Refresh Helm metadata")
	offline.pump(func() bool { return !offline.w.helmReleases.busy })
	offline.click("Select sh.helm.release.v1.example.v2")
	offline.click("Review recovery history")
	nativeHelmClick(offline, "Reload local history")
	offline.pump(func() bool { return offline.w.helmUninstall.busy == "" })
	nativeHelmClick(offline, "Select Helm recovery "+id)
	_, writesBefore, _, _, callsBefore := f.snapshot(t)
	offline.w.observeHelmRecovery()
	_, _, _, _, withoutConsent := f.snapshot(t)
	if len(withoutConsent) != len(callsBefore) {
		t.Fatal("history selection silently authorized a current read")
	}
	nativeHelmClick(offline, helmRecoveryConsentLabel)
	nativeHelmClick(offline, "Observe current metadata")
	offline.pump(func() bool { return offline.w.helmUninstall.busy == "" })
	o := offline.w.helmUninstall.observation
	_, writesAfter, _, _, callsAfter := f.snapshot(t)
	if o == nil || o.HistoryOutcome() != kube.HelmExecutionUnknown || offline.w.helmUninstall.recovery.Outcome() != kube.HelmExecutionUnknown || writesBefore != writesAfter || len(o.Items()) != 2 {
		t.Fatal("fresh observation altered historical authority or facts", offline.w.helmUninstall.status)
	}
	for _, call := range callsAfter[len(callsBefore):] {
		if !strings.HasPrefix(call, "GET ") {
			t.Fatal("recovery made a non-GET request", call)
		}
	}
	for _, item := range o.Items() {
		if item.State != kube.HelmRecoveryTargetOriginal {
			t.Fatal("untouched targets were not observed as original")
		}
	}
	nativeHelmReach(offline, "Helm recovery current target 2")
	saveNativeScreenshot(t, offline.tt)
}

func TestNativeHelmRecoveryCorruptionAndBusyStore(t *testing.T) {
	for _, scenario := range []string{"corrupt", "busy"} {
		t.Run(scenario, func(t *testing.T) {
			h := newRelationshipHarness(t, 15*time.Second)
			path := filepath.Join(t.TempDir(), "journal")
			h.w.helmJournalPath = path
			store, err := helmjournal.Open(path)
			nativeHelmJournalMust(t, err)
			t.Cleanup(func() { nativeHelmJournalMust(t, store.Close()) })
			id := ""
			var original []byte
			if scenario == "corrupt" {
				writer, err := store.Begin(nativeHelmJournalPlan("uid2"))
				nativeHelmJournalMust(t, err)
				id = writer.ID()
				nativeHelmJournalMust(t, writer.Intent(0, "7"))
				nativeHelmJournalMust(t, writer.Abort())
				nativeHelmJournalMust(t, store.Close())
				file, err := os.OpenFile(filepath.Join(path, id+".wal"), os.O_WRONLY|os.O_APPEND, 0600)
				nativeHelmJournalMust(t, err)
				_, err = file.Write([]byte{0, 1})
				nativeHelmJournalMust(t, err)
				nativeHelmJournalMust(t, file.Close())
				original, err = os.ReadFile(filepath.Join(path, id+".wal"))
				nativeHelmJournalMust(t, err)
			}
			h.tt.Frame()
			h.click("Helm recovery")
			nativeHelmClick(h, "Reload local history")
			h.pump(func() bool { return h.w.helmUninstall.busy == "" })
			if scenario == "busy" {
				if len(h.w.helmUninstall.history) != 0 || !strings.Contains(h.w.helmUninstall.status, "journal is in use") {
					t.Fatal("held private store was not rejected", h.w.helmUninstall.status)
				}
				return
			}
			nativeHelmClick(h, "Select Helm recovery "+id)
			r := h.w.helmUninstall.recovery
			if r == nil || !r.Corrupt() || r.Complete() || r.Outcome() != kube.HelmExecutionUnknown || h.w.helmUninstall.recoveryErr == nil {
				t.Fatal("damaged history was concealed or upgraded")
			}
			h.w.helmUninstall.reconcileAck = true
			h.w.observeHelmRecovery()
			if h.w.helmUninstall.observation != nil || h.w.helmUninstall.busy != "" {
				t.Fatal("damaged history admitted current observation")
			}
			nativeHelmReach(h, "Helm recovery damaged")
			saveNativeScreenshot(t, h.tt)
			current, err := os.ReadFile(filepath.Join(path, id+".wal"))
			nativeHelmJournalMust(t, err)
			if !bytes.Equal(current, original) {
				t.Fatal("UI loading rewrote damaged evidence")
			}
		})
	}
}

func TestNativeHelmUninstallMinimumWindow(t *testing.T) {
	for _, scale := range []float32{1, 1.25, 1.5} {
		for _, rtl := range []bool{false, true} {
			t.Run(fmt.Sprintf("scale-%g-rtl-%t", scale, rtl), func(t *testing.T) {
				f := newNativeHelmUninstallFixture(t, helmrelease.MaxPreviewObjects)
				h := f.harness(t)
				h.tt.SetPreferences(ui.Preferences{TextScale: scale, HighContrast: true, ReduceMotion: true})
				if rtl {
					h.tt.SetLocale("ar")
					h.tt.SetDirection(ui.RightToLeft)
				}
				openNativeHelmReview(h)
				prepareNativeHelmReview(h)
				nativeHelmReach(h, "Helm uninstall reviewed identity 32")
				viewport, _ := h.tt.Find("Helm uninstall inventory")
				status, _ := h.tt.Find("Connection status")
				if viewport.H < 96 || viewport.Y+viewport.H > status.Y {
					t.Fatal("review viewport collapsed", viewport, status)
				}
				for _, label := range []string{"Helm uninstall status", "Stop Helm operation", "Close uninstall review"} {
					bounds, ok := h.tt.Find(label)
					if !ok || bounds.X < 0 || bounds.Y < 0 || bounds.W <= 0 || bounds.H < 12 || bounds.X+bounds.W > 1100 || bounds.Y+bounds.H > status.Y {
						t.Fatal("fixed action/status clipped", label, bounds, status)
					}
				}
				nativeHelmReach(h, helmDryRunConsentLabel)
				saveNativeScreenshot(t, h.tt)
				nativeHelmReach(h, "Helm uninstall review expiry")
				if h.w.pendingWrites != 0 {
					t.Fatal("scrolling admitted writes")
				}
			})
		}
	}
}
