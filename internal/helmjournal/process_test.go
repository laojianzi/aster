package helmjournal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const childModeEnv = "ASTER_HELM_JOURNAL_TEST_CHILD"

// This ordinary root passes without a child marker; it never skips a required
// platform test. Child stdout is privately consumed and never copied into the
// parent's JSONL stream as if it were another top-level test execution.
func TestHelmJournalProcessHelper(t *testing.T) {
	mode := os.Getenv(childModeEnv)
	if mode == "" {
		return
	}
	dir := os.Getenv("ASTER_HELM_JOURNAL_TEST_DIR")
	s, err := Open(dir)
	mustJournal(t, err)
	if mode == "read" {
		defer s.Close()
		snapshot, err := s.Load(os.Getenv("ASTER_HELM_JOURNAL_TEST_ID"))
		mustJournal(t, err)
		data, err := json.Marshal(snapshot)
		mustJournal(t, err)
		fmt.Fprintln(os.Stdout, "journal-snapshot:"+base64.StdEncoding.EncodeToString(data))
		return
	}
	if mode == "probe" {
		mustJournal(t, s.Close())
		fmt.Fprintln(os.Stdout, "journal-probe:ok")
		return
	}
	if mode == "hold" {
		fmt.Fprintln(os.Stdout, "journal-ready:lock")
		<-time.After(45 * time.Second)
		t.Fatal("parent did not terminate the isolated lock holder")
	}
	w, err := s.Begin(testPlan())
	mustJournal(t, err)
	stage := os.Getenv("ASTER_HELM_JOURNAL_TEST_STAGE")
	if stage != "start" {
		mustJournal(t, w.Intent(0, "7"))
		if stage != "begin-intent" {
			mustJournal(t, w.Record(0, AcceptedPending, "8"))
			if stage != "begin-ack" {
				mustJournal(t, w.Record(0, Completed, "8"))
				mustJournal(t, w.Intent(1, "21"))
				if stage != "delete-intent" {
					mustJournal(t, w.Record(1, AcceptedPending, ""))
					if stage != "delete-ack" {
						mustJournal(t, w.Record(1, Completed, ""))
						mustJournal(t, w.Intent(2, "22"))
						mustJournal(t, w.Record(2, AcceptedPending, ""))
						mustJournal(t, w.Record(2, Completed, ""))
						mustJournal(t, w.Intent(3, "8"))
						if stage != "finish-intent" {
							mustJournal(t, w.Record(3, AcceptedPending, "9"))
							if stage != "finish-ack" {
								mustJournal(t, w.Record(3, Completed, "9"))
								mustJournal(t, w.Finish(time.Now()))
							}
						}
					}
				}
			}
		}
	}
	fmt.Fprintln(os.Stdout, "journal-ready:"+w.ID())
	<-time.After(45 * time.Second)
	t.Fatal("parent did not terminate the isolated execution holder")
}

func childCommand(ctx context.Context, dir, mode, stage, id string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelmJournalProcessHelper$", "-test.count=1", "-test.timeout=50s")
	cmd.Env = append(os.Environ(), childModeEnv+"="+mode, "ASTER_HELM_JOURNAL_TEST_DIR="+dir, "ASTER_HELM_JOURNAL_TEST_STAGE="+stage, "ASTER_HELM_JOURNAL_TEST_ID="+id)
	return cmd
}

func startJournalChild(t *testing.T, dir, mode, stage string) (*exec.Cmd, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	cmd := childCommand(ctx, dir, mode, stage, "")
	pipe, err := cmd.StdoutPipe()
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
		scanner := bufio.NewScanner(pipe)
		scanner.Buffer(make([]byte, 256), 4096)
		if scanner.Scan() {
			ready <- scanner.Text()
		} else {
			ready <- ""
		}
	}()
	select {
	case line := <-ready:
		if !strings.HasPrefix(line, "journal-ready:") {
			t.Fatal("isolated journal process failed before its persistence barrier")
		}
		return cmd, strings.TrimPrefix(line, "journal-ready:")
	case <-ctx.Done():
		t.Fatal("isolated journal process did not reach its bounded persistence barrier")
	}
	return nil, ""
}

func killJournalChild(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	mustJournal(t, cmd.Process.Kill())
	if err := cmd.Wait(); err == nil {
		t.Fatal("process fixture did not terminate abruptly")
	}
}

func readInFreshProcess(t *testing.T, dir, id string) Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := childCommand(ctx, dir, "read", "", id)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal("fresh process could not open and read the original journal")
	}
	if len(out) > 128<<10 {
		t.Fatal("unbounded journal helper output")
	}
	for _, line := range strings.Split(string(out), "\n") {
		if payload, ok := strings.CutPrefix(line, "journal-snapshot:"); ok {
			data, err := base64.StdEncoding.DecodeString(payload)
			mustJournal(t, err)
			var s Snapshot
			mustJournal(t, json.Unmarshal(data, &s))
			return s
		}
	}
	t.Fatal("fresh process did not return a recovery projection")
	return Snapshot{}
}

func TestHelmJournalProcessCrashRecovery(t *testing.T) {
	stages := []struct {
		name     string
		outcomes []string
		closed   bool
	}{
		{"start", []string{NotStarted, NotStarted, NotStarted, NotStarted}, false},
		{"begin-intent", []string{Unknown, NotStarted, NotStarted, NotStarted}, false},
		{"begin-ack", []string{AcceptedPending, NotStarted, NotStarted, NotStarted}, false},
		{"delete-intent", []string{Completed, Unknown, NotStarted, NotStarted}, false},
		{"delete-ack", []string{Completed, AcceptedPending, NotStarted, NotStarted}, false},
		{"finish-intent", []string{Completed, Completed, Completed, Unknown}, false},
		{"finish-ack", []string{Completed, Completed, Completed, AcceptedPending}, false},
		{"complete", []string{Completed, Completed, Completed, Completed}, true},
	}
	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "journal")
			cmd, id := startJournalChild(t, dir, "crash", stage.name)
			if !validHex(id, 32) {
				t.Fatal("child did not return a canonical operation identity")
			}
			original, err := os.ReadFile(filepath.Join(dir, id+".wal"))
			mustJournal(t, err)
			killJournalChild(t, cmd)
			s := readInFreshProcess(t, dir, id)
			if s.Corrupt || s.ID != id || s.Closed != stage.closed || len(s.Effects) != len(stage.outcomes) || !validHex(s.Digest, 64) {
				t.Fatal("fresh process lost the checked execution history")
			}
			for i, want := range stage.outcomes {
				if s.Effects[i].Outcome != want {
					t.Fatalf("effect %d: got %s, want %s", i, s.Effects[i].Outcome, want)
				}
			}
			after, err := os.ReadFile(filepath.Join(dir, id+".wal"))
			mustJournal(t, err)
			if !bytes.Equal(original, after) {
				t.Fatal("recovery altered or repaired the original WAL")
			}
			entries, err := os.ReadDir(dir)
			mustJournal(t, err)
			if len(entries) != 2 {
				t.Fatal("recovery created a new operation or dropped the lockfile")
			}
		})
	}
}

func TestHelmJournalProcessLockExclusion(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "journal")
	cmd, marker := startJournalChild(t, dir, "hold", "")
	if marker != "lock" {
		t.Fatal("isolated process did not acquire the expected lock")
	}
	before, err := os.Stat(filepath.Join(dir, lockName))
	mustJournal(t, err)
	if other, err := Open(dir); !errors.Is(err, ErrBusy) {
		if other != nil {
			other.Close()
		}
		t.Fatal("two processes acquired the same journal")
	}
	killJournalChild(t, cmd)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := childCommand(ctx, dir, "probe", "", "").Output()
	if err != nil || !bytes.Contains(out, []byte("journal-probe:ok")) {
		t.Fatal("new process could not acquire the OS-released lock")
	}
	after, err := os.Stat(filepath.Join(dir, lockName))
	mustJournal(t, err)
	if !os.SameFile(before, after) {
		t.Fatal("lock recovery deleted/replaced the permanent lockfile")
	}
	entries, err := os.ReadDir(dir)
	mustJournal(t, err)
	if len(entries) != 1 {
		t.Fatal("lock recovery left a PID marker or new operation")
	}
}
