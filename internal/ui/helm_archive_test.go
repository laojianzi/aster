package uiworkbench

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/helmjournal"
)

func nativeArchiveHarness(t *testing.T, unknown bool) (*relationshipHarness, string, []byte) {
	t.Helper()
	h := newRelationshipHarness(t, 10*time.Second)
	h.w.helmJournalPath = filepath.Join(t.TempDir(), "journal")
	store, err := helmjournal.Open(h.w.helmJournalPath)
	nativeHelmJournalMust(t, err)
	writer, err := store.Begin(nativeHelmJournalPlan("archive-storage"))
	nativeHelmJournalMust(t, err)
	if unknown {
		nativeHelmJournalMust(t, writer.Intent(0, "7"))
	}
	nativeHelmJournalMust(t, writer.Finish(time.Now()))
	id := writer.ID()
	nativeHelmJournalMust(t, store.Close())
	original, err := os.ReadFile(filepath.Join(h.w.helmJournalPath, id+".wal"))
	nativeHelmJournalMust(t, err)
	h.w.openHelmRecovery()
	h.w.loadHelmRecovery()
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	h.w.selectHelmRecovery(id)
	return h, id, original
}
func nativeArchiveReview(t *testing.T, h *relationshipHarness) {
	t.Helper()
	h.w.prepareHelmArchive()
	h.pump(func() bool { return h.w.helmUninstall.archive != nil || h.w.helmUninstall.busy == "" })
	if h.w.helmUninstall.archive == nil {
		t.Fatal(h.w.helmUninstall.status)
	}
}
func nativeArchiveUnlock(t *testing.T, path string) {
	t.Helper()
	s, err := helmjournal.OpenExisting(path)
	nativeHelmJournalMust(t, err)
	nativeHelmJournalMust(t, s.Close())
}

func TestNativeHelmArchiveExactConfirmation(t *testing.T) {
	h, id, original := nativeArchiveHarness(t, false)
	nativeArchiveReview(t, h)
	nativeHelmClick(h, helmArchiveAckLabel)
	nativeHelmClick(h, "Helm archive exact ID")
	h.tt.Type("wrong")
	h.tt.Frame()
	nativeHelmClick(h, "Archive this local record")
	if h.w.helmUninstall.archiveReload {
		t.Fatal("wrong ID authorized archive")
	}
	nativeHelmClick(h, "Helm archive exact ID")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Key(0, ui.KeyBackspace)
	h.tt.Type(id)
	h.tt.Frame()
	nativeHelmClick(h, "Archive this local record")
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	out := h.w.helmUninstall.archiveResult
	if out == nil || out.Outcome != helmjournal.ArchiveCompleted || !h.w.helmUninstall.archiveReload {
		t.Fatal(h.w.helmUninstall.status)
	}
	after, err := os.ReadFile(filepath.Join(h.w.helmJournalPath, id+".archived.wal"))
	nativeHelmJournalMust(t, err)
	if !bytes.Equal(original, after) {
		t.Fatal("original changed")
	}
	h.w.prepareHelmArchive()
	if h.w.helmUninstall.busy != "" {
		t.Fatal("old snapshot replayed")
	}
	h.w.loadHelmRecovery()
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	h.w.selectHelmRecovery(id)
	if !h.w.helmUninstall.recovery.Archived() {
		t.Fatal("archived original not browsable")
	}
	h.w.prepareHelmArchive()
	if h.w.helmUninstall.busy != "" {
		t.Fatal("archived view granted another archive")
	}
	nativeHelmReach(h, "Helm archive location")
	saveNativeScreenshot(t, h.tt)
}

func TestNativeHelmArchiveCompositionAndAcknowledgement(t *testing.T) {
	h, id, _ := nativeArchiveHarness(t, false)
	nativeArchiveReview(t, h)
	h.w.helmUninstall.archiveConfirmation = id
	h.w.confirmHelmArchive()
	if h.w.helmUninstall.busy != helmArchiveReviewBusy {
		t.Fatal("missing acknowledgement accepted")
	}
	h.w.helmUninstall.archiveConfirmation = ""
	nativeHelmClick(h, helmArchiveAckLabel)
	nativeHelmClick(h, "Helm archive exact ID")
	h.tt.Compose(id, len(id))
	h.tt.Frame()
	if !h.w.helmUninstall.archiveComposing {
		t.Fatal("composition not established")
	}
	h.w.confirmHelmArchive()
	if h.w.helmUninstall.archiveReload {
		t.Fatal("composition granted authority")
	}
	h.w.stopHelmUninstall()
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	nativeArchiveUnlock(t, h.w.helmJournalPath)
}

func TestNativeHelmArchiveCancellationAndScope(t *testing.T) {
	for _, kind := range []string{"stop", "close-pane", "close-window", "path", "backend", "selection"} {
		t.Run(kind, func(t *testing.T) {
			h, _, _ := nativeArchiveHarness(t, false)
			nativeArchiveReview(t, h)
			session := h.w.helmUninstall.archive
			path := h.w.helmJournalPath
			switch kind {
			case "stop":
				h.w.stopHelmUninstall()
			case "close-pane":
				h.w.clearHelmUninstall()
			case "close-window":
				h.w.Close()
			case "path":
				h.w.helmJournalPath = filepath.Join(t.TempDir(), "other")
				h.w.syncHelmUninstall()
			case "backend":
				f := newNativeHelmUninstallFixture(t, 1)
				h.w.backend = f.backend(t)
				h.w.syncHelmUninstall()
			case "selection":
				h.w.clearHelmUninstall()
				h.w.openHelmRecovery()
			}
			select {
			case <-session.Done():
			case <-time.After(time.Second):
				t.Fatal("lifecycle leaked Store")
			}
			result, _, done := session.Result()
			if !done || result.Outcome != helmjournal.ArchiveNotApplied {
				t.Fatal("cancelled review moved file")
			}
			nativeArchiveUnlock(t, path)
			if kind != "close-window" {
				h.tt.Frame()
			}
		})
	}
}

func TestNativeHelmArchiveDroppedCallbacksReleaseLock(t *testing.T) {
	h, id, _ := nativeArchiveHarness(t, false)
	var queued []func()
	h.w.dispatch = func(f func()) { queued = append(queued, f) }
	h.w.prepareHelmArchive()
	// Close cancels and joins the worker without delivering any queued callback.
	h.w.Close()
	nativeArchiveUnlock(t, h.w.helmJournalPath)
	for _, callback := range queued {
		callback()
	}
	if h.w.helmUninstall.archive != nil {
		t.Fatal("discarded callback revived authority")
	}
	if _, err := os.Stat(filepath.Join(h.w.helmJournalPath, id+".wal")); err != nil {
		t.Fatal(err)
	}
}

func TestNativeHelmArchiveUnknownAndMinimumWindow(t *testing.T) {
	h, id, original := nativeArchiveHarness(t, true)
	h.w.prepareHelmArchive()
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	if h.w.helmUninstall.archive != nil || !strings.Contains(h.w.helmUninstall.status, "unresolved") {
		t.Fatal("Unknown admitted", h.w.helmUninstall.status)
	}
	after, err := os.ReadFile(filepath.Join(h.w.helmJournalPath, id+".wal"))
	nativeHelmJournalMust(t, err)
	if !bytes.Equal(original, after) {
		t.Fatal("Unknown changed")
	}
	h.tt.SetSize(860, 560)
	h.tt.Frame()
	nativeHelmReach(h, "Helm archive boundary")
	saveNativeScreenshot(t, h.tt)
}

func TestNativeHelmArchiveDoesNotRequestCluster(t *testing.T) {
	f := newNativeHelmUninstallFixture(t, 1)
	h, id, _ := nativeArchiveHarness(t, false)
	h.w.clearHelmUninstall()
	h.w.backend = f.backend(t)
	h.w.openHelmRecovery()
	h.w.loadHelmRecovery()
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	h.w.selectHelmRecovery(id)
	_, _, _, _, before := f.snapshot(t)
	nativeArchiveReview(t, h)
	h.w.helmUninstall.archiveAck = true
	h.w.helmUninstall.archiveConfirmation = id
	h.w.confirmHelmArchive()
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	_, _, _, _, after := f.snapshot(t)
	if len(before) != len(after) || len(after) != 0 {
		t.Fatal("local archival sent HTTP", after)
	}
	nativeArchiveUnlock(t, h.w.helmJournalPath)
	// Reading the retained original is also offline and cannot initialize a new path.
	entries, err := loadHelmJournalHistory(context.Background(), h.w.helmJournalPath)
	nativeHelmJournalMust(t, err)
	if len(entries) != 1 || !entries[0].recovery.Archived() {
		t.Fatal("retained history missing")
	}
}

func TestNativeHelmArchiveLateCancelPreservesVerifiedResult(t *testing.T) {
	h, id, _ := nativeArchiveHarness(t, false)
	nativeArchiveReview(t, h)
	session := h.w.helmUninstall.archive
	h.w.helmUninstall.archiveAck = true
	h.w.helmUninstall.archiveConfirmation = id
	h.w.confirmHelmArchive()
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("archive did not finish")
	}
	// The actual move and Store close have completed, but the UI has not consumed
	// the completion event. Cancelling now must not rewrite Archived as Cancelled.
	h.w.stopHelmUninstall()
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	if out := h.w.helmUninstall.archiveResult; out == nil || out.Outcome != helmjournal.ArchiveCompleted {
		t.Fatal("verified effect erased", h.w.helmUninstall.status)
	}
	nativeArchiveUnlock(t, h.w.helmJournalPath)
}

func TestNativeHelmArchiveMinimumWindowReview(t *testing.T) {
	h, _, _ := nativeArchiveHarness(t, false)
	h.tt.SetSize(860, 560)
	h.tt.Frame()
	nativeArchiveReview(t, h)
	for _, label := range []string{"Helm archive active capacity", "Helm archive retained capacity", helmArchiveAckLabel, "Helm archive exact ID", "Archive this local record"} {
		nativeHelmReach(h, label)
	}
	saveNativeScreenshot(t, h.tt)
	if h.w.helmUninstall.archiveReload {
		t.Fatal("viewing review authorized an attempt")
	}
	h.w.stopHelmUninstall()
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	nativeArchiveUnlock(t, h.w.helmJournalPath)
}

func TestNativeHelmArchiveConnectionCancellation(t *testing.T) {
	h, _, _ := nativeArchiveHarness(t, false)
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	h.w.connectionCtx = ctx
	nativeArchiveReview(t, h)
	session := h.w.helmUninstall.archive
	cancel()
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("connection teardown depended on UI delivery")
	}
	nativeArchiveUnlock(t, h.w.helmJournalPath)
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
}

func TestNativeHelmArchiveDifferentHistoryCannotConfirm(t *testing.T) {
	h, id, _ := nativeArchiveHarness(t, false)
	nativeArchiveReview(t, h)
	original := h.w.helmUninstall.recovery
	copy := *original
	h.w.helmUninstall.recovery = &copy
	h.w.helmUninstall.archiveAck = true
	h.w.helmUninstall.archiveConfirmation = id
	h.w.confirmHelmArchive()
	if h.w.helmUninstall.archiveReload {
		t.Fatal("different loaded projection confirmed original grant")
	}
	session := h.w.helmUninstall.archive
	h.w.clearHelmUninstall()
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("stale review leaked lock")
	}
	nativeArchiveUnlock(t, h.w.helmJournalPath)
}
