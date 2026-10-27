package kube

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
)

func TestHelmInstallJournalRejectsUninstallRecovery(t *testing.T) {
	for _, mode := range []string{"active", "archived", "corrupt-prefix"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "journal")
			s, err := helmjournal.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			p := helmjournal.InstallPlan{Binding: strings.Repeat("a", 64), CandidateID: strings.Repeat("b", 32), PreparationID: strings.Repeat("c", 32),
				Namespace: "team", NamespaceUID: "ns-uid", NamespaceResourceVersion: "1", Release: "example", StorageName: "sh.helm.release.v1.example.v1",
				StorageDigest: strings.Repeat("d", 64), Targets: []helmjournal.InstallTarget{{Name: "first", Digest: strings.Repeat("e", 64)}}, Started: now, Expires: now.Add(20 * time.Second)}
			w, err := s.BeginInstall(p)
			if err != nil {
				t.Fatal(err)
			}
			id := w.ID()
			if err = w.Finish(time.Now()); err != nil {
				t.Fatal(err)
			}
			if mode == "archived" {
				review, e := s.PrepareArchive(context.Background(), id)
				if e != nil {
					t.Fatal(e)
				}
				consent, e := review.Confirm(id)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = s.Archive(context.Background(), consent); e != nil {
					t.Fatal(e)
				}
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			if mode == "corrupt-prefix" { // A valid install prefix is still not an uninstall even with a bad tail.
				if err = appendInstallCorruption(filepath.Join(dir, id+".wal")); err != nil {
					t.Fatal(err)
				}
			}
			j, err := OpenHelmUninstallJournal(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			r, err := j.recoverIdentified(id, mode == "archived")
			if r != nil || !errors.Is(err, helmjournal.ErrInvalid) {
				t.Fatal("installation history exposed legacy uninstall/reconciliation projection")
			}
		})
	}
}

func appendInstallCorruption(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	_, e := f.Write([]byte{0, 0})
	return errors.Join(e, f.Close())
}
