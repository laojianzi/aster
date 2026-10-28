package credentialexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type helperDiagnosticWire struct {
	Schema        int    `json:"schema"`
	Outcome       string `json:"outcome"`
	ParentContext string `json:"parent_context"`
	HelperContext string `json:"helper_context"`
	ElapsedNS     int64  `json:"elapsed_ns"`
	FailedStage   string `json:"failed_stage"`
	Phases        []struct {
		Phase     string `json:"phase"`
		State     string `json:"state"`
		Result    string `json:"result"`
		ElapsedNS int64  `json:"elapsed_ns"`
	} `json:"phases"`
	Truncated bool `json:"truncated"`
}

type helperDiagnosticErrorContext struct {
	context.Context
	err error
}

func (c helperDiagnosticErrorContext) Err() error { return c.err }

func readHelperDiagnostic(t *testing.T, d HelperDiagnostics, private ...string) helperDiagnosticWire {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil || len(b) > 2048 || string(b) != d.Summary() {
		t.Fatal("helper diagnostic is not a bounded consistent value")
	}
	for _, value := range private {
		if value != "" && bytes.Contains(b, []byte(value)) {
			t.Fatal("helper diagnostic retained private input or output")
		}
	}
	var wire helperDiagnosticWire
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil || wire.Schema != 1 || wire.Truncated || wire.ElapsedNS < 0 || len(wire.Phases) != 7 {
		t.Fatal("helper diagnostic has an invalid schema or duration")
	}
	for i, name := range []string{"job-setup", "start", "assign", "resume", "wait", "reclaim", "release"} {
		phase := wire.Phases[i]
		if phase.Phase != name || phase.ElapsedNS < 0 {
			t.Fatal("helper diagnostic lost a fixed stage or monotonic duration")
		}
		switch phase.State {
		case "not-entered":
			if phase.Result != "not-entered" || phase.ElapsedNS != 0 {
				t.Fatal("unentered helper stage resembles an executed operation")
			}
		case "completed":
			if phase.Result != "ok" && phase.Result != "already-gone" {
				t.Fatal("completed helper stage has an invalid category")
			}
		case "failed":
			switch phase.Result {
			case "canceled", "deadline", "output-limit", "exit-failed", "wait-delay", "os-error":
			default:
				t.Fatal("failed helper stage has an invalid category")
			}
		default:
			t.Fatal("helper stage has an unbounded state")
		}
	}
	return wire
}

func helperDiagnosticEnvironment(mode string) []string {
	env := []string{"ASTER_RENDER_HELPER_TEST=" + mode, "ASTER_DIAGNOSTIC_PRIVATE=PRIVATE_HELPER_ENV"}
	for _, key := range []string{"SystemRoot", "WINDIR"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func waitHelperDiagnosticReady(t *testing.T, marker string, done <-chan struct{}) {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if data, err := os.ReadFile(marker); err == nil && string(data) == "started" {
			return
		}
		select {
		case <-done:
			t.Fatal("diagnostic helper exited before its real readiness marker")
		case <-deadline.C:
			t.Fatal("diagnostic helper did not publish its real readiness marker")
		case <-tick.C:
		}
	}
}

func TestRenderHelperDiagnostics(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	type testCase struct {
		name, mode, outcome string
		wantErr             error
	}
	cases := []testCase{
		{"success", "stdin", "success", nil},
		{"nonzero", "failure", "command-failed", ErrCommandFailed},
		{"start-failure", "stdin", "command-failed", ErrCommandFailed},
		{"parent-cancel", "sleep", "parent-canceled", context.Canceled},
		// Preserve the old early-start error identity, while making the already
		// expired parent visible separately. No child may start in this case.
		{"parent-deadline", "sleep", "command-failed", ErrCommandFailed},
		{"helper-timeout", "sleep", "helper-timeout", ErrTimeout},
		{"stdout-limit", "stdout", "output-limit", ErrOutputLimit},
		{"stderr-limit", "stderr", "output-limit", ErrOutputLimit},
		{"nil-context", "stdin", "invalid-request", nil},
		{"invalid-request", "stdin", "invalid-request", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "PRIVATE_HELPER_PATH")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			exe := executable
			args := []string{"-test.run=^TestRenderHelperFixture$", "--", "PRIVATE_HELPER_ARG"}
			env := helperDiagnosticEnvironment(tc.mode)
			input := bytes.Repeat([]byte("x"), 256<<10)
			copy(input, "PRIVATE_HELPER_PAYLOAD")
			ctx := context.Background()
			cancel := func() {}
			if tc.name == "nil-context" {
				ctx = nil
			}
			if tc.name == "invalid-request" {
				exe = "PRIVATE_HELPER_RELATIVE_EXECUTABLE"
			}
			if tc.name == "start-failure" {
				exe = filepath.Join(dir, "PRIVATE_HELPER_MISSING_EXECUTABLE.exe")
			}
			if tc.name == "parent-deadline" {
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			}
			defer func() { cancel() }()
			var out []byte
			var diagnostic HelperDiagnostics
			var runErr error
			if tc.name == "parent-cancel" {
				ctx, cancel = context.WithCancel(context.Background())
				marker := filepath.Join(dir, "PRIVATE_HELPER_READY")
				args[0] = "-test.run=^TestCredentialFixture$"
				env = append(env, "ASTER_CREDENTIAL_FIXTURE=sleep", "ASTER_FIXTURE_STARTED="+marker)
				done := make(chan struct{})
				go func() {
					out, diagnostic, runErr = RunRenderHelperWithDiagnostics(ctx, exe, args, env, dir, input)
					close(done)
				}()
				t.Cleanup(func() { cancel(); <-done })
				waitHelperDiagnosticReady(t, marker, done)
				cancel()
				<-done
			} else {
				out, diagnostic, runErr = RunRenderHelperWithDiagnostics(ctx, exe, args, env, dir, input)
			}
			wire := readHelperDiagnostic(t, diagnostic, "PRIVATE", executable, dir)
			if wire.Outcome != tc.outcome {
				t.Fatalf("wrong diagnostic terminal category: got %s, want %s", wire.Outcome, tc.outcome)
			}
			if tc.outcome == "invalid-request" {
				if runErr == nil || runErr.Error() != "invalid render helper request" || out != nil || wire.HelperContext != "not-provided" {
					t.Fatal("invalid request changed its public result")
				}
				for _, phase := range wire.Phases {
					if phase.State != "not-entered" {
						t.Fatal("invalid request entered a process stage")
					}
				}
				if tc.name == "nil-context" && wire.ParentContext != "not-provided" {
					t.Fatal("nil parent was misreported as cancellation")
				}
				return
			}
			if runErr != tc.wantErr {
				t.Fatalf("helper changed its exact public error identity: %v", runErr)
			}
			if tc.name == "success" {
				if string(out) != "ok" || wire.ParentContext != "active" || wire.HelperContext != "active" || wire.FailedStage != "none" {
					t.Fatal("success was altered or own deferred cancel polluted diagnostics")
				}
			} else if out != nil {
				t.Fatal("failed helper returned output")
			}
			if tc.name == "parent-deadline" {
				if wire.ParentContext != "deadline" || wire.HelperContext != "deadline" || wire.Phases[1].State != "not-entered" || wire.Phases[4].State != "not-entered" {
					t.Fatal("early parent deadline was not observed before process start")
				}
				return
			}
			if tc.name == "start-failure" {
				if wire.FailedStage != "start" || wire.Phases[1].State != "failed" || wire.Phases[4].State != "not-entered" {
					t.Fatal("failed start acquired a fictitious wait stage")
				}
				return
			}
			if wire.Phases[1].State != "completed" || wire.Phases[4].State == "not-entered" || wire.Phases[5].State == "not-entered" {
				t.Fatal("real helper omitted start, wait, or reclamation")
			}
			if runtime.GOOS == "windows" {
				for _, index := range []int{0, 2, 3, 6} {
					if wire.Phases[index].State != "completed" {
						t.Fatal("Windows helper omitted actual containment or release")
					}
				}
			} else {
				for _, index := range []int{0, 2, 3, 6} {
					if wire.Phases[index].State != "not-entered" {
						t.Fatal("non-Windows helper invented a Windows-only operation")
					}
				}
			}
			switch tc.name {
			case "parent-cancel":
				if wire.ParentContext != "canceled" || wire.HelperContext != "canceled" {
					t.Fatal("joined cancellation was not observed")
				}
			case "helper-timeout":
				if wire.ParentContext != "active" || wire.HelperContext != "deadline" || time.Duration(wire.ElapsedNS) < 10*time.Second {
					t.Fatal("the real fixed ten-second helper timeout was not observed")
				}
			case "stdout-limit", "stderr-limit":
				if wire.ParentContext != "active" || wire.HelperContext != "canceled" {
					t.Fatal("output-limit cancellation was confused with a parent deadline")
				}
			}
		})
	}
}

func TestRenderHelperDiagnosticsPrivacyAndBounds(t *testing.T) {
	trace := newHelperTrace()
	trace.recordError(helperStart, trace.begin(), errors.New("PRIVATE_OS_ERROR_WITH_PATH_AND_ARGUMENT"))
	trace.recordError(helperReclaim, trace.begin(), errors.New("PRIVATE_RECLAIM_ERROR"))
	trace.recordError(helperRelease, trace.begin(), errors.New("PRIVATE_RELEASE_ERROR"))
	trace.finishResult(context.Background(), context.Background(), ErrCommandFailed)
	value := trace.snapshot()
	wire := readHelperDiagnostic(t, value, "PRIVATE")
	if wire.Outcome != "command-failed" || wire.FailedStage != "start" || wire.Phases[5].State != "failed" || wire.Phases[6].State != "failed" {
		t.Fatal("cleanup observations replaced the execution failure")
	}
	// Every representable duration and private enum still formats through the
	// fixed schema; none can turn a string from the request into a JSON field.
	value.elapsed = time.Duration(math.MaxInt64)
	for i := range value.phases {
		value.phases[i] = helperPhaseObservation{helperPhaseOutputLimit, time.Duration(math.MaxInt64)}
	}
	readHelperDiagnostic(t, value, "PRIVATE")
	value.elapsed = -1
	for i := range value.phases {
		value.phases[i] = helperPhaseObservation{helperPhaseResult(255), -1}
	}
	value.outcome, value.parentContext, value.helperContext, value.failedStage = 255, 255, 255, 255
	wire = readHelperDiagnostic(t, value, "PRIVATE")
	if wire.ElapsedNS != 0 || wire.FailedStage != "none" || wire.Outcome != "not-run" {
		t.Fatal("invalid private enum or duration escaped its fixed representation")
	}
	zero := readHelperDiagnostic(t, HelperDiagnostics{})
	if zero.Outcome != "not-run" || zero.ParentContext != "not-provided" || zero.HelperContext != "not-provided" {
		t.Fatal("zero diagnostic invented an invocation")
	}
	privateError := errors.New("PRIVATE_NONSTANDARD_CONTEXT_ERROR")
	unknown := helperDiagnosticErrorContext{context.Background(), privateError}
	trace = newHelperTrace()
	trace.finishResult(unknown, unknown, privateError)
	wire = readHelperDiagnostic(t, trace.snapshot(), "PRIVATE")
	if wire.Outcome != "other-error" || wire.ParentContext != "other-error" || wire.HelperContext != "other-error" {
		t.Fatal("a nonstandard context error was falsely reported as active or as a known failure")
	}
}

func TestRenderHelperDiagnosticsCancellationRace(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		out        []byte
		diagnostic HelperDiagnostics
		err        error
	}
	const count = 4
	results := make([]result, count)
	cancels := make([]context.CancelFunc, count)
	markers := make([]string, count)
	done := make([]chan struct{}, count)
	var workers sync.WaitGroup
	t.Cleanup(func() {
		for _, cancel := range cancels {
			if cancel != nil {
				cancel()
			}
		}
		workers.Wait()
	})
	for i := range results {
		dir := t.TempDir()
		markers[i] = filepath.Join(dir, "PRIVATE_HELPER_READY")
		ctx, cancel := context.WithCancel(context.Background())
		cancels[i], done[i] = cancel, make(chan struct{})
		env := append(helperDiagnosticEnvironment(""), "ASTER_CREDENTIAL_FIXTURE=sleep", "ASTER_FIXTURE_STARTED="+markers[i])
		workers.Add(1)
		go func() {
			defer workers.Done()
			results[i].out, results[i].diagnostic, results[i].err = RunRenderHelperWithDiagnostics(ctx, executable,
				[]string{"-test.run=^TestCredentialFixture$", "--", "PRIVATE_HELPER_ARG"}, env, dir, []byte("PRIVATE_HELPER_PAYLOAD"))
			close(done[i])
		}()
	}
	for i := range results {
		waitHelperDiagnosticReady(t, markers[i], done[i])
	}
	var cancellation sync.WaitGroup
	for _, cancel := range cancels {
		cancellation.Go(cancel)
	}
	cancellation.Wait()
	workers.Wait()
	for _, result := range results {
		if result.err != context.Canceled || result.out != nil {
			t.Fatal("concurrent real helper cancellation changed error identity", result.err)
		}
		wire := readHelperDiagnostic(t, result.diagnostic, "PRIVATE", executable)
		if wire.Outcome != "parent-canceled" || wire.ParentContext != "canceled" || wire.HelperContext != "canceled" || wire.Phases[5].State == "not-entered" {
			t.Fatal("diagnostic returned before cancellation reclamation was joined")
		}
		before := result.diagnostic.Summary()
		var readers sync.WaitGroup
		for range 8 {
			readers.Go(func() {
				if result.diagnostic.Summary() != before || strings.Contains(result.diagnostic.Summary(), "PRIVATE") {
					t.Error("returned diagnostic was mutable or contained private data")
				}
			})
		}
		readers.Wait()
	}
}
