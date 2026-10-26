package helmjournal

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHelmInstallJournalProcessHelper(t *testing.T) {
	if os.Getenv("ASTER_INSTALL_JOURNAL_CHILD") != "crash" {
		return
	}
	s, err := Open(os.Getenv("ASTER_HELM_JOURNAL_TEST_DIR"))
	mustJournal(t, err)
	w, err := s.BeginInstall(testInstallPlan())
	mustJournal(t, err)
	stage := os.Getenv("ASTER_HELM_JOURNAL_TEST_STAGE")
	if stage != "start" {
		mustJournal(t, installIntent(w, 0))
		if stage != "secret-intent" {
			mustJournal(t, w.Response(0, AcceptedPending, "actual-0", "10"))
			if stage != "secret-response" {
				mustJournal(t, w.Observe(0, "actual-0", "10"))
				mustJournal(t, installIntent(w, 1))
				if stage != "target-intent" {
					mustJournal(t, w.Response(1, AcceptedPending, "actual-1", "11"))
					if stage != "target-response" {
						mustJournal(t, w.Observe(1, "actual-1", "11"))
						installStep(t, w, 2)
						mustJournal(t, installIntent(w, 3))
						if stage != "deploy-intent" {
							mustJournal(t, w.Response(3, AcceptedPending, "actual-0", "13"))
							if stage != "deploy-response" {
								mustJournal(t, w.Observe(3, "actual-0", "13"))
								mustJournal(t, w.Finish(time.Now()))
							}
						}
					}
				}
			}
		}
	}
	fmt.Fprintln(os.Stdout, "install-ready:"+w.ID())
	<-time.After(45 * time.Second)
	t.Fatal("parent did not terminate fixture at persistence barrier")
}

func startInstallChild(t *testing.T, dir, stage string) (*exec.Cmd, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelmInstallJournalProcessHelper$", "-test.count=1", "-test.timeout=50s")
	cmd.Env = append(os.Environ(), "ASTER_INSTALL_JOURNAL_CHILD=crash", "ASTER_HELM_JOURNAL_TEST_DIR="+dir, "ASTER_HELM_JOURNAL_TEST_STAGE="+stage)
	out, err := cmd.StdoutPipe()
	mustJournal(t, err)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	mustJournal(t, cmd.Start())
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	ready := make(chan string, 1)
	go func() {
		scan := bufio.NewScanner(out)
		scan.Buffer(make([]byte, 256), 4096)
		if scan.Scan() {
			ready <- scan.Text()
		} else {
			ready <- ""
		}
	}()
	select {
	case line := <-ready:
		id, ok := strings.CutPrefix(line, "install-ready:")
		if !ok || !validHex(id, 32) {
			t.Fatal("child did not reach install persistence barrier")
		}
		return cmd, id
	case <-ctx.Done():
		t.Fatal("bounded child fixture expired")
	}
	return nil, ""
}

func TestHelmInstallJournalProcessCrashRecovery(t *testing.T) {
	cases := []struct {
		name   string
		want   []string
		closed bool
	}{
		{"start", []string{NotStarted, NotStarted, NotStarted, NotStarted}, false},
		{"secret-intent", []string{Unknown, NotStarted, NotStarted, NotStarted}, false},
		{"secret-response", []string{AcceptedPending, NotStarted, NotStarted, NotStarted}, false},
		{"target-intent", []string{Completed, Unknown, NotStarted, NotStarted}, false},
		{"target-response", []string{Completed, AcceptedPending, NotStarted, NotStarted}, false},
		{"deploy-intent", []string{Completed, Completed, Completed, Unknown}, false},
		{"deploy-response", []string{Completed, Completed, Completed, AcceptedPending}, false},
		{"complete", []string{Completed, Completed, Completed, Completed}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "journal")
			cmd, id := startInstallChild(t, dir, tc.name)
			if other, err := Open(dir); !errors.Is(err, ErrBusy) {
				if other != nil {
					_ = other.Close()
				}
				t.Fatal("legacy opener bypassed held install lock")
			}
			before, err := os.ReadFile(filepath.Join(dir, id+".wal"))
			mustJournal(t, err)
			killJournalChild(t, cmd)
			// Existing helper reads raw snapshots in a separate process with no writer.
			snap := readInFreshProcess(t, dir, id)
			if snap.Version != 2 || snap.Action != "install" || snap.Corrupt || snap.Closed != tc.closed || len(snap.Effects) != 4 {
				t.Fatal("lost typed checked history after actual kill")
			}
			for i, want := range tc.want {
				if snap.Effects[i].Outcome != want {
					t.Fatalf("effect %d: %s != %s", i, snap.Effects[i].Outcome, want)
				}
			}
			if tc.name == "secret-intent" && snap.Effects[0].UID != "" {
				t.Fatal("recovery invented pre-create UID")
			}
			after, err := os.ReadFile(filepath.Join(dir, id+".wal"))
			mustJournal(t, err)
			if !bytes.Equal(before, after) {
				t.Fatal("fresh-process recovery repaired or replayed history")
			}
			s, err := OpenExisting(dir)
			mustJournal(t, err)
			defer s.Close()
			if !tc.closed {
				if w, err := s.BeginInstall(testInstallPlan()); !errors.Is(err, ErrUnresolved) {
					if w != nil {
						_ = w.Abort()
					}
					t.Fatal("abruptly terminated operation lost admission guard")
				}
			}
		})
	}
}
