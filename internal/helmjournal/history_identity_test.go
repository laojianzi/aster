package helmjournal

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestHelmJournalExistingArchiveOpen(t *testing.T) {
	for _, kind := range []string{"missing-directory", "missing-lock", "busy", "valid"} {
		t.Run(kind, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "journal")
			var owner *Store
			if kind != "missing-directory" {
				var err error
				owner, err = Open(dir)
				mustJournal(t, err)
				t.Cleanup(func() { owner.Close() })
				if kind != "busy" {
					mustJournal(t, owner.Close())
				}
			}
			if kind == "missing-lock" {
				mustJournal(t, os.Remove(filepath.Join(dir, lockName)))
			}
			s, err := OpenExisting(dir)
			if kind == "valid" {
				mustJournal(t, err)
				mustJournal(t, s.Close())
				return
			}
			if s != nil || err == nil {
				t.Fatal("uninitialized/busy storage acquired", err)
			}
			if kind == "busy" && !errors.Is(err, ErrBusy) {
				t.Fatal(err)
			}
			if kind == "missing-directory" {
				if _, e := os.Stat(dir); !os.IsNotExist(e) {
					t.Fatal("directory created")
				}
			}
			if kind == "missing-lock" {
				if _, e := os.Stat(filepath.Join(dir, lockName)); !os.IsNotExist(e) {
					t.Fatal("lock created")
				}
			}
		})
	}
}

func TestHelmJournalLoadedArchiveIdentity(t *testing.T) {
	for _, kind := range []string{"reopened-original", "copied-identity", "same-bytes-replacement", "directory-replacement", "changed-bytes", "archived"} {
		t.Run(kind, func(t *testing.T) {
			s, dir := testStore(t)
			id := archiveHistory(t, s, "not-started")
			snap, identity, err := s.LoadIdentified(id, false)
			mustJournal(t, err)
			before, err := os.ReadFile(filepath.Join(dir, id+".wal"))
			mustJournal(t, err)
			if kind == "copied-identity" {
				copy := *identity
				identity = &copy
			}
			if kind == "archived" {
				_, err = s.Archive(context.Background(), archiveGrant(t, s, id))
				mustJournal(t, err)
				archived, token, e := s.LoadIdentified(id, true)
				mustJournal(t, e)
				if archived.Digest != snap.Digest {
					t.Fatal("history changed")
				}
				if r, e := s.PrepareLoadedArchive(context.Background(), token); e == nil || r != nil {
					t.Fatal("archived token acquired new grant")
				}
				return
			}
			mustJournal(t, s.Close())
			if kind == "directory-replacement" {
				mustJournal(t, os.Rename(dir, dir+"-old"))
				replacement, e := Open(dir)
				mustJournal(t, e)
				mustJournal(t, replacement.Close())
				mustJournal(t, os.Rename(filepath.Join(dir+"-old", id+".wal"), filepath.Join(dir, id+".wal")))
			}
			if kind == "same-bytes-replacement" {
				mustJournal(t, os.Rename(filepath.Join(dir, id+".wal"), filepath.Join(t.TempDir(), "original.wal")))
				mustJournal(t, os.WriteFile(filepath.Join(dir, id+".wal"), before, 0600))
			}
			if kind == "changed-bytes" {
				mustJournal(t, os.WriteFile(filepath.Join(dir, id+".wal"), append(before, 'x'), 0600))
			}
			opened, e := OpenExisting(dir)
			mustJournal(t, e)
			defer opened.Close()
			r, e := opened.PrepareLoadedArchive(context.Background(), identity)
			allowed := kind == "reopened-original" || kind == "copied-identity"
			if allowed {
				mustJournal(t, e)
				if r.Record().Digest != snap.Digest {
					t.Fatal("retargeted")
				}
			} else if e == nil || r != nil {
				t.Fatal("replacement or changed history was accepted")
			}
			if kind != "changed-bytes" {
				after, e := os.ReadFile(filepath.Join(dir, id+".wal"))
				mustJournal(t, e)
				if !bytes.Equal(before, after) {
					t.Fatal("review mutated original")
				}
			}
		})
	}
}

func TestHelmJournalIdentifiedCorruptionHasNoToken(t *testing.T) {
	s, _ := testStore(t)
	id := archiveHistory(t, s, "corrupt")
	snap, identity, err := s.LoadIdentified(id, false)
	if !errors.Is(err, ErrCorrupt) || identity != nil || !snap.Corrupt || snap.ID != id {
		t.Fatal("corruption lost or became authority", err)
	}
	if r, e := s.PrepareLoadedArchive(context.Background(), identity); e == nil || r != nil {
		t.Fatal("nil identity granted archive")
	}
}
