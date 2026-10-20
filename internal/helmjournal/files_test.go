package helmjournal

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelmJournalPrivateFilesAndPaths(t *testing.T) {
	t.Run("explicit-private-location", func(t *testing.T) {
		for _, path := range []string{"", "relative-journal", filepath.Join(t.TempDir(), "absent", "journal"), t.TempDir() + string(os.PathSeparator) + "x" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "journal"} {
			if store, err := Open(path); err == nil {
				store.Close()
				t.Fatal("invalid or unavailable journal path was silently accepted")
			}
		}
		parent := t.TempDir()
		path := filepath.Join(parent, "file")
		mustJournal(t, os.WriteFile(path, []byte("PRIVATE_UNRELATED_FILE"), 0600))
		if store, err := Open(path); err == nil {
			store.Close()
			t.Fatal("regular file became a directory")
		}
		data, err := os.ReadFile(path)
		mustJournal(t, err)
		if string(data) != "PRIVATE_UNRELATED_FILE" {
			t.Fatal("failed Open modified an existing file")
		}
	})
	t.Run("strict-ids-and-entries", func(t *testing.T) {
		s, dir := testStore(t)
		for _, id := range []string{"", "../credentials", strings.Repeat("A", 32), strings.Repeat("a", 31)} {
			if _, err := s.Load(id); !errors.Is(err, ErrInvalid) {
				t.Fatal("noncanonical ID reached the filesystem")
			}
		}
		mustJournal(t, os.WriteFile(filepath.Join(dir, "unexpected-file"), []byte("PRIVATE_UNTRUSTED_INPUT"), 0600))
		if _, err := s.List(); !errors.Is(err, ErrCorrupt) {
			t.Fatal("unrecognized store entry was ignored")
		}
		if _, err := s.Begin(testPlan()); !errors.Is(err, ErrCorrupt) {
			t.Fatal("unrecognized entry allowed execution")
		}
	})
	t.Run("private-owner-and-permissions", platformPermissionChecks)
	t.Run("redirects-and-nonregular-files", platformRedirectChecks)
	t.Run("changed-lock-file", func(t *testing.T) {
		s, dir := testStore(t)
		w := testBegin(t, s, testPlan())
		f, err := os.OpenFile(filepath.Join(dir, lockName), os.O_WRONLY, 0600)
		mustJournal(t, err)
		// Windows locks byte zero; modifying a different byte must still fail
		// the immutable permanent-lock-file check on every public read/write.
		_, err = f.WriteAt([]byte{'!'}, 1)
		mustJournal(t, err)
		mustJournal(t, f.Close())
		if _, err := s.List(); !errors.Is(err, ErrUnavailable) {
			t.Fatal("changed lock file admitted inventory")
		}
		if _, err := s.Load(w.ID()); !errors.Is(err, ErrUnavailable) {
			t.Fatal("changed lock file admitted recovery")
		}
		if err := w.Intent(0, "7"); !errors.Is(err, ErrUnavailable) {
			t.Fatal("changed lock file admitted an effect intent")
		}
	})
	t.Run("hardlink", func(t *testing.T) {
		s, dir := testStore(t)
		w := testBegin(t, s, testPlan())
		mustJournal(t, w.Abort())
		mustJournal(t, os.Link(filepath.Join(dir, w.ID()+".wal"), filepath.Join(filepath.Dir(dir), "outside-hardlink")))
		if _, err := s.Load(w.ID()); !errors.Is(err, ErrUnavailable) {
			t.Fatal("multiply linked journal file was accepted")
		}
	})
	t.Run("held-file-replacement", func(t *testing.T) {
		s, dir := testStore(t)
		w := testBegin(t, s, testPlan())
		name := filepath.Join(dir, w.ID()+".wal")
		moved := filepath.Join(filepath.Dir(dir), "moved.wal")
		if err := os.Rename(name, moved); err != nil {
			// Windows denies renaming the inode while this no-delete-share handle
			// is held. That protection is also an acceptable prevention result.
			mustJournal(t, w.Intent(0, "7"))
			return
		}
		f, err := s.root.openFile(w.ID()+".wal", true, true)
		mustJournal(t, err)
		mustJournal(t, f.Close())
		if !errors.Is(w.Intent(0, "7"), ErrUnavailable) {
			t.Fatal("replacement path received a new effect intent")
		}
	})
	t.Run("held-directory-replacement", func(t *testing.T) {
		s, dir := testStore(t)
		w := testBegin(t, s, testPlan())
		moved := filepath.Join(filepath.Dir(dir), "moved-directory")
		if err := os.Rename(dir, moved); err != nil {
			mustJournal(t, w.Intent(0, "7"))
			return
		}
		if !errors.Is(w.Intent(0, "7"), ErrUnavailable) {
			t.Fatal("disappeared or replaced store path was still usable")
		}
	})
	t.Run("same-length-prefix-change", func(t *testing.T) {
		s, dir := testStore(t)
		w := testBegin(t, s, testPlan())
		name := filepath.Join(dir, w.ID()+".wal")
		f, err := os.OpenFile(name, os.O_RDWR, 0600)
		mustJournal(t, err)
		_, err = f.WriteAt([]byte{'!'}, int64(len(journalMagic)))
		mustJournal(t, err)
		mustJournal(t, f.Close())
		if !errors.Is(w.Intent(0, "7"), ErrUnavailable) {
			t.Fatal("same-length journal alteration was not detected")
		}
		data, err := os.ReadFile(name)
		mustJournal(t, err)
		if len(data) != w.size {
			t.Fatal("failed prefix validation appended another frame")
		}
	})
}
