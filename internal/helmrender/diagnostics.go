package helmrender

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/laojianzi/aster/internal/credentialexec"
)

const (
	// MaxDiagnosticsBytes also bounds Summary, including its fixed prefix.
	MaxDiagnosticsBytes     = 4096
	DiagnosticSummaryPrefix = "Helm capture diagnostic v1: "
)

// DiagnosticStage identifies a fixed renderer operation, never an input path.
type DiagnosticStage uint8

const (
	StageRequest DiagnosticStage = iota
	StageInputSnapshot
	StageArchiveReadHash
	StageTempDir
	StageUnpackExecutable
	StagePrivateEnvironment
	StageHelper
	StageOutputValidation
	StageCleanup
	diagnosticStageCount
)

func (s DiagnosticStage) String() string {
	switch s {
	case StageRequest:
		return "request"
	case StageInputSnapshot:
		return "input-snapshot"
	case StageArchiveReadHash:
		return "archive-read-hash"
	case StageTempDir:
		return "private-tempdir"
	case StageUnpackExecutable:
		return "unpack-executable"
	case StagePrivateEnvironment:
		return "private-files-environment"
	case StageHelper:
		return "helper"
	case StageOutputValidation:
		return "output-validation"
	case StageCleanup:
		return "cleanup"
	default:
		return "unknown"
	}
}

type DiagnosticPhaseState uint8

const (
	PhaseNotEntered DiagnosticPhaseState = iota
	PhaseCompleted
	PhaseFailed
)

func (s DiagnosticPhaseState) String() string {
	switch s {
	case PhaseNotEntered:
		return "not-entered"
	case PhaseCompleted:
		return "completed"
	case PhaseFailed:
		return "failed"
	default:
		return "unknown"
	}
}

type DiagnosticOutcome uint8

const (
	DiagnosticNotStarted DiagnosticOutcome = iota
	DiagnosticSuccess
	DiagnosticInputRejected
	DiagnosticDistributionRejected
	DiagnosticBusy
	DiagnosticCanceled
	DiagnosticDeadline
	DiagnosticOutputRejected
	DiagnosticRenderFailed
	DiagnosticOtherFailure
)

func (o DiagnosticOutcome) String() string {
	switch o {
	case DiagnosticNotStarted:
		return "not-started"
	case DiagnosticSuccess:
		return "success"
	case DiagnosticInputRejected:
		return "input-rejected"
	case DiagnosticDistributionRejected:
		return "distribution-rejected"
	case DiagnosticBusy:
		return "busy"
	case DiagnosticCanceled:
		return "canceled"
	case DiagnosticDeadline:
		return "deadline"
	case DiagnosticOutputRejected:
		return "output-rejected"
	case DiagnosticRenderFailed:
		return "render-failed"
	case DiagnosticOtherFailure:
		return "other-failure"
	default:
		return "unknown"
	}
}

type DiagnosticContext uint8

const (
	ContextNotProvided DiagnosticContext = iota
	ContextActive
	ContextCanceled
	ContextDeadline
	ContextOtherError
)

func (c DiagnosticContext) String() string {
	switch c {
	case ContextNotProvided:
		return "not-provided"
	case ContextActive:
		return "active"
	case ContextCanceled:
		return "canceled"
	case ContextDeadline:
		return "deadline"
	case ContextOtherError:
		return "other-error"
	default:
		return "unknown"
	}
}

// DiagnosticPhase is a copied observation. Durations are measured with Go's
// monotonic clock. The wire summary reports whole milliseconds, not wall time.
type DiagnosticPhase struct {
	Stage    DiagnosticStage
	State    DiagnosticPhaseState
	Duration time.Duration
}

type renderPhase struct {
	state   DiagnosticPhaseState
	elapsed time.Duration
}

// Diagnostics owns only fixed enums, durations and the helper's bounded safe
// diagnostic value. It retains no errors, requests, paths, hashes or output.
// Its nine renderer phases and seven nested helper phases have a fixed bound.
type Diagnostics struct {
	outcome         DiagnosticOutcome
	fault           DiagnosticStage
	hasFault        bool
	parentContext   DiagnosticContext
	rendererContext DiagnosticContext
	elapsed         time.Duration
	phases          [diagnosticStageCount]renderPhase
	helper          credentialexec.HelperDiagnostics
	helperEntered   bool
}

func (d Diagnostics) Outcome() DiagnosticOutcome          { return d.outcome }
func (d Diagnostics) FaultStage() (DiagnosticStage, bool) { return d.fault, d.hasFault }
func (d Diagnostics) ParentContext() DiagnosticContext    { return d.parentContext }
func (d Diagnostics) RendererContext() DiagnosticContext  { return d.rendererContext }
func (d Diagnostics) Elapsed() time.Duration              { return nonnegativeDuration(d.elapsed) }
func (d Diagnostics) Helper() (credentialexec.HelperDiagnostics, bool) {
	return d.helper, d.helperEntered
}

func (d Diagnostics) Phases() []DiagnosticPhase {
	phases := make([]DiagnosticPhase, diagnosticStageCount)
	for stage, phase := range d.phases {
		phases[stage] = DiagnosticPhase{DiagnosticStage(stage), phase.state, nonnegativeDuration(phase.elapsed)}
	}
	return phases
}

type diagnosticPhaseJSON struct {
	Stage     string `json:"stage"`
	State     string `json:"state"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

// MarshalJSON serializes only the fixed diagnostic schema. The helper has its
// own bounded serializer; no arbitrary Go values or error strings are included.
func (d Diagnostics) MarshalJSON() ([]byte, error) {
	var phases [diagnosticStageCount]diagnosticPhaseJSON
	for stage, phase := range d.phases {
		phases[stage] = diagnosticPhaseJSON{
			Stage: DiagnosticStage(stage).String(), State: phase.state.String(),
			ElapsedMS: nonnegativeDuration(phase.elapsed).Milliseconds(),
		}
	}
	fault := "none"
	if d.hasFault {
		fault = d.fault.String()
	}
	var helper *credentialexec.HelperDiagnostics
	if d.helperEntered {
		helper = &d.helper
	}
	wire := struct {
		Schema          int                                       `json:"schema"`
		Outcome         string                                    `json:"outcome"`
		FaultStage      string                                    `json:"fault_stage"`
		ParentContext   string                                    `json:"parent_context"`
		RendererContext string                                    `json:"renderer_context"`
		ElapsedMS       int64                                     `json:"elapsed_ms"`
		Phases          [diagnosticStageCount]diagnosticPhaseJSON `json:"phases"`
		Helper          *credentialexec.HelperDiagnostics         `json:"helper,omitempty"`
		Truncated       bool                                      `json:"truncated"`
	}{1, d.outcome.String(), fault, d.parentContext.String(), d.rendererContext.String(),
		nonnegativeDuration(d.elapsed).Milliseconds(), phases, helper, false}
	encoded, err := json.Marshal(wire)
	if err != nil || len(encoded)+len(DiagnosticSummaryPrefix) > MaxDiagnosticsBytes {
		// Fixed primitives make this fallback infallible and much smaller than
		// the bound. Never return or retain a serialization error's text.
		fallback := struct {
			Schema          int    `json:"schema"`
			Outcome         string `json:"outcome"`
			FaultStage      string `json:"fault_stage"`
			ParentContext   string `json:"parent_context"`
			RendererContext string `json:"renderer_context"`
			Truncated       bool   `json:"truncated"`
			Diagnostic      string `json:"diagnostic"`
		}{1, d.outcome.String(), fault, d.parentContext.String(), d.rendererContext.String(),
			true, "encoding-unavailable"}
		encoded, _ = json.Marshal(fallback)
	}
	return encoded, nil
}

func (d Diagnostics) Summary() string {
	// MarshalJSON always supplies either the bounded complete schema or the
	// fixed truncated schema. It never exposes an underlying encoding error.
	encoded, _ := d.MarshalJSON()
	return DiagnosticSummaryPrefix + string(encoded)
}

func nonnegativeDuration(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

func diagnosticContext(ctx context.Context) DiagnosticContext {
	if ctx == nil {
		return ContextNotProvided
	}
	err := ctx.Err()
	switch {
	case err == nil:
		return ContextActive
	case errors.Is(err, context.Canceled):
		return ContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return ContextDeadline
	default:
		return ContextOtherError
	}
}

func diagnosticOutcome(err error) DiagnosticOutcome {
	switch {
	case err == nil:
		return DiagnosticSuccess
	case errors.Is(err, ErrInput):
		return DiagnosticInputRejected
	case errors.Is(err, ErrDistribution):
		return DiagnosticDistributionRejected
	case errors.Is(err, ErrBusy):
		return DiagnosticBusy
	case errors.Is(err, context.Canceled):
		return DiagnosticCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return DiagnosticDeadline
	case errors.Is(err, ErrOutput):
		return DiagnosticOutputRejected
	case errors.Is(err, ErrRender):
		return DiagnosticRenderFailed
	default:
		return DiagnosticOtherFailure
	}
}

// renderTrace belongs to one synchronous render call. It is never published;
// only its immutable value snapshot leaves the call after every defer returns.
type renderTrace struct {
	value            Diagnostics
	started          time.Time
	phaseStarted     time.Time
	phase            DiagnosticStage
	active           bool
	primaryFinished  bool
	contextsRecorded bool
}

func newRenderTrace() *renderTrace { return &renderTrace{started: time.Now()} }

func (t *renderTrace) begin(stage DiagnosticStage) {
	if t == nil {
		return
	}
	now := time.Now()
	if t.active {
		t.finishPhase(now, false)
	}
	t.phase, t.phaseStarted, t.active = stage, now, true
}

func (t *renderTrace) finishPhase(now time.Time, failed bool) {
	state := PhaseCompleted
	if failed {
		state = PhaseFailed
	}
	t.value.phases[t.phase] = renderPhase{state, nonnegativeDuration(now.Sub(t.phaseStarted))}
	t.active = false
}

func (t *renderTrace) finishPrimary(err error) {
	if t == nil || t.primaryFinished {
		return
	}
	if t.active {
		t.finishPhase(time.Now(), err != nil)
	}
	t.value.outcome = diagnosticOutcome(err)
	if err != nil {
		t.value.fault, t.value.hasFault = t.phase, true
	}
	t.primaryFinished = true
}

func (t *renderTrace) observeContexts(parent, renderer context.Context) {
	if t == nil {
		return
	}
	t.value.parentContext = diagnosticContext(parent)
	t.value.rendererContext = diagnosticContext(renderer)
	t.contextsRecorded = true
}

func (t *renderTrace) observeParentIfNeeded(parent context.Context) {
	if t != nil && !t.contextsRecorded {
		t.observeContexts(parent, nil)
	}
}

func (t *renderTrace) recordCleanup(started time.Time, failed bool) {
	if t == nil {
		return
	}
	state := PhaseCompleted
	if failed {
		state = PhaseFailed
	}
	// Cleanup is independent: never replace the real operation's fault stage,
	// returned error or outcome, even if removal itself fails.
	t.value.phases[StageCleanup] = renderPhase{state, nonnegativeDuration(time.Since(started))}
}

func (t *renderTrace) snapshot() Diagnostics {
	t.value.elapsed = nonnegativeDuration(time.Since(t.started))
	return t.value
}
