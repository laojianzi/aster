package credentialexec

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"sync"
	"time"
)

// HelperDiagnostics is an immutable, bounded observation of one render helper
// call. It contains only fixed categories and monotonic durations, never the
// executable, arguments, environment, input, output, or underlying error text.
// Context states are observations at return, not a claim about what caused an
// operating-system operation to fail. Cleanup observations do not change the
// helper's result or replace its first observed failing execution stage.
type HelperDiagnostics struct {
	outcome       helperOutcome
	parentContext helperContextState
	helperContext helperContextState
	elapsed       time.Duration
	failed        bool
	failedStage   helperPhase
	phases        [helperPhaseCount]helperPhaseObservation
}

type helperOutcome uint8

const (
	helperNotRun helperOutcome = iota
	helperSuccess
	helperInvalidRequest
	helperCommandFailed
	helperParentCanceled
	helperParentDeadline
	helperTimeout
	helperOutputLimit
	helperOtherError
)

func (o helperOutcome) label() string {
	switch o {
	case helperSuccess:
		return "success"
	case helperInvalidRequest:
		return "invalid-request"
	case helperCommandFailed:
		return "command-failed"
	case helperParentCanceled:
		return "parent-canceled"
	case helperParentDeadline:
		return "parent-deadline"
	case helperTimeout:
		return "helper-timeout"
	case helperOutputLimit:
		return "output-limit"
	case helperOtherError:
		return "other-error"
	default:
		return "not-run"
	}
}

type helperContextState uint8

const (
	helperContextAbsent helperContextState = iota
	helperContextActive
	helperContextCanceled
	helperContextDeadline
	helperContextOtherError
)

func (s helperContextState) label() string {
	switch s {
	case helperContextActive:
		return "active"
	case helperContextCanceled:
		return "canceled"
	case helperContextDeadline:
		return "deadline"
	case helperContextOtherError:
		return "other-error"
	default:
		return "not-provided"
	}
}

func observeHelperContext(ctx context.Context) helperContextState {
	if ctx == nil {
		return helperContextAbsent
	}
	switch ctx.Err() {
	case nil:
		return helperContextActive
	case context.Canceled:
		return helperContextCanceled
	case context.DeadlineExceeded:
		return helperContextDeadline
	default:
		return helperContextOtherError
	}
}

type helperPhase uint8

const (
	helperJobSetup helperPhase = iota
	helperStart
	helperAssign
	helperResume
	helperWait
	helperReclaim
	helperRelease
	helperPhaseCount
)

func (p helperPhase) label() string {
	switch p {
	case helperJobSetup:
		return "job-setup"
	case helperStart:
		return "start"
	case helperAssign:
		return "assign"
	case helperResume:
		return "resume"
	case helperWait:
		return "wait"
	case helperReclaim:
		return "reclaim"
	case helperRelease:
		return "release"
	default:
		return "none"
	}
}

type helperPhaseResult uint8

const (
	helperPhaseNotEntered helperPhaseResult = iota
	helperPhaseOK
	helperPhaseCanceled
	helperPhaseDeadline
	helperPhaseOutputLimit
	helperPhaseExitFailed
	helperPhaseWaitDelay
	helperPhaseOSError
	helperPhaseAlreadyGone
)

func (r helperPhaseResult) label() string {
	switch r {
	case helperPhaseOK:
		return "ok"
	case helperPhaseCanceled:
		return "canceled"
	case helperPhaseDeadline:
		return "deadline"
	case helperPhaseOutputLimit:
		return "output-limit"
	case helperPhaseExitFailed:
		return "exit-failed"
	case helperPhaseWaitDelay:
		return "wait-delay"
	case helperPhaseOSError:
		return "os-error"
	case helperPhaseAlreadyGone:
		return "already-gone"
	default:
		return "not-entered"
	}
}

func (r helperPhaseResult) failed() bool {
	return r != helperPhaseNotEntered && r != helperPhaseOK && r != helperPhaseAlreadyGone
}

func classifyHelperPhaseError(err error) helperPhaseResult {
	switch {
	case err == nil:
		return helperPhaseOK
	case errors.Is(err, context.Canceled):
		return helperPhaseCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return helperPhaseDeadline
	case errors.Is(err, ErrOutputLimit):
		return helperPhaseOutputLimit
	case errors.Is(err, exec.ErrWaitDelay):
		return helperPhaseWaitDelay
	case errors.Is(err, os.ErrProcessDone):
		return helperPhaseAlreadyGone
	default:
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return helperPhaseExitFailed
		}
		return helperPhaseOSError
	}
}

type helperPhaseObservation struct {
	result  helperPhaseResult
	elapsed time.Duration
}

func helperNonnegative(d time.Duration) int64 {
	if d < 0 {
		return 0
	}
	return int64(d)
}

// MarshalJSON exposes the same fixed schema as Summary. The seven entries are
// always present, so a stage that was never entered cannot resemble a fast
// successful operation. Repeated reclamation calls accumulate durations; the
// first observed failure is retained. Concurrent wait/reclaim intervals can
// overlap, so their durations must not be summed to infer total latency.
func (d HelperDiagnostics) MarshalJSON() ([]byte, error) {
	type phaseJSON struct {
		Phase     string `json:"phase"`
		State     string `json:"state"`
		Result    string `json:"result"`
		ElapsedNS int64  `json:"elapsed_ns"`
	}
	type diagnosticJSON struct {
		Schema        int         `json:"schema"`
		Outcome       string      `json:"outcome"`
		ParentContext string      `json:"parent_context"`
		HelperContext string      `json:"helper_context"`
		ElapsedNS     int64       `json:"elapsed_ns"`
		FailedStage   string      `json:"failed_stage"`
		Phases        []phaseJSON `json:"phases"`
		Truncated     bool        `json:"truncated"`
	}
	wire := diagnosticJSON{
		Schema: 1, Outcome: d.outcome.label(),
		ParentContext: d.parentContext.label(), HelperContext: d.helperContext.label(),
		ElapsedNS: helperNonnegative(d.elapsed), FailedStage: "none",
		Phases: make([]phaseJSON, helperPhaseCount),
	}
	if d.failed {
		wire.FailedStage = d.failedStage.label()
	}
	for i, p := range d.phases {
		state := "not-entered"
		elapsed := int64(0)
		if p.result >= helperPhaseOK && p.result <= helperPhaseAlreadyGone {
			state = "completed"
			elapsed = helperNonnegative(p.elapsed)
			if p.result.failed() {
				state = "failed"
			}
		}
		wire.Phases[i] = phaseJSON{helperPhase(i).label(), state, p.result.label(), elapsed}
	}
	data, err := json.Marshal(wire)
	// Fixed enums, seven records, and int64 durations fit below this bound even
	// at their longest representations. Keep a diagnostic-only fallback should
	// a future schema addition exceed it; execution and cleanup are untouched.
	if len(data) > 2048 {
		wire.Phases = nil
		wire.Truncated = true
		return json.Marshal(wire)
	}
	return data, err
}

// Summary returns only the bounded, fixed-schema JSON observation.
func (d HelperDiagnostics) Summary() string {
	data, _ := d.MarshalJSON()
	return string(data)
}

// helperTrace is private to one invocation. The existing cancellation callback
// may record reclamation concurrently with Wait; no caller callback is exposed.
// All callbacks are joined before the final value is returned.
type helperTrace struct {
	mu      sync.Mutex
	started time.Time
	value   HelperDiagnostics
}

func newHelperTrace() *helperTrace { return &helperTrace{started: time.Now()} }

func (t *helperTrace) begin() time.Time {
	if t == nil {
		return time.Time{}
	}
	return time.Now()
}

func (t *helperTrace) recordError(phase helperPhase, start time.Time, err error) {
	if t != nil {
		t.record(phase, start, classifyHelperPhaseError(err))
	}
}

func (t *helperTrace) record(phase helperPhase, start time.Time, result helperPhaseResult) {
	if t == nil || phase >= helperPhaseCount {
		return
	}
	elapsed := time.Duration(helperNonnegative(time.Since(start)))
	t.mu.Lock()
	defer t.mu.Unlock()
	p := &t.value.phases[phase]
	if p.result == helperPhaseNotEntered || (!p.result.failed() && result.failed()) {
		p.result = result
	}
	if elapsed > time.Duration(math.MaxInt64)-p.elapsed {
		p.elapsed = time.Duration(math.MaxInt64)
	} else {
		p.elapsed += elapsed
	}
	if result.failed() && phase != helperReclaim && phase != helperRelease && !t.value.failed {
		t.value.failed, t.value.failedStage = true, phase
	}
}

func (t *helperTrace) finish(parent, ctx context.Context, outcome helperOutcome) {
	if t == nil {
		return
	}
	parentState, helperState := observeHelperContext(parent), observeHelperContext(ctx)
	elapsed := time.Duration(helperNonnegative(time.Since(t.started)))
	t.mu.Lock()
	t.value.outcome = outcome
	t.value.parentContext, t.value.helperContext = parentState, helperState
	t.value.elapsed = elapsed
	t.mu.Unlock()
}

func (t *helperTrace) finishResult(parent, ctx context.Context, err error) {
	if t == nil {
		return
	}
	outcome := helperOtherError
	switch err {
	case nil:
		outcome = helperSuccess
	case ErrCommandFailed:
		outcome = helperCommandFailed
	case ErrOutputLimit:
		outcome = helperOutputLimit
	case ErrTimeout:
		outcome = helperTimeout
	case context.Canceled:
		outcome = helperParentCanceled
	case context.DeadlineExceeded:
		outcome = helperParentDeadline
	}
	t.finish(parent, ctx, outcome)
}

func (t *helperTrace) snapshot() HelperDiagnostics {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.value
}
