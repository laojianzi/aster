package uiworkbench

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrender"
)

func TestNativeHelmInstallIndependentConfirmations(t *testing.T) {
	t.Parallel()
	nativeInstallSerialRenderer(t)
	in := nativeInstallFiles(t, "team", 1, false)
	f := newNativeInstallFixture(t)
	h := newNativeInstallHarness(t, f, true)
	nativeInstallOpenAndFill(h, in)
	nativeInstallCaptureStep(h, func() {
		nativeInstallClick(h, "Capture install candidate")
		if h.w.helmInstall.busy != "" || h.w.helmInstall.candidate != nil {
			t.Fatal("capture did not require explicit trust and capability acknowledgement")
		}
	})
	nativeInstallClick(h, helmInstallTrustLabel)
	nativeInstallCaptureStep(h, func() {
		nativeInstallClick(h, "Capture install candidate")
		if h.w.helmInstall.busy != "" || h.w.helmInstall.candidate != nil {
			t.Fatal("trust alone silently accepted the fixed capability assumption")
		}
	})
	nativeInstallClick(h, helmInstallCapabilityLabel)
	s := &h.w.helmInstall
	nativeInstallCaptureStep(h, func() {
		nativeInstallClick(h, "Capture install candidate")
		nativeInstallDrain(h, func() bool { return s.busy == "" })
		if s.candidate == nil || s.prepareAck || s.dryRunAck || s.executeAck || !s.inputsMatch() {
			t.Fatal("capture failed or inherited later confirmations", s.status)
		}
		nativeInstallRememberCapture(nativeInstallInput{request: s.capturedRequest, candidate: s.candidate})
	})
	if calls, dry, writes := f.counts(); calls != 0 || dry != 0 || writes != 0 {
		t.Fatal("opening or local capture contacted the cluster")
	}
	if _, err := os.Stat(f.path); !os.IsNotExist(err) {
		t.Fatal("opening or capture opened the private journal", err)
	}
	nativeInstallClick(h, "Prepare Helm install")
	if calls, _, _ := f.counts(); calls != 0 {
		t.Fatal("preparation used capture acknowledgement as read permission")
	}
	nativeInstallClick(h, helmInstallPrepareConsentLabel)
	nativeInstallClick(h, "Prepare Helm install")
	nativeInstallDrain(h, func() bool { return s.busy == "" })
	if s.preparation == nil || s.prepareAck || s.dryRunAck || s.executeAck {
		t.Fatal("preparation failed or retained another stage's permission", s.status)
	}
	before, dry, writes := f.counts()
	if before == 0 || dry != 0 || writes != 0 {
		t.Fatal("preparation did more than its separately acknowledged GETs")
	}
	nativeInstallClick(h, "Run install dry-run")
	if calls, _, _ := f.counts(); calls != before {
		t.Fatal("sensitive dry-run inherited read permission")
	}
	nativeInstallClick(h, helmInstallDryRunConsentLabel)
	nativeInstallClick(h, "Run install dry-run")
	nativeInstallDrain(h, func() bool { return s.busy == "" })
	if s.dryRun == nil || !s.dryRun.Complete() || s.dryRunAck || s.executeAck || s.confirmation != "" {
		t.Fatal("dry-run failed or implicitly authorized execution", s.status)
	}
	if _, dry, writes = f.counts(); dry != 2 || writes != 0 {
		t.Fatal("dry-run did not separately verify the frozen Secret and ConfigMap", dry, writes)
	}
	if _, err := os.Stat(f.path); !os.IsNotExist(err) {
		t.Fatal("read and dry-run stages created persistent recovery state", err)
	}
	nativeInstallClick(h, "Execute Helm install")
	if s.attempted || h.w.pendingWrites != 0 {
		t.Fatal("execution did not require an independent write acknowledgement")
	}
	nativeInstallClick(h, helmInstallExecuteConsentLabel)
	nativeInstallClick(h, "Execute Helm install")
	if s.attempted {
		t.Fatal("execution did not require exact typed release confirmation")
	}
	nativeInstallEdit(h, "Helm install release confirmation", "example")
	nativeInstallClick(h, "Execute Helm install")
	nativeInstallDrain(h, func() bool { return s.busy == "" && h.w.pendingWrites == 0 })
	result := s.execution
	if result == nil || result.Outcome() != helmjournal.Completed || !result.JournalComplete() || result.JournalID() == "" ||
		!s.attempted || s.executeAck || s.confirmation != "" || s.preparation != nil || s.dryRun != nil {
		t.Fatal("native execution did not preserve its separate server and journal outcomes", s.status)
	}
	if _, _, writes = f.counts(); writes != 3 || len(result.Effects()) != 3 {
		t.Fatal("first-revision install did not use its exact three persistent stages", writes)
	}
	snap := nativeInstallLoad(t, f.path, result.JournalID())
	if snap.Version != 2 || snap.Action != "install" || !snap.Closed || snap.Corrupt || len(snap.Effects) != 3 {
		t.Fatal("completed native execution was not saved as actual v2 history")
	}
	for _, effect := range snap.Effects {
		if effect.Outcome != helmjournal.Completed || effect.UID == "" || effect.ObservedResourceVersion == "" {
			t.Fatal("journal erased a positively observed accepted identity")
		}
	}
	nativeInstallReach(h, "Helm install outcome")
	outcome, shown := h.tt.Find("Helm install outcome")
	if !shown || outcome.W <= 0 || outcome.H <= 0 || strings.Contains(strings.Join(h.tt.Texts(), "\n"), "PRIVATE_NATIVE_INSTALL_FROZEN") {
		t.Fatal("native outcome element is missing or exposed captured sensitive values")
	}
	nativeInstallReach(h, "Helm install journal outcome")
	for _, raw := range nativeInstallDiskSnapshot(t, f.path) {
		if bytes.Contains(raw, []byte("PRIVATE_NATIVE_INSTALL_FROZEN")) {
			t.Fatal("private journal persisted captured request content")
		}
	}
	saveNativeScreenshot(t, h.tt)
	before, _, _ = f.counts()
	h.w.executeHelmInstall()
	if calls, _, _ := f.counts(); calls != before || h.w.pendingWrites != 0 {
		t.Fatal("finished native execution replayed a consumed attempt")
	}
}

func TestNativeHelmInstallFrozenInputs(t *testing.T) {
	t.Parallel()
	nativeInstallSerialRenderer(t)
	// Capture once through the actual native action. The two children restore
	// these same private inputs before independently modifying/removing them;
	// each still creates a fresh Backend, preparation, dry-run and journal.
	in := nativeInstallFiles(t, "team", 1, true)
	captureFixture := newNativeInstallFixture(t)
	captureHarness := newNativeInstallHarness(t, captureFixture, true)
	captureHarness.w.openHelmInstall()
	captureState := &captureHarness.w.helmInstall
	captureState.archive, captureState.chart, captureState.values, captureState.release = in.request.HelmArchive, in.request.ChartArchive, in.request.ValuesFile, in.request.Release
	captureState.trusted, captureState.capabilityAck = true, true
	captureHarness.tt.Frame()
	nativeInstallCaptureStep(captureHarness, func() {
		nativeInstallClick(captureHarness, "Capture install candidate")
		nativeInstallDrain(captureHarness, func() bool { return captureState.busy == "" })
		if captureState.candidate == nil {
			t.Fatal("explicit real capture failed", captureState.status)
		}
		in.candidate = captureState.candidate
		nativeInstallRememberCapture(in)
	})
	captureHarness.w.clearHelmInstall()
	originals := map[string][]byte{}
	for _, file := range []string{in.request.HelmArchive, in.request.ChartArchive, in.request.ValuesFile} {
		var err error
		originals[file], err = os.ReadFile(file)
		nativeHelmJournalMust(t, err)
	}
	for _, change := range []string{"modify-after-capture", "remove-after-capture"} {
		t.Run(change, func(t *testing.T) {
			// The approved evidence archive is copied before either destructive
			// operation; shared source evidence is always opened read-only.
			for file, raw := range originals {
				nativeHelmJournalMust(t, os.WriteFile(file, raw, 0600))
			}
			f := newNativeInstallFixture(t)
			h := newNativeInstallHarness(t, f, false)
			nativeInstallUseCapture(h, in)
			s := &h.w.helmInstall
			captured := s.candidate
			for _, file := range []string{in.request.HelmArchive, in.request.ChartArchive, in.request.ValuesFile} {
				if change == "modify-after-capture" {
					nativeHelmJournalMust(t, os.WriteFile(file, []byte("PRIVATE_CHANGED_DISK_INPUT\n"), 0600))
				} else {
					nativeHelmJournalMust(t, os.Remove(file))
				}
			}
			nativeInstallPreflight(h)
			result := nativeInstallExecute(h)
			if result == nil || result.Outcome() != helmjournal.Completed || s.candidate != captured {
				t.Fatal("later stages reread or replaced immutable captured inputs", s.status)
			}
			var config struct {
				Data map[string]string `json:"data"`
			}
			nativeHelmJournalMust(t, json.Unmarshal(f.object("configmaps/config-00"), &config))
			if config.Data["message"] != "PRIVATE_NATIVE_INSTALL_FROZEN" {
				t.Fatal("persistent ConfigMap did not contain the once-captured values")
			}
			before, _, _ := f.counts()
			h.w.clearHelmInstall()
			h.w.openHelmInstall()
			s = &h.w.helmInstall
			s.archive, s.chart, s.values, s.release = in.request.HelmArchive, in.request.ChartArchive, in.request.ValuesFile, in.request.Release
			s.trusted, s.capabilityAck = true, true
			nativeInstallCaptureStep(h, func() {
				h.w.captureHelmInstall()
				nativeInstallDrain(h, func() bool { return s.busy == "" })
			})
			if s.candidate != nil || s.preparation != nil || s.dryRun != nil || s.status != helmrender.ErrInput.Error() {
				t.Fatal("explicit recapture reused old bytes or failed for a reason other than the replaced input", s.status)
			}
			if calls, _, _ := f.counts(); calls != before {
				t.Fatal("invalid explicit recapture made a cluster request")
			}
		})
	}
}

func TestNativeHelmInstallQueuedInputAndComposition(t *testing.T) {
	t.Parallel()
	nativeInstallSerialRenderer(t)
	in := nativeInstallCapture(t, "team", 1)
	t.Run("busy-consent", func(t *testing.T) {
		f := newNativeInstallFixture(t)
		h := newNativeInstallHarness(t, f, true)
		nativeInstallUseCapture(h, in)
		nativeInstallReach(h, helmInstallPrepareConsentLabel)
		h.w.helmInstall.busy = "fixture-busy"
		h.click(helmInstallPrepareConsentLabel)
		if h.w.helmInstall.prepareAck {
			t.Fatal("input queued against a previously enabled control granted busy preparation")
		}
		h.w.helmInstall.busy = ""
		h.tt.Frame()
		h.tt.Frame()
		h.w.prepareHelmInstall()
		if calls, _, _ := f.counts(); calls != 0 || h.w.helmInstall.busy != "" || h.w.helmInstall.prepareAck {
			t.Fatal("unlock replayed a rejected acknowledgement")
		}
	})
	t.Run("exact-text-and-composition", func(t *testing.T) {
		f := newNativeInstallFixture(t)
		h := newNativeInstallHarness(t, f, true)
		nativeInstallUseCapture(h, in)
		nativeInstallPreflight(h)
		nativeInstallClick(h, helmInstallExecuteConsentLabel)
		nativeInstallEdit(h, "Helm install release confirmation", " example ")
		nativeInstallClick(h, "Execute Helm install")
		if h.w.helmInstall.attempted {
			t.Fatal("trimmed or inexact release confirmation authorized persistent writes")
		}
		nativeInstallClick(h, "Helm install release confirmation")
		h.tt.Key(ui.Cmd, ui.KeyA)
		h.tt.Key(0, ui.KeyBackspace)
		h.tt.Compose("example", 7)
		h.tt.Frame()
		if !h.w.helmInstall.confirmationComposing {
			t.Fatal("fixture failed to establish a real native IME composition")
		}
		h.w.executeHelmInstall()
		if _, _, writes := f.counts(); writes != 0 || h.w.helmInstall.attempted {
			t.Fatal("uncommitted IME text authorized an installation")
		}
		h.tt.Compose("", 0)
		h.tt.Frame()
		h.w.helmInstall.busy = "fixture-busy"
		h.tt.Type("example")
		h.w.helmInstall.busy = ""
		h.tt.Frame()
		h.tt.Frame()
		if h.w.helmInstall.confirmation != "" || h.w.helmInstall.attempted {
			t.Fatal("busy native text was retained or replayed after unlocking")
		}
	})
	t.Run("edited-input", func(t *testing.T) {
		f := newNativeInstallFixture(t)
		h := newNativeInstallHarness(t, f, true)
		nativeInstallUseCapture(h, in)
		nativeInstallPreflight(h)
		s := &h.w.helmInstall
		oldEpoch, pane := s.epoch, s.paneEpoch
		nativeInstallClick(h, "Helm install release name")
		h.tt.Key(ui.Cmd, ui.KeyA)
		h.tt.Type("n")
		h.tt.Frame()
		h.tt.Type("e")
		h.tt.Frame()
		h.tt.Type("w")
		h.tt.Frame()
		if s.release != "new" || !h.tt.Focused("Helm install release name") || s.paneEpoch != pane || s.epoch <= oldEpoch {
			t.Fatal("review invalidation lost native input focus or failed to reject the old control epoch")
		}
		if s.candidate != nil || s.preparation != nil || s.dryRun != nil || s.flowCtx != nil || s.trusted || s.capabilityAck || s.executeAck {
			t.Fatal("editing selected input retained frozen content or downstream authority")
		}
		h.w.executeHelmInstall()
		if _, _, writes := f.counts(); writes != 0 {
			t.Fatal("edited inputs reused prior execution authority")
		}
	})
}

func TestNativeHelmInstallDryRunOriginSurvivesWorker(t *testing.T) {
	t.Parallel()
	nativeInstallSerialRenderer(t)
	in := nativeInstallCapture(t, "team", 1)
	f := newNativeInstallFixture(t)
	h := newNativeInstallHarness(t, f, true)
	nativeInstallUseCapture(h, in)
	nativeInstallPreflight(h)
	s := &h.w.helmInstall
	if s.cancel != nil || s.flowCtx == nil || s.flowCtx.Err() != nil || !s.dryRun.Matches(s.backend, s.candidate, s.session, time.Now()) {
		t.Fatal("completed dry-run worker cancelled the still independently confirmable flow")
	}
	before, _, _ := f.counts()
	// Stop is a real idle native action: it must remain reachable when a
	// successful dry-run has finished but no execution has been acknowledged.
	h.click("Stop Helm install")
	if s.preparation != nil || s.dryRun != nil || s.flowCtx != nil || s.executeAck || s.confirmation != "" || s.candidate != in.candidate {
		t.Fatal("idle Stop failed to revoke the dry-run origin while retaining inert captured content")
	}
	s.executeAck, s.confirmation = true, "example"
	h.w.executeHelmInstall()
	if calls, _, writes := f.counts(); calls != before || writes != 0 || s.attempted {
		t.Fatal("stopped successful dry-run could still execute")
	}
	if _, err := os.Stat(f.path); !os.IsNotExist(err) {
		t.Fatal("idle Stop or a rejected stale confirmation opened recovery storage", err)
	}
	nativeInstallPrepare(h)
	if s.preparation == nil || s.dryRun != nil || s.executeAck || s.confirmation != "" {
		t.Fatal("a fresh explicit preparation inherited stopped dry-run or write consent")
	}
}

func TestNativeHelmInstallMinimumWindow(t *testing.T) {
	t.Parallel()
	nativeInstallSerialRenderer(t)
	in := nativeInstallCapture(t, "team", 32)
	for _, scenario := range []struct {
		name  string
		scale float32
		rtl   bool
	}{{"scale-1-ltr", 1, false}, {"scale-1.5-rtl", 1.5, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newNativeInstallFixture(t)
			h := newNativeInstallHarness(t, f, true)
			h.tt.SetPreferences(ui.Preferences{TextScale: scenario.scale, HighContrast: true, ReduceMotion: true})
			if scenario.rtl {
				h.tt.SetLocale("ar")
				h.tt.SetDirection(ui.RightToLeft)
			}
			nativeInstallUseCapture(h, in)
			nativeInstallReach(h, "Helm install distribution archive label")
			archive, archiveOK := h.tt.Find("Helm install distribution archive label")
			chart, chartOK := h.tt.Find("Helm install chart archive label")
			// Workbench currently forces LTR, including under an RTL request.
			// Assert the actual physical order; this case is availability under
			// requested RTL, not a claim that the open #113 has been solved.
			if !archiveOK || !chartOK || archive.X >= chart.X {
				t.Fatal("native pane no longer has the documented root LTR layout")
			}
			nativeInstallPrepare(h)
			nativeInstallReach(h, "Helm install captured target 32")
			viewport, _ := h.tt.Find("Helm install inventory")
			connection, _ := h.tt.Find("Connection status")
			if viewport.H < 96 || viewport.Y+viewport.H > connection.Y {
				t.Fatal("scaled native install inventory collapsed or escaped its viewport", viewport, connection)
			}
			for _, label := range []string{"Helm install status", "Stop Helm install", "Close install review"} {
				box, ok := h.tt.Find(label)
				if !ok || box.W <= 0 || box.H <= 0 || box.X < 0 || box.Y < 0 || box.X+box.W > 1100 || box.Y+box.H > connection.Y {
					t.Fatal("scaled review hid a fixed lifecycle control", label, box)
				}
			}
			nativeInstallReach(h, helmInstallDryRunConsentLabel)
			saveNativeScreenshot(t, h.tt)
			if _, dry, writes := f.counts(); dry != 0 || writes != 0 || h.w.pendingWrites != 0 {
				t.Fatal("scrolling or native scaling granted a sensitive or persistent request")
			}
		})
	}
}
