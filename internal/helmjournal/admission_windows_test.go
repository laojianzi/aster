//go:build windows

package helmjournal

import (
	"context"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func platformAdmissionPermissionChecks(t *testing.T) {
	for _, directory := range []bool{true, false} {
		s, dir := testStore(t)
		mustJournal(t, s.Close())
		path := filepath.Join(dir, lockName)
		if directory {
			path = dir
		}
		broadenACL(t, path)
		original, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		mustJournal(t, err)
		before := admissionFiles(t, dir)
		got, err := ReviewAdmission(context.Background(), dir, admissionScope(testPlan()))
		requireAdmission(t, got, err, AdmissionUnavailable, ErrUnavailable)
		unchangedAdmissionFiles(t, dir, before)
		after, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		mustJournal(t, err)
		if original.String() != after.String() {
			t.Fatal("review rewrote the original owner or Windows ACL")
		}
	}
}

func platformAdmissionRedirectChecks(t *testing.T) {
	s, dir := testStore(t)
	mustJournal(t, s.Close())
	link := filepath.Join(filepath.Dir(dir), "redirect")
	makeJunction(t, link, dir)
	before := admissionFiles(t, dir)
	got, err := ReviewAdmission(context.Background(), link, admissionScope(testPlan()))
	requireAdmission(t, got, err, AdmissionUnavailable, ErrUnavailable)
	unchangedAdmissionFiles(t, dir, before)
	outside := filepath.Join(filepath.Dir(dir), "outside")
	target, err := Open(outside)
	mustJournal(t, err)
	mustJournal(t, target.Close())
	makeJunction(t, filepath.Join(dir, fixtureID+".wal"), outside)
	before = admissionFiles(t, dir)
	got, err = ReviewAdmission(context.Background(), dir, admissionScope(testPlan()))
	requireAdmission(t, got, err, AdmissionCorrupt, ErrCorrupt)
	unchangedAdmissionFiles(t, dir, before)
}
