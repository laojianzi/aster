//go:build linux || darwin

package helmjournal

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func platformAdmissionPermissionChecks(t *testing.T) {
	for _, directory := range []bool{true, false} {
		s, dir := testStore(t)
		mustJournal(t, s.Close())
		path, mode := filepath.Join(dir, lockName), os.FileMode(0640)
		if directory {
			path, mode = dir, 0750
		}
		mustJournal(t, os.Chmod(path, mode))
		before := admissionFiles(t, dir)
		got, err := ReviewAdmission(context.Background(), dir, admissionScope(testPlan()))
		requireAdmission(t, got, err, AdmissionUnavailable, ErrUnavailable)
		unchangedAdmissionFiles(t, dir, before)
	}
}

func platformAdmissionRedirectChecks(t *testing.T) {
	s, dir := testStore(t)
	mustJournal(t, s.Close())
	link := filepath.Join(filepath.Dir(dir), "redirect")
	mustJournal(t, os.Symlink(dir, link))
	before := admissionFiles(t, dir)
	got, err := ReviewAdmission(context.Background(), link, admissionScope(testPlan()))
	requireAdmission(t, got, err, AdmissionUnavailable, ErrUnavailable)
	unchangedAdmissionFiles(t, dir, before)
	outside := filepath.Join(filepath.Dir(dir), "PRIVATE_OUTSIDE")
	mustJournal(t, os.WriteFile(outside, []byte("not a journal"), 0600))
	mustJournal(t, os.Symlink(outside, filepath.Join(dir, fixtureID+".wal")))
	before = admissionFiles(t, dir)
	got, err = ReviewAdmission(context.Background(), dir, admissionScope(testPlan()))
	requireAdmission(t, got, err, AdmissionCorrupt, ErrCorrupt)
	unchangedAdmissionFiles(t, dir, before)
	// A FIFO must be classified from its entry type without blocking on Open.
	mustJournal(t, os.Remove(filepath.Join(dir, fixtureID+".wal")))
	mustJournal(t, unix.Mkfifo(filepath.Join(dir, fixtureID+".wal"), 0600))
	before = admissionFiles(t, dir)
	got, err = ReviewAdmission(context.Background(), dir, admissionScope(testPlan()))
	requireAdmission(t, got, err, AdmissionCorrupt, ErrCorrupt)
	unchangedAdmissionFiles(t, dir, before)
}
