//go:build linux || darwin

package helmjournal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func platformPermissionChecks(t *testing.T) {
	t.Run("existing-directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "broad")
		mustJournal(t, os.Mkdir(dir, 0755))
		if store, err := Open(dir); !errors.Is(err, ErrUnavailable) {
			if store != nil {
				store.Close()
			}
			t.Fatal("public directory accepted")
		}
		st, err := os.Stat(dir)
		mustJournal(t, err)
		if st.Mode().Perm() != 0755 {
			t.Fatal("Open rewrote an existing directory's permissions")
		}
	})
	t.Run("changed-directory", func(t *testing.T) {
		s, dir := testStore(t)
		mustJournal(t, os.Chmod(dir, 0750))
		if _, err := s.Begin(testPlan()); !errors.Is(err, ErrUnavailable) {
			t.Fatal("changed directory permissions admitted a writer")
		}
	})
	t.Run("changed-file", func(t *testing.T) {
		s, dir := testStore(t)
		w := testBegin(t, s, testPlan())
		mustJournal(t, os.Chmod(filepath.Join(dir, w.ID()+".wal"), 0640))
		if !errors.Is(w.Intent(0, "7"), ErrUnavailable) {
			t.Fatal("changed file permissions admitted an intent")
		}
	})
	t.Run("native-owner-and-mode", func(t *testing.T) {
		s, _ := testStore(t)
		w := testBegin(t, s, testPlan())
		for _, entry := range []struct {
			f    *os.File
			mode uint32
		}{{s.root.dir, 0700}, {s.lock, 0600}, {w.file, 0600}} {
			var st unix.Stat_t
			mustJournal(t, unix.Fstat(int(entry.f.Fd()), &st))
			if st.Uid != uint32(os.Geteuid()) || uint32(st.Mode)&07777 != entry.mode {
				t.Fatal("private native mode/owner was not actually established")
			}
		}
	})
}

func platformRedirectChecks(t *testing.T) {
	t.Run("directory-symlink", func(t *testing.T) {
		parent := t.TempDir()
		target := filepath.Join(parent, "target")
		mustJournal(t, os.Mkdir(target, 0700))
		link := filepath.Join(parent, "link")
		mustJournal(t, os.Symlink(target, link))
		if s, err := Open(link); !errors.Is(err, ErrUnavailable) {
			if s != nil {
				s.Close()
			}
			t.Fatal("symlink journal directory accepted")
		}
	})
	t.Run("file-symlink", func(t *testing.T) {
		s, dir := testStore(t)
		w := testBegin(t, s, testPlan())
		mustJournal(t, w.Abort())
		path := filepath.Join(dir, w.ID()+".wal")
		target := filepath.Join(filepath.Dir(dir), "outside.wal")
		mustJournal(t, os.Rename(path, target))
		mustJournal(t, os.Symlink(target, path))
		if _, err := s.Load(w.ID()); !errors.Is(err, ErrUnavailable) {
			t.Fatal("symlink journal file accepted")
		}
	})
	t.Run("fifo-does-not-block", func(t *testing.T) {
		s, dir := testStore(t)
		mustJournal(t, unix.Mkfifo(filepath.Join(dir, fixtureID+".wal"), 0600))
		if _, err := s.Load(fixtureID); !errors.Is(err, ErrUnavailable) {
			t.Fatal("nonregular file was accepted")
		}
	})
}
