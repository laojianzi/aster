package uiworkbench

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
)

// These controller tests share the real backend and journal but deliberately do
// not create a renderer. The two native interaction tests cover actual widgets.
type helmAdmissionControllerHarness struct {
	t       *testing.T
	w       *Workbench
	ctx     context.Context
	updates chan func()
}

func newHelmAdmissionController(t *testing.T, f *nativeHelmUninstallFixture) *helmAdmissionControllerHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	h := &helmAdmissionControllerHarness{t: t, w: New(), ctx: ctx, updates: make(chan func(), 16)}
	h.w.attach(ctx, func(fn func()) {
		select {
		case h.updates <- fn:
		case <-ctx.Done():
		}
	})
	t.Cleanup(func() { cancel(); h.w.Close() })
	h.w.backend, h.w.namespace, h.w.sessionID = f.backend(t), "team", "admission-controller-session"
	h.w.activeContext, h.w.currentContext, h.w.activeNamespace = "test-context", "test-context", "team"
	h.w.helmJournalPath = filepath.Join(t.TempDir(), "journal")
	snapshot, err := h.w.backend.HelmReleaseMetadata(ctx, "team")
	nativeHelmJournalMust(t, err)
	if len(snapshot.Revisions) != 1 {
		t.Fatal("missing current metadata selection")
	}
	h.w.helmReleases = helmReleaseState{open: true, backend: h.w.backend, contextEpoch: h.w.contextEpoch,
		namespace: "team", session: h.w.sessionID, snapshot: snapshot, selected: snapshot.Revisions[0]}
	h.w.openHelmUninstall()
	if !h.w.helmUninstall.open {
		t.Fatal("controller review did not open")
	}
	return h
}

func (h *helmAdmissionControllerHarness) next() func() {
	h.t.Helper()
	select {
	case fn := <-h.updates:
		return fn
	case <-h.ctx.Done():
		h.t.Fatal("local admission callback did not finish", h.w.helmUninstall.status)
		return nil
	}
}

func (h *helmAdmissionControllerHarness) finish() {
	h.t.Helper()
	h.next()()
	if h.w.helmUninstall.busy != "" {
		h.t.Fatal("controller worker did not settle", h.w.helmUninstall.status)
	}
}

type helmAdmissionFile struct {
	Mode os.FileMode
	Data string
}

func helmAdmissionFiles(t *testing.T, path string) map[string]helmAdmissionFile {
	t.Helper()
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	nativeHelmJournalMust(t, err)
	files := make(map[string]helmAdmissionFile, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		nativeHelmJournalMust(t, err)
		if !info.Mode().IsRegular() {
			t.Fatal("unexpected fixture journal entry")
		}
		data, err := os.ReadFile(filepath.Join(path, entry.Name()))
		nativeHelmJournalMust(t, err)
		files[entry.Name()] = helmAdmissionFile{Mode: info.Mode(), Data: string(data)}
	}
	return files
}

func helmAdmissionUnchanged(t *testing.T, f *nativeHelmUninstallFixture, path string, files map[string]helmAdmissionFile, requests int) {
	t.Helper()
	_, _, _, _, after := f.snapshot(t)
	if len(after) != requests {
		t.Fatal("local admission issued an HTTP request", after[requests:])
	}
	if !reflect.DeepEqual(files, helmAdmissionFiles(t, path)) {
		t.Fatal("local admission changed journal files or permissions")
	}
}

func helmAdmissionInitialize(t *testing.T, path string) {
	t.Helper()
	store, err := helmjournal.Open(path)
	nativeHelmJournalMust(t, err)
	nativeHelmJournalMust(t, store.Close())
}

func helmAdmissionClickCheck(t *testing.T, h *relationshipHarness, f *nativeHelmUninstallFixture, reason helmjournal.AdmissionReason) {
	t.Helper()
	path := h.w.helmJournalPath
	files := helmAdmissionFiles(t, path)
	_, _, _, _, requests := f.snapshot(t)
	nativeHelmClick(h, helmAdmissionCheckLabel)
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	r := h.w.helmUninstall.admission
	if r == nil || r.Report().Reason != reason {
		t.Fatal("unexpected local admission result", h.w.helmUninstall.status)
	}
	helmAdmissionUnchanged(t, f, path, files, len(requests))
	if h.w.pendingWrites != 0 {
		t.Fatal("local check was counted as a persistent write")
	}
}

// Seed matching Unknown history through the actual independently confirmed
// executor, so no test reproduces or exposes the backend's private binding hash.
func helmAdmissionUnknown(t *testing.T, f *nativeHelmUninstallFixture, ctx context.Context, backend *kube.Backend, path, session, ns string, row helmrelease.Revision, p *kube.HelmUninstallPreparation) string {
	t.Helper()
	if p == nil {
		var err error
		p, err = backend.PrepareHelmUninstall(ctx, ns, row, session, true)
		nativeHelmJournalMust(t, err)
	}
	consent, err := backend.AcknowledgeHelmUninstallDryRun(p, session, ns, row, true)
	nativeHelmJournalMust(t, err)
	defer consent.Discard()
	dryRun, err := backend.PreflightHelmUninstall(ctx, consent, session, ns, row)
	nativeHelmJournalMust(t, err)
	journal, err := kube.OpenHelmUninstallJournal(path)
	nativeHelmJournalMust(t, err)
	defer func() { nativeHelmJournalMust(t, journal.Close()) }()
	execution, err := backend.AcknowledgeHelmUninstallExecution(dryRun, session, ns, row, true, journal)
	nativeHelmJournalMust(t, err)
	defer execution.Discard()
	f.setMode("lose-begin")
	result, err := backend.ExecuteHelmUninstall(ctx, execution, session, ns, row)
	f.setMode("")
	if result == nil || result.Outcome() != kube.HelmExecutionUnknown || !result.JournalComplete() || !errors.Is(err, kube.ErrHelmExecutionUnknown) {
		t.Fatal("fixture did not retain genuine Unknown history", err)
	}
	return result.JournalID()
}
