//go:build e2e

package uiworkbench

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/testcluster"
)

// The journal is produced by actual native execution against the disposable API
// server. Archiving may change only a local name, never the server or uncertainty.
func TestNativeHelmArchiveAgainstRealCluster(t *testing.T) {
	for _, kind := range []string{"completed", "committed-response-loss"} {
		t.Run(kind, func(t *testing.T) {
			f := testcluster.NewHelmNativeUninstall(t)
			h := newRelationshipHarness(t, 45*time.Second)
			h.w.helmJournalPath = filepath.Join(t.TempDir(), "journal")
			audit := &nativeHelmAudit{loseDelete: kind != "completed"}
			connectNativeHelmFixture(t, h, f, audit)
			result := runNativeHelmUninstall(t, h, f, audit)
			id := result.JournalID()
			original, err := os.ReadFile(filepath.Join(h.w.helmJournalPath, id+".wal"))
			nativeHelmJournalMust(t, err)
			before, _ := audit.snapshot()
			h.w.openHelmRecovery()
			h.w.loadHelmRecovery()
			h.pump(func() bool { return h.w.helmUninstall.busy == "" })
			h.w.selectHelmRecovery(id)
			nativeHelmE2EClick(h, "Review local archive")
			h.pump(func() bool { return h.w.helmUninstall.archive != nil || h.w.helmUninstall.busy == "" })
			if kind == "completed" {
				if result.Outcome() != kube.HelmExecutionCompleted || h.w.helmUninstall.archive == nil {
					t.Fatal("completed history unavailable", h.w.helmUninstall.status)
				}
				nativeHelmE2EClick(h, helmArchiveAckLabel)
				nativeHelmE2EClick(h, "Helm archive exact ID")
				h.tt.Type(id)
				h.tt.Frame()
				nativeHelmE2EClick(h, "Archive this local record")
				h.pump(func() bool { return h.w.helmUninstall.busy == "" })
				if out := h.w.helmUninstall.archiveResult; out == nil || out.Outcome != helmjournal.ArchiveCompleted {
					t.Fatal("real history archive", h.w.helmUninstall.status)
				}
				after, err := os.ReadFile(filepath.Join(h.w.helmJournalPath, id+".archived.wal"))
				nativeHelmJournalMust(t, err)
				if !bytes.Equal(original, after) {
					t.Fatal("real WAL changed")
				}
				h.w.loadHelmRecovery()
				h.pump(func() bool { return h.w.helmUninstall.busy == "" })
				h.w.selectHelmRecovery(id)
				if !h.w.helmUninstall.recovery.Archived() {
					t.Fatal("retained original not browsable")
				}
				assertNativeHelmRetained(t, h.ctx, f, "uninstalled")
				assertNativeHelmTargets(t, h.ctx, f, len(f.Targets))
			} else {
				if result.Outcome() != kube.HelmExecutionUnknown || h.w.helmUninstall.archive != nil {
					t.Fatal("lost response was cleared")
				}
				h.w.confirmHelmArchive()
				after, err := os.ReadFile(filepath.Join(h.w.helmJournalPath, id+".wal"))
				nativeHelmJournalMust(t, err)
				if !bytes.Equal(original, after) {
					t.Fatal("uncertain WAL changed")
				}
				if _, err = os.Stat(filepath.Join(h.w.helmJournalPath, id+".archived.wal")); !os.IsNotExist(err) {
					t.Fatal("uncertainty archived")
				}
				assertNativeHelmRetained(t, h.ctx, f, "uninstalling")
				assertNativeHelmTargets(t, h.ctx, f, 1)
			}
			after, _ := audit.snapshot()
			if !reflect.DeepEqual(before, after) {
				t.Fatal("archival sent an API request")
			}
			h.tt.SetSize(860, 560)
			h.tt.Frame()
			nativeHelmE2EReach(h, "Helm archive boundary")
			saveNativeScreenshot(t, h.tt)
			h.tt.Key(ui.Cmd, ui.KeyEscape)
		})
	}
}
