package helmjournal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const archiveChildEnv = "ASTER_HELM_ARCHIVE_TEST_CHILD"

// This ordinary test passes without a child marker. Child process output is
// consumed privately and never injected into the parent's JSONL census.
func TestHelmJournalArchiveProcessHelper(t *testing.T) {
	stage := os.Getenv(archiveChildEnv)
	if stage == "" {
		return
	}
	dir := os.Getenv("ASTER_HELM_ARCHIVE_TEST_DIR")
	s, err := Open(dir)
	mustJournal(t, err)
	if stage == "read" {
		defer s.Close()
		id := os.Getenv("ASTER_HELM_ARCHIVE_TEST_ID")
		active, err := s.List()
		mustJournal(t, err)
		archived, err := s.ListArchived()
		mustJournal(t, err)
		if len(active)+len(archived) != 1 {
			t.Fatal("crash left zero or duplicate histories")
		}
		name := id + ".wal"
		if len(archived) == 1 {
			name = id + archivedSuffix
			_, err = s.LoadArchived(id)
		} else {
			_, err = s.Load(id)
		}
		mustJournal(t, err)
		data, err := os.ReadFile(filepath.Join(dir, name))
		mustJournal(t, err)
		fmt.Fprintln(os.Stdout, "archive-bytes:"+base64.StdEncoding.EncodeToString(data))
		return
	}
	id := archiveHistory(t, s, "completed")
	grant := archiveGrant(t, s, id)
	barrier := func() {
		fmt.Fprintln(os.Stdout, "archive-ready:"+id)
		<-time.After(40 * time.Second)
		t.Fatal("parent did not terminate archive child")
	}
	s.renameArchive = func(old, new string) error {
		if stage == "before-rename" {
			barrier()
		}
		if err := s.root.renameNoReplace(old, new); err != nil {
			return err
		}
		if stage == "after-rename" {
			barrier()
		}
		return nil
	}
	result, err := s.Archive(context.Background(), grant)
	mustJournal(t, err)
	if result.Outcome != ArchiveCompleted {
		t.Fatal("archive not observed")
	}
	barrier()
}

func TestHelmJournalArchiveProcessCrash(t *testing.T) {
	for _, stage := range []string{"before-rename", "after-rename", "after-verification"} {
		t.Run(stage, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "journal")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelmJournalArchiveProcessHelper$", "-test.count=1", "-test.timeout=45s")
			cmd.Env = append(os.Environ(), archiveChildEnv+"="+stage, "ASTER_HELM_ARCHIVE_TEST_DIR="+dir)
			pipe, err := cmd.StdoutPipe()
			mustJournal(t, err)
			mustJournal(t, cmd.Start())
			defer func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			ready := make(chan string, 1)
			go func() {
				scan := bufio.NewScanner(pipe)
				if scan.Scan() {
					ready <- scan.Text()
				} else {
					ready <- ""
				}
			}()
			var line string
			select {
			case line = <-ready:
			case <-ctx.Done():
				t.Fatal("archive barrier timed out")
			}
			id, ok := strings.CutPrefix(line, "archive-ready:")
			if !ok || !validHex(id, 32) {
				t.Fatal("child failed before real rename barrier")
			}
			name := id + ".wal"
			if stage != "before-rename" {
				name = id + archivedSuffix
			}
			original, err := os.ReadFile(filepath.Join(dir, name))
			mustJournal(t, err)
			// The permanent lock is still owned while the process is at the barrier.
			if other, err := Open(dir); err != ErrBusy {
				if other != nil {
					_ = other.Close()
				}
				t.Fatal("archive lost OS lock")
			}
			killJournalChild(t, cmd)
			read := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelmJournalArchiveProcessHelper$", "-test.count=1", "-test.timeout=15s")
			read.Env = append(os.Environ(), archiveChildEnv+"=read", "ASTER_HELM_ARCHIVE_TEST_DIR="+dir, "ASTER_HELM_ARCHIVE_TEST_ID="+id)
			out, err := read.Output()
			mustJournal(t, err)
			found := false
			for _, line := range strings.Split(string(out), "\n") {
				if payload, ok := strings.CutPrefix(line, "archive-bytes:"); ok {
					data, err := base64.StdEncoding.DecodeString(payload)
					mustJournal(t, err)
					if !bytes.Equal(data, original) {
						t.Fatal("crash recovery changed history")
					}
					found = true
				}
			}
			if !found {
				t.Fatal("fresh process did not verify original WAL")
			}
			entries, err := os.ReadDir(dir)
			mustJournal(t, err)
			if len(entries) != 2 {
				t.Fatal("crash left temporary file, duplicate or missing record")
			}
		})
	}
}
