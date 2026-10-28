package uiworkbench

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/kube"
)

func TestNativeHelmInstallJournalCancellation(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"nil-context", "cancelled-before-open"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "must-not-open")
			var ctx context.Context
			want := helmjournal.ErrInvalid
			if scenario == "cancelled-before-open" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
				want = context.Canceled
			}
			result, err := executeHelmInstallJournal(ctx, path, nil, nil, nil, "")
			if result != nil || !errors.Is(err, want) {
				t.Fatal("journal worker accepted an absent or already cancelled context", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("cancelled-before-open worker created its private directory", err)
			}
		})
	}
}

func TestNativeHelmInstallSharedJournalAdmission(t *testing.T) {
	t.Parallel()
	in := nativeInstallCapture(t, "team", 1)
	for _, scenario := range []string{"busy", "unresolved-v1", "unresolved-v2", "capacity"} {
		t.Run(scenario, func(t *testing.T) {
			if scenario == "busy" {
				// Only this child creates a native renderer. Take the gate
				// before any fixture/context; its earliest Cleanup releases
				// it after every Store, worker and server has been closed.
				nativeInstallSerialRenderer(t)
			}
			f := newNativeInstallFixture(t)
			h := newNativeInstallHarness(t, f, false)
			nativeInstallUseCapture(h, in)
			nativeInstallPreflight(h)
			s, err := helmjournal.Open(f.path)
			nativeHelmJournalMust(t, err)
			t.Cleanup(func() { nativeHelmJournalMust(t, s.Close()) })
			wantRecords := 0
			switch scenario {
			case "unresolved-v1":
				plan := nativeHelmJournalPlan("legacy-storage-uid")
				plan.Binding = f.binding(t)
				writer, err := s.Begin(plan)
				nativeHelmJournalMust(t, err)
				nativeHelmJournalMust(t, writer.Intent(0, plan.Revision.ResourceVersion))
				nativeHelmJournalMust(t, writer.Finish(time.Now()))
				wantRecords = 1
			case "unresolved-v2":
				plan := nativeHelmInstallHistoryPlan("example")
				plan.Binding = f.binding(t)
				writeNativeHelmInstallHistory(t, s, plan, helmjournal.Unknown)
				wantRecords = 1
			case "capacity":
				nativeHelmJournalMust(t, s.Close())
				nativeInstallPopulateCapacity(t, f.path, f.binding(t))
				wantRecords = helmjournal.MaxRecords
			}
			nativeHelmJournalMust(t, s.Close())
			// Snapshot every file, including journal.lock, while no Store
			// holds the OS lock. Windows prevents even reading that byte
			// through another handle while its exclusive lock is held.
			before := nativeInstallDiskSnapshot(t, f.path)
			var lockHolder *helmjournal.Store
			if scenario == "busy" {
				lockHolder, err = helmjournal.Open(f.path)
				nativeHelmJournalMust(t, err)
				t.Cleanup(func() { nativeHelmJournalMust(t, lockHolder.Close()) })
			}
			beforeCalls, _, _ := f.counts()
			result := nativeInstallExecute(h)
			if result.Outcome() != helmjournal.NotStarted || result.JournalID() != "" || result.JournalComplete() ||
				!h.w.helmInstall.attempted || h.w.helmInstall.executeAck || h.w.helmInstall.dryRun != nil || h.w.pendingWrites != 0 {
				t.Fatal("blocked shared-journal admission misreported an effect or retained write authority", h.w.helmInstall.status)
			}
			if calls, _, writes := f.counts(); calls != beforeCalls || writes != 0 {
				t.Fatal("shared lock, unresolved history or capacity barrier was checked after cluster I/O")
			}
			if scenario == "busy" {
				if result != nil {
					t.Fatal("failed journal open invented an execution record")
				}
				h.tt = ui.NewTester(h.w.View, 1100, 700)
				nativeInstallReach(h, "Helm install outcome")
				outcome, shown := h.tt.Find("Helm install outcome")
				if !shown || outcome.W <= 0 || outcome.H <= 0 {
					t.Fatal("native view hid the pre-dispatch server outcome")
				}
				nativeInstallReach(h, "Helm install journal outcome")
				journalOutcome, shown := h.tt.Find("Helm install journal outcome")
				if !shown || journalOutcome.W <= 0 || journalOutcome.H <= 0 {
					t.Fatal("native view hid the separate pre-dispatch journal outcome")
				}
				nativeHelmJournalMust(t, lockHolder.Close())
			}
			nativeInstallAssertDiskUnchanged(t, f.path, before)
			reopened, err := helmjournal.OpenExisting(f.path)
			nativeHelmJournalMust(t, err)
			t.Cleanup(func() { nativeHelmJournalMust(t, reopened.Close()) })
			ids, err := reopened.List()
			nativeHelmJournalMust(t, err)
			nativeHelmJournalMust(t, reopened.Close())
			if len(ids) != wantRecords {
				t.Fatal("blocked admission allocated or removed a WAL", len(ids), wantRecords)
			}
			if scenario == "capacity" {
				// The live execution budget is a second independent capacity
				// barrier. A valid 32-target capture can pass preparation/dry-run
				// while being provably impossible at the retained QPS/deadline.
				large := nativeInstallCapture(t, "team", 32)
				limited := newNativeInstallFixture(t)
				lh := newNativeInstallHarness(t, limited, false)
				lh.w.backend = limited.backend(t, true)
				nativeInstallUseCapture(lh, large)
				nativeInstallPreflight(lh)
				calls, _, _ := limited.counts()
				limitedResult := nativeInstallExecute(lh)
				if limitedResult == nil || limitedResult.Outcome() != helmjournal.NotStarted || limitedResult.JournalID() != "" || limitedResult.JournalComplete() || lh.w.helmInstall.status != "Server outcome: NotStarted. "+kube.ErrHelmInstallExecutionBudget.Error() {
					t.Fatal("native execution hid a provable capacity rejection", lh.w.helmInstall.status)
				}
				if after, _, writes := limited.counts(); after != calls || writes != 0 {
					t.Fatal("provably insufficient execution capacity caused a partial operation")
				}
				journal, err := helmjournal.OpenExisting(limited.path)
				nativeHelmJournalMust(t, err)
				t.Cleanup(func() { nativeHelmJournalMust(t, journal.Close()) })
				ids, err := journal.List()
				nativeHelmJournalMust(t, err)
				nativeHelmJournalMust(t, journal.Close())
				if len(ids) != 0 {
					t.Fatal("budget rejection wrote an operation record")
				}
			}
		})
	}
}

func nativeInstallPopulateCapacity(t *testing.T, path, binding string) {
	t.Helper()
	// Each WAL still comes from the real durable writer. Keep its seed Store
	// empty between records so fixture construction does not repeatedly load
	// 0+1+...+127 already verified histories. Move only a finished, closed file
	// on the same volume, preserving its exact ID, bytes and private mode.
	seedPath := filepath.Join(filepath.Dir(path), "capacity-seed")
	originals := make(map[string][]byte, helmjournal.MaxRecords)
	for i := 0; i < helmjournal.MaxRecords; i++ {
		seed, err := helmjournal.Open(seedPath)
		nativeHelmJournalMust(t, err)
		t.Cleanup(func() { nativeHelmJournalMust(t, seed.Close()) })
		plan := nativeHelmInstallHistoryPlan("example")
		plan.Binding = binding
		id := writeNativeHelmInstallHistory(t, seed, plan, helmjournal.NotStarted)
		nativeHelmJournalMust(t, seed.Close())
		if _, duplicate := originals[id]; duplicate {
			t.Fatal("capacity fixture reused an operation ID")
		}
		source, target := filepath.Join(seedPath, id+".wal"), filepath.Join(path, id+".wal")
		originals[id], err = os.ReadFile(source)
		nativeHelmJournalMust(t, err)
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatal("capacity fixture would replace an existing WAL", err)
		}
		nativeHelmJournalMust(t, os.Rename(source, target))
	}
	verified, err := helmjournal.OpenExisting(path)
	nativeHelmJournalMust(t, err)
	t.Cleanup(func() { nativeHelmJournalMust(t, verified.Close()) })
	ids, err := verified.List()
	nativeHelmJournalMust(t, err)
	if len(ids) != helmjournal.MaxRecords {
		t.Fatal("capacity fixture did not retain all real records", len(ids))
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		original, found := originals[id]
		if !found || seen[id] {
			t.Fatal("capacity fixture changed or duplicated an operation ID")
		}
		seen[id] = true
		stored, err := os.ReadFile(filepath.Join(path, id+".wal"))
		nativeHelmJournalMust(t, err)
		if !bytes.Equal(stored, original) {
			t.Fatal("capacity fixture rewrote a real writer's WAL")
		}
		snap, err := verified.Load(id)
		nativeHelmJournalMust(t, err)
		if snap.ID != id || snap.Version != 2 || snap.Action != "install" || !snap.Closed || snap.Corrupt ||
			snap.Integrity != helmjournal.IntegrityValid || snap.Install == nil || snap.Install.Binding != binding ||
			snap.Install.Namespace != "team" || snap.Install.Release != "example" || len(snap.Effects) != 3 {
			t.Fatal("capacity fixture lost a complete valid installation history")
		}
		for _, effect := range snap.Effects {
			if effect.Outcome != helmjournal.NotStarted || effect.UID != "" || effect.ObservedResourceVersion != "" {
				t.Fatal("capacity fixture invented effects or unresolved history")
			}
		}
	}
	admission, err := verified.ReviewAdmission(context.Background(), helmjournal.AdmissionScope{
		Binding: binding, Namespace: "team", Release: "example",
	})
	if !errors.Is(err, helmjournal.ErrBudget) || admission.Reason != helmjournal.AdmissionRecordLimit ||
		!admission.InventoryComplete || admission.Records != helmjournal.MaxRecords {
		t.Fatal("capacity fixture did not establish the exact record-count barrier", admission, err)
	}
	nativeHelmJournalMust(t, verified.Close())
}
