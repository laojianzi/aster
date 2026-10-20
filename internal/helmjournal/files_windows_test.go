//go:build windows

package helmjournal

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func broadenACL(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
	mustJournal(t, err)
	acl, _, err := sd.DACL()
	mustJournal(t, err)
	mustJournal(t, windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil))
}

func platformPermissionChecks(t *testing.T) {
	t.Run("existing-directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "broad")
		mustJournal(t, os.Mkdir(dir, 0700))
		broadenACL(t, dir)
		if store, err := Open(dir); !errors.Is(err, ErrUnavailable) {
			if store != nil {
				store.Close()
			}
			t.Fatal("broad Windows DACL accepted")
		}
		sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		mustJournal(t, err)
		acl, _, err := sd.DACL()
		mustJournal(t, err)
		if acl == nil || acl.AceCount != 1 {
			t.Fatal("Open changed existing ACL")
		}
	})
	t.Run("changed-directory", func(t *testing.T) {
		s, dir := testStore(t)
		broadenACL(t, dir)
		if _, err := s.Begin(testPlan()); !errors.Is(err, ErrUnavailable) {
			t.Fatal("changed Windows directory DACL admitted a writer")
		}
	})
	t.Run("changed-file", func(t *testing.T) {
		s, dir := testStore(t)
		w := testBegin(t, s, testPlan())
		broadenACL(t, filepath.Join(dir, w.ID()+".wal"))
		if !errors.Is(w.Intent(0, "7"), ErrUnavailable) {
			t.Fatal("changed Windows file DACL admitted an intent")
		}
	})
	t.Run("native-owner-and-protected-dacl", func(t *testing.T) {
		s, _ := testStore(t)
		w := testBegin(t, s, testPlan())
		for _, f := range []*os.File{s.root.dir, s.lock, w.file} {
			sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
			mustJournal(t, err)
			control, _, err := sd.Control()
			mustJournal(t, err)
			acl, _, err := sd.DACL()
			mustJournal(t, err)
			if control&windows.SE_DACL_PROTECTED == 0 || acl == nil || acl.AceCount != 1 {
				t.Fatal("protected current-user Windows DACL was not established")
			}
			owner, _, err := sd.Owner()
			mustJournal(t, err)
			user, err := journalUser()
			mustJournal(t, err)
			if !windows.EqualSid(owner, user) {
				t.Fatal("Windows record owner changed")
			}
		}
	})
}

func makeJunction(t *testing.T, link, target string) {
	t.Helper()
	// Directory junction creation does not require symlink developer-mode
	// policy. Exercise an actual native reparse point rather than skipping it.
	cmd := exec.Command("cmd.exe", "/c", "mklink", "/J", link, target)
	if err := cmd.Run(); err != nil {
		t.Fatal("could not create isolated native junction fixture")
	}
}

func platformRedirectChecks(t *testing.T) {
	t.Run("directory-reparse-point", func(t *testing.T) {
		parent := t.TempDir()
		target := filepath.Join(parent, "target")
		store, err := Open(target)
		mustJournal(t, err)
		mustJournal(t, store.Close())
		link := filepath.Join(parent, "link")
		makeJunction(t, link, target)
		if s, err := Open(link); !errors.Is(err, ErrUnavailable) {
			if s != nil {
				s.Close()
			}
			t.Fatal("Windows junction journal directory accepted")
		}
	})
	t.Run("record-reparse-point", func(t *testing.T) {
		s, dir := testStore(t)
		target := filepath.Join(filepath.Dir(dir), "target")
		mustJournal(t, os.Mkdir(target, 0700))
		makeJunction(t, filepath.Join(dir, fixtureID+".wal"), target)
		if _, err := s.Load(fixtureID); !errors.Is(err, ErrUnavailable) {
			t.Fatal("Windows record reparse point accepted")
		}
	})
}
