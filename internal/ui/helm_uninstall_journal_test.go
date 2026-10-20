package uiworkbench

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
)

func TestNativeHelmJournalStablePrivateHomePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	path, err := defaultHelmJournalPath()
	if err != nil || !filepath.IsAbs(path) || filepath.Clean(path) != path ||
		filepath.Dir(path) != filepath.Clean(home) || filepath.Base(path) != ".aster-helm-uninstall" {
		t.Fatal("journal is not the stable single private home leaf", err)
	}
	again, err := defaultHelmJournalPath()
	if err != nil || again != path {
		t.Fatal("journal location changed across workspaces", err)
	}
}

func TestNativeHelmJournalHistoryCreatesPrivateStoreAndReleasesLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal")
	entries, err := loadHelmJournalHistory(context.Background(), path)
	if err != nil || len(entries) != 0 {
		t.Fatal("empty history", len(entries), err)
	}
	if runtime.GOOS != "windows" {
		for name, mode := range map[string]os.FileMode{path: 0700, filepath.Join(path, "journal.lock"): 0600} {
			info, statErr := os.Stat(name)
			if statErr != nil || info.Mode().Perm() != mode {
				t.Fatal("history did not retain private permissions", statErr)
			}
		}
	}
	// This performs the real platform privacy and OS-lock checks, including DACL
	// validation on Windows. No UI callback is required for the first lock to end.
	j, err := kube.OpenHelmUninstallJournal(path)
	if err != nil {
		t.Fatal("history worker leaked its lock", err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
}

func nativeHelmJournalPlan(uid string) helmjournal.Plan {
	now := time.Now().UTC()
	return helmjournal.Plan{
		Binding: strings.Repeat("a", 64), PreparationID: strings.Repeat("b", 32), Namespace: "team",
		Revision: helmrelease.Revision{Release: "example", Number: 2, Status: "deployed",
			StorageName: "sh.helm.release.v1.example.v2", UID: uid, ResourceVersion: "7"},
		Targets: []helmrelease.UninstallTarget{{APIVersion: "v1", Kind: "ConfigMap", Resource: "configmaps",
			Namespace: "team", Name: "sample", UID: "target-uid", ResourceVersion: "21"}},
		Started: now, Expires: now.Add(20 * time.Second),
	}
}

func nativeHelmJournalMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeHelmJournalHistoryKeepsOutcomesIntegrityAndOriginalBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal")
	store, err := helmjournal.Open(path)
	nativeHelmJournalMust(t, err)
	t.Cleanup(func() { nativeHelmJournalMust(t, store.Close()) })
	type expectation struct {
		outcome  kube.HelmUninstallExecutionOutcome
		complete bool
		corrupt  bool
	}
	want := map[string]expectation{}
	for _, kind := range []string{"completed", "unfinished", "closed-unknown", "corrupt"} {
		writer, beginErr := store.Begin(nativeHelmJournalPlan("storage-" + kind))
		nativeHelmJournalMust(t, beginErr)
		id := writer.ID()
		nativeHelmJournalMust(t, writer.Intent(0, "7"))
		switch kind {
		case "completed":
			nativeHelmJournalMust(t, writer.Record(0, helmjournal.AcceptedPending, "8"))
			nativeHelmJournalMust(t, writer.Record(0, helmjournal.Completed, "8"))
			nativeHelmJournalMust(t, writer.Intent(1, "21"))
			nativeHelmJournalMust(t, writer.Record(1, helmjournal.AcceptedPending, ""))
			nativeHelmJournalMust(t, writer.Record(1, helmjournal.Completed, ""))
			nativeHelmJournalMust(t, writer.Intent(2, "8"))
			nativeHelmJournalMust(t, writer.Record(2, helmjournal.AcceptedPending, "9"))
			nativeHelmJournalMust(t, writer.Record(2, helmjournal.Completed, "9"))
			nativeHelmJournalMust(t, writer.Finish(time.Now()))
			want[id] = expectation{outcome: kube.HelmExecutionCompleted, complete: true}
		case "closed-unknown":
			nativeHelmJournalMust(t, writer.Record(0, helmjournal.Unknown, ""))
			nativeHelmJournalMust(t, writer.Finish(time.Now()))
			want[id] = expectation{outcome: kube.HelmExecutionUnknown, complete: true}
		case "unfinished":
			want[id] = expectation{outcome: kube.HelmExecutionUnknown}
		case "corrupt":
			nativeHelmJournalMust(t, writer.Record(0, helmjournal.AcceptedPending, "8"))
			nativeHelmJournalMust(t, writer.Record(0, helmjournal.Completed, "8"))
			nativeHelmJournalMust(t, writer.Intent(1, "21"))
			want[id] = expectation{outcome: kube.HelmExecutionUnknown, corrupt: true}
		}
		nativeHelmJournalMust(t, writer.Abort())
	}
	nativeHelmJournalMust(t, store.Close())
	for id, expected := range want {
		if expected.corrupt {
			f, openErr := os.OpenFile(filepath.Join(path, id+".wal"), os.O_APPEND|os.O_WRONLY, 0600)
			nativeHelmJournalMust(t, openErr)
			_, writeErr := f.Write([]byte{0, 1}) // A real torn frame after a valid prefix.
			nativeHelmJournalMust(t, writeErr)
			nativeHelmJournalMust(t, f.Close())
		}
	}
	original := map[string][]byte{}
	for id := range want {
		original[id], err = os.ReadFile(filepath.Join(path, id+".wal"))
		nativeHelmJournalMust(t, err)
	}
	entries, err := loadHelmJournalHistory(context.Background(), path)
	nativeHelmJournalMust(t, err)
	if len(entries) != len(want) {
		t.Fatal("historical inventory omitted a record", len(entries))
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		expected, ok := want[entry.id]
		if !ok || seen[entry.id] || entry.recovery == nil || entry.recovery.ID() != entry.id {
			t.Fatal("unexpected, duplicated or unbound history entry")
		}
		seen[entry.id] = true
		r := entry.recovery
		if r.Outcome() != expected.outcome || r.Complete() != expected.complete || r.Corrupt() != expected.corrupt ||
			errors.Is(entry.err, helmjournal.ErrCorrupt) != expected.corrupt {
			t.Fatal("history completion, outcome or integrity was conflated", entry.id)
		}
		if !expected.corrupt && entry.err != nil {
			t.Fatal("valid history acquired an error", entry.err)
		}
		if expected.corrupt && (len(r.Effects()) != 3 || r.Effects()[0].Outcome != kube.HelmExecutionCompleted || r.Effects()[1].Outcome != kube.HelmExecutionUnknown) {
			t.Fatal("corrupt record lost its fully checked per-effect prefix")
		}
		data, readErr := os.ReadFile(filepath.Join(path, entry.id+".wal"))
		nativeHelmJournalMust(t, readErr)
		if !bytes.Equal(data, original[entry.id]) {
			t.Fatal("history load repaired, truncated or replaced a historical file")
		}
	}
	j, err := kube.OpenHelmUninstallJournal(path)
	nativeHelmJournalMust(t, err)
	nativeHelmJournalMust(t, j.Close())
}

func TestNativeHelmJournalCancelledWorkersDoNotCreateStorage(t *testing.T) {
	for _, operation := range []string{"history", "execution"} {
		t.Run(operation, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "private-path-marker")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var err error
			if operation == "history" {
				entries, loadErr := loadHelmJournalHistory(ctx, path)
				err = loadErr
				if entries != nil {
					t.Fatal("cancelled history published entries")
				}
			} else {
				report, runErr := executeHelmJournal(ctx, path, nil, nil, "session", "team", helmrelease.Revision{})
				err = runErr
				if report != nil {
					t.Fatal("cancelled worker created an execution report")
				}
			}
			if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "private-path-marker") {
				t.Fatal("cancelled worker error", err)
			}
			if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatal("cancelled worker touched storage", statErr)
			}
		})
	}
}

func TestNativeHelmJournalBusyWorkersPreserveOwnerLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal")
	owner, err := kube.OpenHelmUninstallJournal(path)
	nativeHelmJournalMust(t, err)
	t.Cleanup(func() { nativeHelmJournalMust(t, owner.Close()) })
	entries, err := loadHelmJournalHistory(context.Background(), path)
	if !errors.Is(err, helmjournal.ErrBusy) || entries != nil {
		t.Fatal("history bypassed another worker's private lock", err)
	}
	report, err := executeHelmJournal(context.Background(), path, nil, nil, "session", "team", helmrelease.Revision{})
	if !errors.Is(err, helmjournal.ErrBusy) || report != nil {
		t.Fatal("execution bypassed another worker's private lock", err)
	}
	if _, err := owner.IDs(); err != nil {
		t.Fatal("failed contender closed the owner's store", err)
	}
	nativeHelmJournalMust(t, owner.Close())
	if _, err := loadHelmJournalHistory(context.Background(), path); err != nil {
		t.Fatal("worker could not reopen after owner released its lock", err)
	}
}

func TestNativeHelmJournalFailedExecutionAcknowledgementReleasesLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-path-marker")
	report, err := executeHelmJournal(context.Background(), path, nil, nil, "session", "team", helmrelease.Revision{})
	if !errors.Is(err, kube.ErrHelmExecutionConsent) || report != nil || strings.Contains(err.Error(), "private-path-marker") {
		t.Fatal("invalid execution acknowledgement", err)
	}
	j, err := kube.OpenHelmUninstallJournal(path)
	if err != nil {
		t.Fatal("failed acknowledgement leaked the OS lock", err)
	}
	defer j.Close()
	ids, err := j.IDs()
	if err != nil || len(ids) != 0 {
		t.Fatal("failed acknowledgement started a persistent operation", err)
	}
}

func TestNativeHelmJournalInventoryFailureDoesNotPublishPartialHistory(t *testing.T) {
	for _, kind := range []string{"unexpected-file", "record-over-budget"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "journal")
			store, err := helmjournal.Open(path)
			nativeHelmJournalMust(t, err)
			writer, err := store.Begin(nativeHelmJournalPlan("storage-valid"))
			nativeHelmJournalMust(t, err)
			nativeHelmJournalMust(t, writer.Finish(time.Now()))
			var oversizeID string
			if kind == "record-over-budget" {
				oversize, beginErr := store.Begin(nativeHelmJournalPlan("storage-oversized"))
				nativeHelmJournalMust(t, beginErr)
				oversizeID = oversize.ID()
				nativeHelmJournalMust(t, oversize.Finish(time.Now()))
			}
			nativeHelmJournalMust(t, store.Close())
			wantErr := helmjournal.ErrCorrupt
			if kind == "record-over-budget" {
				// Preserve the existing file's native private DACL on Windows;
				// an unrelated file created with inherited permissions is rejected
				// before the budget check, which would exercise a different boundary.
				nativeHelmJournalMust(t, os.Truncate(filepath.Join(path, oversizeID+".wal"), helmjournal.MaxOperationBytes+1))
				wantErr = helmjournal.ErrBudget
			} else {
				nativeHelmJournalMust(t, os.WriteFile(filepath.Join(path, "unexpected"), []byte("not a journal record"), 0600))
			}
			entries, err := loadHelmJournalHistory(context.Background(), path)
			if !errors.Is(err, wantErr) || entries != nil {
				t.Fatal("invalid inventory appeared complete", err)
			}
			j, err := kube.OpenHelmUninstallJournal(path)
			if err != nil {
				t.Fatal("failed inventory leaked the store lock", err)
			}
			nativeHelmJournalMust(t, j.Close())
		})
	}
}

func TestNativeHelmJournalHistoryDoesNotClearUnresolvedAdmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal")
	store, err := helmjournal.Open(path)
	nativeHelmJournalMust(t, err)
	plan := nativeHelmJournalPlan("storage-unresolved")
	writer, err := store.Begin(plan)
	nativeHelmJournalMust(t, err)
	nativeHelmJournalMust(t, writer.Intent(0, "7"))
	nativeHelmJournalMust(t, writer.Record(0, helmjournal.Unknown, ""))
	nativeHelmJournalMust(t, writer.Finish(time.Now()))
	nativeHelmJournalMust(t, store.Close())
	entries, err := loadHelmJournalHistory(context.Background(), path)
	nativeHelmJournalMust(t, err)
	if len(entries) != 1 || !entries[0].recovery.Complete() || entries[0].recovery.Outcome() != kube.HelmExecutionUnknown {
		t.Fatal("terminal Unknown history was not preserved")
	}
	store, err = helmjournal.Open(path)
	nativeHelmJournalMust(t, err)
	defer store.Close()
	_, err = store.Begin(plan)
	if !errors.Is(err, helmjournal.ErrUnresolved) {
		t.Fatal("reading history cleared unresolved admission", err)
	}
}
