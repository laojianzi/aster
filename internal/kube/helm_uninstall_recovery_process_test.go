package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

const (
	helmRecoveryProcessMode = "ASTER_HELM_RECOVERY_PROCESS_MODE"
	helmRecoveryProcessDir  = "ASTER_HELM_RECOVERY_PROCESS_DIR"
	helmRecoveryProcessURL  = "ASTER_HELM_RECOVERY_PROCESS_URL"
)

// The only child output is this bounded nonsensitive fixture projection. Offline
// recovery receives no endpoint and never constructs a Backend or a consent.
type helmRecoveryProcessSummary struct {
	ID       string
	Revision helmrelease.Revision
	Targets  []helmrelease.UninstallTarget
	Effects  []HelmUninstallExecutionEffect
	Outcome  HelmUninstallExecutionOutcome
	Complete bool
	Corrupt  bool
}

func TestHelmUninstallRecoveryProcessHelper(t *testing.T) {
	mode := os.Getenv(helmRecoveryProcessMode)
	if mode == "" {
		return
	}
	journal, err := OpenHelmUninstallJournal(os.Getenv(helmRecoveryProcessDir))
	if err != nil {
		t.Fatal("child could not open the dedicated private journal")
	}
	if mode == "recover" {
		if os.Getenv(helmRecoveryProcessURL) != "" {
			t.Fatal("offline recovery was given a network endpoint")
		}
		ids, err := journal.IDs()
		if err != nil || len(ids) != 1 {
			t.Fatal("offline recovery did not find exactly one operation")
		}
		r, err := journal.Recover(ids[0])
		if err != nil || r == nil {
			t.Fatal("offline recovery could not read the checked history")
		}
		summary := helmRecoveryProcessSummary{r.ID(), r.Revision(), r.Targets(), r.Effects(), r.Outcome(), r.Complete(), r.Corrupt()}
		if err := journal.Close(); err != nil {
			t.Fatal("offline recovery could not close its journal")
		}
		if err := json.NewEncoder(os.Stdout).Encode(summary); err != nil {
			t.Fatal("offline recovery could not emit its bounded result")
		}
		os.Exit(0)
	}
	if mode != "execute" {
		t.Fatal("unknown recovery child mode")
	}
	b, err := New(&rest.Config{Host: os.Getenv(helmRecoveryProcessURL), BearerToken: "current",
		RateLimiter: flowcontrol.NewFakeAlwaysRateLimiter()})
	if err != nil {
		t.Fatal("child backend configuration failed")
	}
	ctx := context.Background()
	row := selectedHelmRevision()
	prepared, err := b.PrepareHelmUninstall(ctx, "team", row, "fresh-process", true)
	if err != nil {
		t.Fatal("child fresh preparation failed")
	}
	dryConsent, err := b.AcknowledgeHelmUninstallDryRun(prepared, "fresh-process", "team", row, true)
	if err != nil {
		t.Fatal("child fresh dry-run consent failed")
	}
	dry, err := b.PreflightHelmUninstall(ctx, dryConsent, "fresh-process", "team", row)
	if err != nil {
		t.Fatal("child actual HTTP preflight failed")
	}
	consent, err := b.AcknowledgeHelmUninstallExecution(dry, "fresh-process", "team", row, true, journal)
	if err != nil {
		t.Fatal("child independent execution consent failed")
	}
	_, _ = b.ExecuteHelmUninstall(ctx, consent, "fresh-process", "team", row)
	// Each parent case must forcibly kill this process at a server barrier.
	// A returned executor cannot substitute for a process-crash qualification.
	t.Fatal("executor returned before the parent killed the process")
}

type helmRecoveryProcessOutput struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (b *helmRecoveryProcessOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := min(len(p), (16<<10)-len(b.data))
	b.data = append(b.data, p[:n]...)
	b.truncated = b.truncated || n != len(p)
	return len(p), nil
}

func (b *helmRecoveryProcessOutput) result() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.data), b.truncated
}

func helmRecoveryChild(ctx context.Context, mode, dir, endpoint string, output io.Writer) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelmUninstallRecoveryProcessHelper$", "-test.count=1", "-test.timeout=20s")
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != helmRecoveryProcessMode && key != helmRecoveryProcessDir && key != helmRecoveryProcessURL {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, helmRecoveryProcessMode+"="+mode, helmRecoveryProcessDir+"="+dir)
	if endpoint != "" {
		cmd.Env = append(cmd.Env, helmRecoveryProcessURL+"="+endpoint)
	}
	cmd.Stdout, cmd.Stderr = output, output
	return cmd
}

func TestHelmUninstallRecoveryExecutorProcessCrash(t *testing.T) {
	for _, phase := range []string{"begin", "delete", "finish"} {
		for _, boundary := range []string{"committed-response-blocked", "accepted-observation-blocked"} {
			t.Run(phase+"/"+boundary, func(t *testing.T) {
				f := newHelmExecutionFixture(t, true)
				if err := f.journal.Close(); err != nil {
					t.Fatal("parent did not release the journal before child execution")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
				defer cancel()
				reached, release, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var paused atomic.Bool
				var releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				defer unblock()
				// Both hooks run under the fixture lock. Release it while paused
				// so the parent can kill/join the child without a server deadlock.
				// No testing.Fatal or assertion runs inside a server goroutine.
				pause := func() {
					if !paused.CompareAndSwap(false, true) {
						return
					}
					f.mu.Unlock()
					defer func() {
						f.mu.Lock()
						// The parent receives this while the handler owns f.mu;
						// inspect then waits for the whole response path to finish.
						close(released)
					}()
					close(reached)
					select {
					case <-release:
					case <-ctx.Done():
					}
				}
				f.mu.Lock()
				lastWrite := ""
				f.onWrite = func(kind string) {
					lastWrite = kind
					if kind == phase && boundary == "committed-response-blocked" {
						pause()
					}
				}
				f.onRead = func(string) {
					// A new GET can occur only after the executor validated the
					// preceding response and synchronously stored AcceptedPending.
					if lastWrite == phase && boundary == "accepted-observation-blocked" {
						pause()
					}
				}
				f.mu.Unlock()
				output := &helmRecoveryProcessOutput{}
				child := helmRecoveryChild(ctx, "execute", f.journalDir, f.b.config.Host, output)
				if err := child.Start(); err != nil {
					t.Fatal("execution subprocess did not start")
				}
				joined := make(chan struct{})
				var childErr error
				go func() { childErr = child.Wait(); close(joined) }()
				defer func() {
					_ = child.Process.Kill()
					unblock()
					select {
					case <-joined:
					case <-time.After(3 * time.Second):
						t.Error("execution child did not join during cleanup")
					}
				}()
				select {
				case <-reached:
				case <-joined:
					data, _ := output.result()
					t.Fatalf("child exited before crash boundary: %s", data)
				case <-ctx.Done():
					t.Fatal("execution child did not reach the bounded crash barrier")
				}
				if err := child.Process.Kill(); err != nil {
					t.Fatal("parent did not forcibly kill the execution process")
				}
				select {
				case <-joined:
					if childErr == nil || child.ProcessState == nil || child.ProcessState.Success() {
						t.Fatal("execution process exited normally instead of being killed")
					}
				case <-ctx.Done():
					t.Fatal("killed execution process did not join")
				}
				unblock()
				select {
				case <-released:
				case <-ctx.Done():
					t.Fatal("HTTP barrier did not release after process death")
				}
				if _, truncated := output.result(); truncated {
					t.Fatal("execution child exceeded its output budget")
				}
				requests, events, storage := f.inspect(t)
				wantKinds, deleted, index, status := []string{"begin"}, 0, 0, "uninstalling"
				if phase == "delete" {
					wantKinds, deleted, index = []string{"begin", "delete"}, 1, 1
				} else if phase == "finish" {
					wantKinds, deleted, index, status = []string{"begin", "delete", "delete", "finish"}, 2, 3, "uninstalled"
				}
				assertExecutionKinds(t, requests, wantKinds...)
				if storage.Labels["status"] != status || string(storage.UID) != selectedHelmRevision().UID {
					t.Fatal("server did not persist the expected conditional storage effect")
				}
				var original corev1.Secret
				if json.Unmarshal(f.original, &original) != nil {
					t.Fatal("original server fixture snapshot unavailable")
				}
				if reason := checkExecutionPreservation(original, storage, "begin"); reason != "" {
					t.Fatal("crash changed unrelated retained release data", reason)
				}
				f.mu.Lock()
				for i, name := range f.names {
					if f.deleted[name] != (i < deleted) {
						f.mu.Unlock()
						t.Fatal("server target effects differ from the killed request boundary")
					}
				}
				f.mu.Unlock()

				// A completely new process now loads history without receiving
				// any endpoint. It cannot use the original client's memory or grant.
				recoveryOutput := &helmRecoveryProcessOutput{}
				recovery := helmRecoveryChild(ctx, "recover", f.journalDir, "", recoveryOutput)
				if err := recovery.Run(); err != nil {
					data, _ := recoveryOutput.result()
					t.Fatalf("new offline recovery process failed: %s", data)
				}
				data, truncated := recoveryOutput.result()
				var summary helmRecoveryProcessSummary
				decoder := json.NewDecoder(bytes.NewReader(data))
				decoder.DisallowUnknownFields()
				if truncated || bytes.Contains(data, []byte("PRIVATE")) || bytes.Contains(data, []byte("current")) ||
					decoder.Decode(&summary) != nil || decoder.Decode(new(any)) != io.EOF {
					t.Fatal("offline recovery did not emit a bounded private-safe fixture projection")
				}
				if len(summary.ID) != 32 || summary.Revision != selectedHelmRevision() ||
					!reflect.DeepEqual(summary.Targets, f.p.Targets()) || summary.Complete || summary.Corrupt ||
					summary.Outcome != HelmExecutionUnknown || len(summary.Effects) != 4 {
					t.Fatal("new process invented completion or changed the interrupted history")
				}
				for i, effect := range summary.Effects {
					want := HelmExecutionNotStarted
					if i < index {
						want = HelmExecutionCompleted
					} else if i == index {
						want = HelmExecutionUnknown
						if boundary == "accepted-observation-blocked" {
							want = HelmExecutionAcceptedPending
						}
					}
					if effect.Outcome != want || effect.Namespace != "team" || effect.Name == "" || effect.UID == "" {
						t.Fatal("checked history lost an earlier completion or guessed an interrupted effect", i, effect.Outcome, want)
					}
					if i == index && boundary == "committed-response-blocked" && effect.ObservedResourceVersion != "" ||
						i == index && boundary == "accepted-observation-blocked" && effect.ObservedResourceVersion == "" {
						t.Fatal("journal confused a blocked response with durable accepted-response evidence")
					}
				}
				afterRequests, afterEvents, afterStorage := f.inspect(t)
				if !reflect.DeepEqual(requests, afterRequests) || !reflect.DeepEqual(events, afterEvents) || !reflect.DeepEqual(storage, afterStorage) {
					t.Fatal("offline recovery sent a request, replayed a mutation or changed server state")
				}
			})
		}
	}
}
