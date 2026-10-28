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
)

func nativeHelmInstallHistoryPlan(release string) helmjournal.InstallPlan {
	started := time.Now().UTC().Add(-time.Hour)
	return helmjournal.InstallPlan{
		Binding: strings.Repeat("a", 64), CandidateID: strings.Repeat("b", 32), PreparationID: strings.Repeat("c", 32),
		Namespace: "team", NamespaceUID: "namespace-uid", NamespaceResourceVersion: "100",
		Release: release, StorageName: "sh.helm.release.v1." + release + ".v1", StorageDigest: strings.Repeat("d", 64),
		Targets: []helmjournal.InstallTarget{{Name: "config-00", Digest: strings.Repeat("e", 64)}},
		Started: started, Expires: started.Add(20 * time.Second),
	}
}

func archiveNativeHelmHistory(t *testing.T, s *helmjournal.Store, id string) {
	t.Helper()
	review, err := s.PrepareArchive(context.Background(), id)
	nativeHelmJournalMust(t, err)
	consent, err := review.Confirm(id)
	nativeHelmJournalMust(t, err)
	result, err := s.Archive(context.Background(), consent)
	nativeHelmJournalMust(t, err)
	if result.Outcome != helmjournal.ArchiveCompleted {
		t.Fatal("history fixture did not archive")
	}
}

func TestNativeHelmInstallRecoveryMixedHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal")
	s, err := helmjournal.Open(path)
	nativeHelmJournalMust(t, err)
	t.Cleanup(func() { nativeHelmJournalMust(t, s.Close()) })
	files := map[string]string{}
	legacy := map[string]bool{}
	for _, archived := range []bool{false, true} {
		old, err := s.Begin(nativeHelmJournalPlan("mixed-v1"))
		nativeHelmJournalMust(t, err)
		nativeHelmJournalMust(t, old.Finish(time.Now()))
		install, err := s.BeginInstall(nativeHelmInstallHistoryPlan("mixed-v2"))
		nativeHelmJournalMust(t, err)
		nativeHelmJournalMust(t, install.Finish(time.Now()))
		legacy[old.ID()] = archived
		for _, id := range []string{old.ID(), install.ID()} {
			suffix := ".wal"
			if archived {
				archiveNativeHelmHistory(t, s, id)
				suffix = ".archived.wal"
			}
			files[id] = filepath.Join(path, id+suffix)
		}
	}
	nativeHelmJournalMust(t, s.Close())
	originals := map[string][]byte{}
	for id, file := range files {
		originals[id], err = os.ReadFile(file)
		nativeHelmJournalMust(t, err)
	}
	entries, err := loadHelmJournalHistory(context.Background(), path)
	if err != nil || len(entries) != len(files) {
		t.Fatalf("mixed v1/v2 active/archive inventory must retain every record: got %d, want %d, err=%v", len(entries), len(files), err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if _, ok := files[entry.id]; !ok || seen[entry.id] || entry.err != nil {
			t.Fatal("mixed history duplicated, replaced or damaged a record")
		}
		seen[entry.id] = true
		if archived, isLegacy := legacy[entry.id]; isLegacy {
			if entry.recovery == nil || entry.installRecovery != nil || entry.recovery.Archived() != archived || !entry.recovery.Complete() {
				t.Fatal("install history hid or changed the legacy projection")
			}
		} else if entry.recovery != nil || entry.installRecovery == nil || !entry.installRecovery.Complete() || entry.installRecovery.Outcome() != helmjournal.NotStarted {
			t.Fatal("installation history did not retain its distinct typed projection")
		}
		after, err := os.ReadFile(files[entry.id])
		nativeHelmJournalMust(t, err)
		if !bytes.Equal(after, originals[entry.id]) {
			t.Fatal("offline mixed history rewrote a historical file")
		}
	}
	// Loading both versions must end the one shared OS lock without a UI callback.
	reopened, err := helmjournal.OpenExisting(path)
	nativeHelmJournalMust(t, err)
	nativeHelmJournalMust(t, reopened.Close())
}

func writeNativeHelmInstallHistory(t *testing.T, s *helmjournal.Store, plan helmjournal.InstallPlan, outcome string) string {
	t.Helper()
	w, err := s.BeginInstall(plan)
	nativeHelmJournalMust(t, err)
	if outcome != helmjournal.NotStarted {
		nativeHelmJournalMust(t, w.Intent(0, plan.StorageDigest))
		if outcome != helmjournal.Unknown {
			response := helmjournal.AcceptedPending
			uid, rv := "storage-uid", "101"
			if outcome == helmjournal.Rejected {
				response, uid, rv = helmjournal.Rejected, "", ""
			}
			nativeHelmJournalMust(t, w.Response(0, response, uid, rv))
			if outcome == "Partial" || outcome == helmjournal.Completed {
				nativeHelmJournalMust(t, w.Observe(0, uid, rv))
			}
			if outcome == helmjournal.Completed {
				for i, target := range plan.Targets {
					nativeHelmJournalMust(t, w.Intent(i+1, target.Digest))
					nativeHelmJournalMust(t, w.Response(i+1, helmjournal.AcceptedPending, "uid-"+target.Name, "102"))
					nativeHelmJournalMust(t, w.Observe(i+1, "uid-"+target.Name, "102"))
				}
				last := len(plan.Targets) + 1
				nativeHelmJournalMust(t, w.Intent(last, strings.Repeat("f", 64)))
				nativeHelmJournalMust(t, w.Response(last, helmjournal.AcceptedPending, uid, "103"))
				nativeHelmJournalMust(t, w.Observe(last, uid, "103"))
			}
		}
	}
	nativeHelmJournalMust(t, w.Finish(time.Now()))
	return w.ID()
}

func TestNativeHelmInstallRecoveryHistoryOutcomes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal")
	s, err := helmjournal.Open(path)
	nativeHelmJournalMust(t, err)
	t.Cleanup(func() { nativeHelmJournalMust(t, s.Close()) })
	want := map[string]string{}
	for _, outcome := range []string{helmjournal.NotStarted, helmjournal.AcceptedPending, "Partial", helmjournal.Unknown, helmjournal.Rejected, helmjournal.Completed} {
		plan := nativeHelmInstallHistoryPlan(strings.ToLower(outcome))
		want[writeNativeHelmInstallHistory(t, s, plan, outcome)] = outcome
	}
	nativeHelmJournalMust(t, s.Close())
	entries, err := loadHelmJournalHistory(context.Background(), path)
	nativeHelmJournalMust(t, err)
	if len(entries) != len(want) {
		t.Fatal("installation outcomes omitted from inventory")
	}
	for _, entry := range entries {
		r := entry.installRecovery
		if r == nil || r.Outcome() != want[entry.id] || !r.Complete() || r.Corrupt() || !time.Now().After(r.ExpiresAt()) {
			t.Fatal("terminal state, historical outcome or historical expiry conflated", entry.id)
		}
		if r.Effects()[0].Outcome != want[entry.id] && want[entry.id] != "Partial" {
			t.Fatal("checked installation effect changed")
		}
	}
}

func TestNativeHelmInstallRecoveryHistoryFailures(t *testing.T) {
	for _, mode := range []string{"active-corrupt-prefix", "unknown-prefix", "archived-corrupt", "oversized", "busy", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "journal")
			s, err := helmjournal.Open(path)
			nativeHelmJournalMust(t, err)
			t.Cleanup(func() { nativeHelmJournalMust(t, s.Close()) })
			old, err := s.Begin(nativeHelmJournalPlan("visible-v1"))
			nativeHelmJournalMust(t, err)
			nativeHelmJournalMust(t, old.Finish(time.Now()))
			id := writeNativeHelmInstallHistory(t, s, nativeHelmInstallHistoryPlan("failure"), helmjournal.NotStarted)
			file := filepath.Join(path, id+".wal")
			if mode == "archived-corrupt" {
				archiveNativeHelmHistory(t, s, id)
				file = filepath.Join(path, id+".archived.wal")
			}
			if mode != "busy" {
				nativeHelmJournalMust(t, s.Close())
			}
			switch mode {
			case "active-corrupt-prefix", "archived-corrupt":
				f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0600)
				nativeHelmJournalMust(t, err)
				_, err = f.Write([]byte{0, 1})
				nativeHelmJournalMust(t, err)
				nativeHelmJournalMust(t, f.Close())
			case "unknown-prefix":
				nativeHelmJournalMust(t, os.WriteFile(file, []byte("unreadable journal header"), 0600))
			case "oversized":
				nativeHelmJournalMust(t, os.Truncate(file, helmjournal.MaxOperationBytes+1))
			}
			before, err := os.ReadFile(file)
			nativeHelmJournalMust(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			entries, loadErr := loadHelmJournalHistory(ctx, path)
			if mode == "active-corrupt-prefix" || mode == "unknown-prefix" {
				nativeHelmJournalMust(t, loadErr)
				if len(entries) != 2 {
					t.Fatal("a damaged active record hid the legacy history")
				}
				for _, entry := range entries {
					if entry.id == old.ID() && (entry.err != nil || entry.recovery == nil || entry.recovery.Corrupt()) {
						t.Fatal("valid v1 history changed beside damaged v2")
					}
					if entry.id != id {
						continue
					}
					if !errors.Is(entry.err, helmjournal.ErrCorrupt) {
						t.Fatal("corrupt record lost its error")
					}
					if mode == "active-corrupt-prefix" {
						if entry.installRecovery == nil || !entry.installRecovery.Corrupt() || entry.installRecovery.Complete() || entry.installRecovery.Outcome() != helmjournal.Unknown {
							t.Fatal("damaged v2 prefix became complete or lost uncertainty")
						}
					} else if entry.recovery == nil || !entry.recovery.Corrupt() || entry.installRecovery != nil {
						t.Fatal("unreadable first frame gained a typed valid installation")
					}
				}
			} else {
				want := map[string]error{"archived-corrupt": helmjournal.ErrCorrupt, "oversized": helmjournal.ErrBudget, "busy": helmjournal.ErrBusy, "cancelled": context.Canceled}[mode]
				if entries != nil || !errors.Is(loadErr, want) {
					t.Fatal("incomplete inventory was published as safe history", mode, len(entries), loadErr)
				}
			}
			after, err := os.ReadFile(file)
			nativeHelmJournalMust(t, err)
			if !bytes.Equal(before, after) {
				t.Fatal("history failure repaired or rewrote a record")
			}
		})
	}
}
