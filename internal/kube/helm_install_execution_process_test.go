package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

const installExecutionChildMode = "ASTER_INSTALL_EXECUTION_CHILD"

func TestHelmInstallExecutionProcessHelper(t *testing.T) {
	if os.Getenv(installExecutionChildMode) == "" {
		return
	}
	b, err := New(&rest.Config{Host: os.Getenv("ASTER_INSTALL_EXECUTION_URL"), BearerToken: "current", RateLimiter: flowcontrol.NewFakeAlwaysRateLimiter()})
	if err != nil {
		t.Fatal(err)
	}
	j, err := OpenHelmUninstallJournal(os.Getenv("ASTER_INSTALL_EXECUTION_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	ctx := context.Background()
	c := installHTTPCandidate(t, time.Now())
	p, err := b.PrepareHelmInstall(ctx, c, "process", true)
	if err != nil {
		t.Fatal(err)
	}
	d, err := b.AcknowledgeHelmInstallDryRun(ctx, p, c, "process", true)
	if err != nil {
		t.Fatal(err)
	}
	pre, err := b.PreflightHelmInstall(ctx, d, c, "process")
	if err != nil {
		t.Fatal(err)
	}
	g, err := b.AcknowledgeHelmInstallExecution(ctx, pre, c, "process", true, j)
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.ExecuteHelmInstall(ctx, g, c, "process")
	t.Fatal("child returned before kill barrier", err)
}
func TestHelmInstallExecutionProcessCrash(t *testing.T) {
	for _, phase := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(phase), func(t *testing.T) {
			reached, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			f := &installExecutionFixture{objects: map[string][]byte{}, skipLiveWAL: true, onWrite: func(n int) {
				if n == phase {
					close(reached)
					<-release
				}
			}}
			srv := httptest.NewServer(http.HandlerFunc(f.handler))
			defer srv.Close()
			defer unblock()
			dir := filepath.Join(t.TempDir(), "journal")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelmInstallExecutionProcessHelper$", "-test.count=1", "-test.timeout=12s")
			for _, v := range os.Environ() {
				if !strings.HasPrefix(v, "ASTER_INSTALL_EXECUTION_") {
					cmd.Env = append(cmd.Env, v)
				}
			}
			cmd.Env = append(cmd.Env, installExecutionChildMode+"=1", "ASTER_INSTALL_EXECUTION_URL="+srv.URL+"/prefix", "ASTER_INSTALL_EXECUTION_DIR="+dir)
			output := &helmRecoveryProcessOutput{}
			cmd.Stdout, cmd.Stderr = output, output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-reached:
			case err := <-done:
				raw, _ := output.result()
				t.Fatal("child exited before barrier", err, string(raw))
			case <-ctx.Done():
				_ = cmd.Process.Kill()
				<-done
				t.Fatal("barrier timeout")
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err == nil {
				t.Fatal("not a process kill")
			}
			unblock()
			j, err := OpenHelmUninstallJournal(dir)
			if err != nil {
				t.Fatal("lock did not release after process death", err)
			}
			defer j.Close()
			ids, err := j.IDs()
			if err != nil || len(ids) != 1 {
				t.Fatal("missing actual journal", err)
			}
			recovery, err := j.store.RecoverInstall(ids[0], false)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := recovery.Snapshot()
			if snapshot.Closed || snapshot.Effects[phase-1].Outcome != helmjournal.Unknown {
				t.Fatal("crash did not preserve unacknowledged intent")
			}
			for i := 0; i < phase-1; i++ {
				if snapshot.Effects[i].Outcome != helmjournal.Completed {
					t.Fatal("lost completed prefix")
				}
			}
			for i := phase; i < len(snapshot.Effects); i++ {
				if snapshot.Effects[i].Outcome != helmjournal.NotStarted {
					t.Fatal("dispatched past barrier")
				}
			}
			before := f.calls.Load()
			copy := recovery.Snapshot()
			copy.Effects[0].Outcome = "forged"
			_, _ = json.Marshal(recovery.Snapshot())
			if f.calls.Load() != before || int(f.writes.Load()) != phase || f.bad.Load() {
				t.Fatal("recovery sent network or bad request")
			}
			if _, err = j.store.BeginInstall(*snapshot.Install); err != helmjournal.ErrUnresolved {
				t.Fatal("unknown could be restarted", err)
			}
		})
	}
}
