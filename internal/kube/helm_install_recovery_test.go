package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
)

func TestHelmInstallRecoveryJournal(t *testing.T) {
	for _, mode := range []string{"complete", "not-started", "rejected", "secret-unknown", "secret-pending", "target-unknown", "target-pending", "partial", "deploy-unknown", "deploy-pending", "incomplete", "corrupt", "archived"} {
		t.Run(mode, func(t *testing.T) {
			f := newInstallReconciliationFixture(t, 2, mode)
			r := f.recovery
			if r.ID() == "" || r.CandidateID() != f.plan.CandidateID || r.PreparationID() != f.plan.PreparationID || r.Namespace() != "team" ||
				r.Release() != "example" || r.StorageName() != f.plan.StorageName || r.Archived() != (mode == "archived") ||
				!r.StartedAt().Equal(f.plan.Started) || !r.ExpiresAt().Equal(f.plan.Expires) || !r.ExpiresAt().Before(time.Now()) {
				t.Fatal("lost historical identity")
			}
			uid, rv := r.NamespaceIdentity()
			if uid != f.plan.NamespaceUID || rv != f.plan.NamespaceResourceVersion || r.Complete() != (mode != "incomplete" && mode != "corrupt") || r.Corrupt() != (mode == "corrupt") {
				t.Fatal("historical status")
			}
			want := map[string]string{"complete": helmjournal.Completed, "archived": helmjournal.Completed, "not-started": helmjournal.NotStarted,
				"rejected": helmjournal.Rejected, "secret-pending": helmjournal.AcceptedPending, "target-pending": "Partial", "partial": "Partial", "deploy-pending": "Partial"}[mode]
			if want == "" {
				want = helmjournal.Unknown
			}
			if r.Outcome() != want {
				t.Fatal("historical outcome", r.Outcome(), want)
			}
			name := r.ID() + ".wal"
			if r.Archived() {
				name = r.ID() + ".archived.wal"
			}
			path := filepath.Join(f.dir, name)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			effects, targets := r.Effects(), r.Targets()
			r.Effects()[0].UID = "forged"
			r.Effects()[0].Outcome = helmjournal.Completed
			r.Targets()[0].Name = "forged"
			if !reflect.DeepEqual(effects, r.Effects()) || !reflect.DeepEqual(targets, r.Targets()) {
				t.Fatal("historical accessor alias")
			}
			if mode == "secret-unknown" && (effects[0].UID != "" || effects[0].ObservedResourceVersion != "") {
				t.Fatal("invented create identity")
			}
			if mode == "deploy-unknown" || mode == "deploy-pending" || mode == "complete" {
				last := effects[len(effects)-1]
				if last.UID != "storage-uid" || last.ResourceVersion != "storage-before" || mode == "deploy-unknown" && last.ObservedResourceVersion != "" ||
					mode != "deploy-unknown" && last.ObservedResourceVersion != "storage-after" {
					t.Fatal("PATCH precondition/response versions conflated")
				}
			}
			raw, _ := json.Marshal(r)
			if string(raw) != "{}" {
				t.Fatal("private recovery state serialized")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("offline accessor changed journal", err)
			}
			_, requests := f.inspect(t)
			if len(requests) != 0 {
				t.Fatal("offline recovery performed network I/O")
			}
		})
	}
}

func TestHelmInstallRecoveryInventory(t *testing.T) {
	for _, mode := range []string{"active-mixed", "archived-mixed", "corrupt-v2", "unidentified-corrupt", "archived-ineligible-direct", "archived-ineligible-dispatch"} {
		t.Run(mode, func(t *testing.T) {
			history := "complete"
			if strings.HasPrefix(mode, "archived-ineligible") {
				history = "secret-unknown"
			}
			f := newInstallReconciliationFixture(t, 1, history)
			v2 := f.recovery.ID()
			path := filepath.Join(f.dir, v2+".wal")
			if strings.HasPrefix(mode, "archived-ineligible") {
				archived := filepath.Join(f.dir, v2+".archived.wal")
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(path, archived); err != nil {
					t.Fatal(err)
				}
				j, err := OpenHelmUninstallJournal(f.dir)
				if err != nil {
					t.Fatal(err)
				}
				defer j.Close()
				var r *HelmInstallRecovery
				if mode == "archived-ineligible-direct" {
					r, err = j.RecoverInstall(v2, true)
				} else {
					var entry *HelmJournalRecovery
					entry, err = j.RecoverHistory(v2, true)
					r = entry.Install()
				}
				if !errors.Is(err, helmjournal.ErrCorrupt) || r == nil || !r.Corrupt() || r.Complete() || r.Outcome() != helmjournal.Unknown {
					t.Fatal("ineligible archive regained trust", err)
				}
				if c, err := f.b.AcknowledgeHelmInstallReconciliation(r, "new-session", "team", true); c != nil || err != ErrHelmReconciliationConsent {
					t.Fatal("ignored corrupt error minted read grant", err)
				}
				after, err := os.ReadFile(archived)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("recovery repaired archived bytes")
				}
				return
			}
			j, err := OpenHelmUninstallJournal(f.dir)
			if err != nil {
				t.Fatal(err)
			}
			p := helmjournal.Plan{Binding: f.plan.Binding, PreparationID: strings.Repeat("e", 32), Namespace: "team", Revision: selectedHelmRevision(),
				Targets: []helmrelease.UninstallTarget{{APIVersion: "v1", Kind: "ConfigMap", Resource: "configmaps", Namespace: "team", Name: "legacy", UID: "legacy-uid", ResourceVersion: "legacy-rv"}}, Started: f.plan.Started, Expires: f.plan.Expires}
			w, err := j.store.Begin(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.Finish(f.plan.Started.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			v1 := w.ID()
			archived := mode == "archived-mixed"
			if archived {
				for _, id := range []string{v1, v2} {
					review, err := j.store.PrepareArchive(context.Background(), id)
					if err != nil {
						t.Fatal(err)
					}
					c, err := review.Confirm(id)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := j.store.Archive(context.Background(), c); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := j.Close(); err != nil {
				t.Fatal(err)
			}
			if mode == "corrupt-v2" {
				file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, err = file.Write([]byte("PRIVATE_TAIL"))
				if err != nil {
					t.Fatal(err)
				}
				_ = file.Close()
			} else if mode == "unidentified-corrupt" {
				if err := os.WriteFile(path, []byte("PRIVATE_HEADER"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			j, err = OpenHelmUninstallJournal(f.dir)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			old, err := j.RecoverHistory(v1, archived)
			if err != nil || old.Version() != 1 || old.Uninstall() == nil || old.Install() != nil {
				t.Fatal("valid legacy sibling hidden", err)
			}
			entry, err := j.RecoverHistory(v2, archived)
			if mode == "unidentified-corrupt" {
				if !errors.Is(err, helmjournal.ErrCorrupt) || entry.Version() != 0 || entry.Uninstall() == nil || !entry.Uninstall().Corrupt() || entry.Install() != nil {
					t.Fatal("unidentified corrupt entry hidden or reclassified", err)
				}
				return
			}
			if entry.Version() != 2 || entry.Install() == nil || entry.Uninstall() != nil || entry.Install().Archived() != archived {
				t.Fatal("v2 dispatch", err)
			}
			if mode == "corrupt-v2" {
				if !errors.Is(err, helmjournal.ErrCorrupt) || !entry.Install().Corrupt() {
					t.Fatal("checked corrupt v2 prefix trusted", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if _, err := j.RecoverInstall(v1, archived); err != helmjournal.ErrInvalid {
				t.Fatal("v1 accepted as v2", err)
			}
			if _, err := j.recoverIdentified(v2, archived); err != helmjournal.ErrInvalid {
				t.Fatal("v2 accepted by legacy accessor", err)
			}
		})
	}
}

func TestHelmInstallRecoveryEmptyHandles(t *testing.T) {
	var r *HelmInstallRecovery
	uid, rv := r.NamespaceIdentity()
	if r.ID() != "" || r.CandidateID() != "" || r.PreparationID() != "" || r.Namespace() != "" || uid != "" || rv != "" || r.Release() != "" ||
		r.StorageName() != "" || r.Targets() != nil || r.Effects() != nil || r.Outcome() != helmjournal.NotStarted || r.Complete() || !r.Corrupt() || r.Archived() ||
		!r.StartedAt().IsZero() || !r.FinishedAt().IsZero() || !r.ExpiresAt().IsZero() {
		t.Fatal("empty recovery")
	}
	var entry *HelmJournalRecovery
	if entry.Version() != 0 || entry.Uninstall() != nil || entry.Install() != nil {
		t.Fatal("empty typed entry")
	}
	var o *HelmInstallRecoveryObservation
	uid, rv = o.NamespaceIdentity()
	if o.ID() != "" || o.Namespace() != "" || o.Release() != "" || uid != "" || rv != "" || o.HistoryOutcome() != helmjournal.NotStarted ||
		o.Storage() != (HelmInstallRecoveryItem{}) || o.Items() != nil || o.Revisions() != nil || !o.StartedAt().IsZero() || !o.FinishedAt().IsZero() || !o.ExpiresAt().IsZero() {
		t.Fatal("empty observation")
	}
	var c *HelmInstallReconciliationConsent
	c.Discard()
	(&HelmInstallReconciliationConsent{}).Discard()
	var j *HelmUninstallJournal
	if v, err := j.RecoverHistory("", false); v != nil || err != helmjournal.ErrClosed {
		t.Fatal("empty journal dispatch")
	}
	if v, err := j.RecoverInstall("", false); v != nil || err != helmjournal.ErrClosed {
		t.Fatal("empty journal install accessor")
	}
}
