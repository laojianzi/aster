package helmrender

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const diagnosticPrivateValue = "PRIVATE_DIAGNOSTIC_VALUES_CANARY"

func diagnosticRequest(t *testing.T, entries []entry) Request {
	t.Helper()
	if entries == nil {
		entries = minimalEntries()
	}
	return Request{
		HelmArchive:  writeTestFile(t, "PRIVATE_DISTRIBUTION_PATH", []byte("PRIVATE_UNAPPROVED_ARCHIVE_CANARY")),
		ChartArchive: writeTestFile(t, "PRIVATE_CHART_PATH.tgz", chartArchive(t, entries...)),
		ValuesFile:   writeTestFile(t, "PRIVATE_VALUES_PATH.yaml", []byte("message: "+diagnosticPrivateValue+"\n")),
		Release:      "private-release-canary", Namespace: "private-namespace-canary",
		KubeVersion: "1.37.0", Trusted: true,
	}
}

func diagnosticApprovedArchive(t *testing.T) string {
	t.Helper()
	archive := os.Getenv("ASTER_HELM_ARCHIVE")
	if archive == "" {
		t.Skip("requires approved Helm archive; mandatory CI evidence rejects this skip")
	}
	return archive
}

func diagnosticTempEnvironment(t *testing.T, root string) {
	t.Helper()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, root)
	}
	if filepath.Clean(os.TempDir()) != filepath.Clean(root) {
		t.Fatal("test did not establish the intended private temporary directory")
	}
}

func diagnosticAssertPrivacy(t *testing.T, d Diagnostics, request Request) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(d)
	if err != nil || len(encoded) == 0 {
		t.Fatal("safe diagnostic did not serialize", err)
	}
	summary := d.Summary()
	if !strings.HasPrefix(summary, DiagnosticSummaryPrefix) || len(summary) > MaxDiagnosticsBytes ||
		strings.TrimPrefix(summary, DiagnosticSummaryPrefix) != string(encoded) {
		t.Fatal("diagnostic summary lost the fixed complete schema or exceeded its bound")
	}
	// Check both supported serialization and the value itself: a hidden retained
	// input/error must not be mistaken for a safe public projection.
	all := summary + string(encoded) + fmt.Sprintf("%#v", d)
	forbidden := []string{"PRIVATE_", request.HelmArchive, request.ChartArchive, request.ValuesFile,
		request.Release, request.Namespace}
	for _, path := range []string{request.ChartArchive, request.ValuesFile} {
		if data, err := os.ReadFile(path); err == nil {
			digest := sha256.Sum256(data)
			forbidden = append(forbidden, hex.EncodeToString(digest[:]))
		}
	}
	if approved, _, ok := approved(); ok {
		forbidden = append(forbidden, approved.SHA256)
	}
	for _, private := range forbidden {
		if private != "" && strings.Contains(all, private) {
			t.Fatal("diagnostic retained or emitted private input/error material")
		}
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil || wire["schema"] != float64(1) {
		t.Fatal("invalid diagnostic schema", err)
	}
	return wire
}

func diagnosticAssertPhases(t *testing.T, d Diagnostics) {
	t.Helper()
	phases := d.Phases()
	if len(phases) != 9 || d.Elapsed() < 0 {
		t.Fatal("renderer phase count or total duration changed")
	}
	var elapsed time.Duration
	gap := false
	for index, phase := range phases {
		if phase.Stage != DiagnosticStage(index) || phase.Duration < 0 {
			t.Fatal("phase order or nonnegative monotonic duration was lost")
		}
		elapsed += phase.Duration
		switch phase.State {
		case PhaseNotEntered:
			if phase.Duration != 0 {
				t.Fatal("unentered phase has an invented duration")
			}
			gap = true
		case PhaseCompleted, PhaseFailed:
			if gap && phase.Stage != StageCleanup {
				t.Fatal("a primary phase ran after an unentered predecessor")
			}
		default:
			t.Fatal("unexpected completed-call phase state")
		}
	}
	if elapsed > d.Elapsed() {
		t.Fatal("renderer phases overlap or exceed the measured whole invocation")
	}
	_, helperEntered := d.Helper()
	if helperEntered != (phases[StageHelper].State != PhaseNotEntered) {
		t.Fatal("helper diagnostic presence disagrees with the actual helper phase")
	}
}

func TestHelmRenderDiagnosticsValidation(t *testing.T) {
	base := diagnosticRequest(t, nil)
	for _, name := range []string{"nil-context", "early-cancel", "early-deadline", "invalid-request", "busy", "input-snapshot", "distribution"} {
		t.Run(name, func(t *testing.T) {
			request := base
			var ctx context.Context = context.Background()
			wantErr, wantOutcome, wantStage := ErrInput, DiagnosticInputRejected, StageRequest
			wantContext := ContextActive
			switch name {
			case "nil-context":
				ctx, wantContext = nil, ContextNotProvided
			case "early-cancel":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx, wantErr, wantOutcome, wantContext = cancelled, context.Canceled, DiagnosticCanceled, ContextCanceled
			case "early-deadline":
				expired, cancel := context.WithDeadline(ctx, time.Unix(1, 0))
				t.Cleanup(cancel)
				ctx, wantErr, wantOutcome, wantContext = expired, context.DeadlineExceeded, DiagnosticDeadline, ContextDeadline
			case "invalid-request":
				request.Trusted = false
			case "busy":
				admission <- struct{}{}
				t.Cleanup(func() { <-admission })
				wantErr, wantOutcome = ErrBusy, DiagnosticBusy
			case "input-snapshot":
				request.ChartArchive = filepath.Join(filepath.Dir(request.ChartArchive), "PRIVATE_MISSING_CHART_PATH.tgz")
				wantStage = StageInputSnapshot
			case "distribution":
				wantErr, wantOutcome, wantStage = ErrDistribution, DiagnosticDistributionRejected, StageArchiveReadHash
			}
			result, d, err := RenderWithDiagnostics(ctx, request)
			legacy, legacyErr := Render(ctx, request)
			if result != nil || legacy != nil || err != wantErr || legacyErr != wantErr || !errors.Is(err, wantErr) {
				t.Fatal("diagnostic path changed the original public error identity", err, legacyErr)
			}
			stage, failed := d.FaultStage()
			if d.Outcome() != wantOutcome || !failed || stage != wantStage || d.ParentContext() != wantContext {
				t.Fatal("wrong early failure classification", d.Summary())
			}
			if d.Phases()[StageCleanup].State != PhaseNotEntered {
				t.Fatal("early failure invented private-directory cleanup")
			}
			if wantStage == StageRequest && d.RendererContext() != ContextNotProvided ||
				wantStage != StageRequest && d.RendererContext() != ContextActive {
				t.Fatal("renderer context was invented or sampled after deferred cancellation")
			}
			diagnosticAssertPhases(t, d)
			diagnosticAssertPrivacy(t, d, request)
		})
	}
}

func TestHelmRenderDiagnosticsPreparationFailure(t *testing.T) {
	archive := diagnosticApprovedArchive(t)
	for _, name := range []string{"tempdir", "private-files"} {
		t.Run(name, func(t *testing.T) {
			entries := minimalEntries()
			if name == "private-files" {
				// Both are legal chart members. Their ancestor/file conflict makes
				// the real private write fail regardless of Go map iteration order.
				entries = append(entries,
					entry{"sample/templates/blocked", "PRIVATE_PARENT_FILE_CANARY", 0},
					entry{"sample/templates/blocked/child.yaml", "PRIVATE_CHILD_FILE_CANARY", 0})
			}
			request := diagnosticRequest(t, entries)
			request.HelmArchive = archive
			privateRoot := t.TempDir()
			wantStage := StagePrivateEnvironment
			if name == "tempdir" {
				privateRoot = writeTestFile(t, "PRIVATE_NOT_A_DIRECTORY", []byte("PRIVATE_TEMP_INPUT_CANARY"))
				wantStage = StageTempDir
			}
			diagnosticTempEnvironment(t, privateRoot)
			result, d, err := RenderWithDiagnostics(context.Background(), request)
			if result != nil || err != ErrRender || !errors.Is(err, ErrRender) || d.Outcome() != DiagnosticRenderFailed {
				t.Fatal("real preparation failure changed the public result", err, d.Summary())
			}
			if stage, failed := d.FaultStage(); !failed || stage != wantStage {
				t.Fatal("cleanup replaced the actual preparation fault", d.Summary())
			}
			if _, entered := d.Helper(); entered {
				t.Fatal("preparation failure was mistaken for a helper execution")
			}
			if name == "tempdir" {
				data, readErr := os.ReadFile(privateRoot)
				if readErr != nil || string(data) != "PRIVATE_TEMP_INPUT_CANARY" || d.Phases()[StageCleanup].State != PhaseNotEntered {
					t.Fatal("failed tempdir creation mutated caller input or invented cleanup")
				}
			} else {
				remaining, readErr := os.ReadDir(privateRoot)
				if readErr != nil || len(remaining) != 0 || d.Phases()[StageCleanup].State != PhaseCompleted {
					t.Fatal("real private directory was not fully reclaimed", readErr)
				}
			}
			if d.ParentContext() != ContextActive || d.RendererContext() != ContextActive {
				t.Fatal("context state was sampled after our own cancellation", d.Summary())
			}
			diagnosticAssertPhases(t, d)
			diagnosticAssertPrivacy(t, d, request)
		})
	}
}

func TestHelmRenderDiagnosticsRealHelper(t *testing.T) {
	archive := diagnosticApprovedArchive(t)
	for _, name := range []string{"success", "render-error", "output-limit"} {
		t.Run(name, func(t *testing.T) {
			entries := minimalEntries()
			var wantErr error
			wantOutcome, wantHelper := DiagnosticSuccess, "success"
			switch name {
			case "render-error":
				entries[2].text = `{{ fail "PRIVATE_STDERR_TEMPLATE_CANARY" }}`
				wantErr, wantOutcome, wantHelper = ErrRender, DiagnosticRenderFailed, "command-failed"
			case "output-limit":
				entries[2].text = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: overflow\ndata:\n  large: {{ repeat 2100000 \"x\" | quote }}\n"
				wantErr, wantOutcome, wantHelper = ErrOutput, DiagnosticOutputRejected, "output-limit"
			}
			request := diagnosticRequest(t, entries)
			request.HelmArchive = archive
			privateRoot := t.TempDir()
			diagnosticTempEnvironment(t, privateRoot)
			t.Setenv("AWS_SECRET_ACCESS_KEY", "PRIVATE_INHERITED_ENV_CANARY")
			t.Setenv("HELM_KUBETOKEN", "PRIVATE_INHERITED_TOKEN_CANARY")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			result, d, err := RenderWithDiagnostics(ctx, request)
			if err != wantErr || d.Outcome() != wantOutcome || !errors.Is(err, wantErr) {
				t.Fatal("unexpected actual helper result", err, d.Summary())
			}
			if wantErr == nil {
				if result == nil || !strings.Contains(result.Manifest(), diagnosticPrivateValue) {
					t.Fatal("approved Helm did not render the actual private values")
				}
				if _, failed := d.FaultStage(); failed {
					t.Fatal("successful render invented a fault stage")
				}
			} else if stage, failed := d.FaultStage(); result != nil || !failed || stage != StageHelper {
				t.Fatal("helper failure accepted output or lost the exact helper fault", d.Summary())
			}
			remaining, readErr := os.ReadDir(privateRoot)
			if readErr != nil || len(remaining) != 0 || d.Phases()[StageCleanup].State != PhaseCompleted {
				t.Fatal("actual helper private executable/files survived cleanup", readErr)
			}
			if d.ParentContext() != ContextActive || d.RendererContext() != ContextActive {
				t.Fatal("context sampling observed our deferred cancellation", d.Summary())
			}
			diagnosticAssertPhases(t, d)
			wire := diagnosticAssertPrivacy(t, d, request)
			helper, ok := wire["helper"].(map[string]any)
			if !ok || helper["outcome"] != wantHelper {
				t.Fatal("summary did not preserve the actual helper terminal category", d.Summary())
			}
			phases, ok := helper["phases"].([]any)
			if !ok || len(phases)+len(d.Phases()) != 16 {
				t.Fatal("combined renderer/helper phase budget changed")
			}
		})
	}
}

func TestHelmRenderDiagnosticsPrivacyAndBounds(t *testing.T) {
	var zero Diagnostics
	zeroWire := diagnosticAssertPrivacy(t, zero, Request{})
	if zero.Outcome() != DiagnosticNotStarted || zeroWire["fault_stage"] != "none" || len(zero.Phases()) != 9 {
		t.Fatal("zero diagnostic invented an executed call")
	}
	for _, phase := range zero.Phases() {
		if phase.State != PhaseNotEntered || phase.Duration != 0 {
			t.Fatal("zero diagnostic invented phase work")
		}
	}
	// These exercise serialization limits and fault retention, not fake render
	// results. Real validation/preparation/helper calls are covered above.
	maximum := Diagnostics{outcome: DiagnosticDistributionRejected, fault: StagePrivateEnvironment,
		hasFault: true, parentContext: ContextNotProvided, rendererContext: ContextNotProvided,
		elapsed: time.Duration(math.MaxInt64), helperEntered: true}
	for index := range maximum.phases {
		maximum.phases[index] = renderPhase{PhaseCompleted, time.Duration(math.MaxInt64)}
	}
	encoded, err := json.Marshal(maximum)
	if err != nil || len(encoded)+len(DiagnosticSummaryPrefix) > MaxDiagnosticsBytes {
		t.Fatal("maximum fixed-width diagnostic exceeds its complete summary bound", err)
	}
	var maximumWire map[string]any
	if err := json.Unmarshal(encoded, &maximumWire); err != nil || maximumWire["schema"] != float64(1) ||
		maximumWire["truncated"] != false || !json.Valid([]byte(strings.TrimPrefix(maximum.Summary(), DiagnosticSummaryPrefix))) {
		t.Fatal("maximum fixed-width diagnostic lost complete valid JSON", err)
	}
	maximum.phases[0].elapsed = -time.Second
	maximum.parentContext = DiagnosticContext(255)
	maximum.fault = DiagnosticStage(255)
	if maximum.Phases()[0].Duration != 0 || strings.Contains(maximum.Summary(), "-1000") ||
		!strings.Contains(maximum.Summary(), `"fault_stage":"unknown"`) ||
		!json.Valid([]byte(strings.TrimPrefix(maximum.Summary(), DiagnosticSummaryPrefix))) || len(maximum.Summary()) > MaxDiagnosticsBytes {
		t.Fatal("invalid private enum/duration escaped bounded serialization")
	}
	trace := newRenderTrace()
	trace.begin(StageInputSnapshot)
	trace.finishPrimary(errors.New("PRIVATE_OS_ERROR_CANARY"))
	trace.recordCleanup(time.Now(), true)
	trace.finishPrimary(nil)
	d := trace.snapshot()
	if stage, failed := d.FaultStage(); !failed || stage != StageInputSnapshot ||
		d.Outcome() != DiagnosticOtherFailure || d.Phases()[StageCleanup].State != PhaseFailed {
		t.Fatal("cleanup or a second finalization replaced the original operation failure")
	}
	diagnosticAssertPrivacy(t, d, Request{})
}

func TestHelmRenderDiagnosticsSnapshotIsolation(t *testing.T) {
	request := diagnosticRequest(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, d, err := RenderWithDiagnostics(ctx, request)
	if err != ErrDistribution || d.ParentContext() != ContextActive {
		t.Fatal("could not capture a real active-context diagnostic", err)
	}
	before := d.Summary()
	phases := d.Phases()
	phases[StageArchiveReadHash].Stage = StageCleanup
	phases[StageArchiveReadHash].State = PhaseCompleted
	phases[StageArchiveReadHash].Duration = 0
	copy := d
	copy.phases[StageArchiveReadHash].state = PhaseCompleted
	copy.hasFault = false
	cancel()
	if err := os.WriteFile(request.ValuesFile, []byte("message: PRIVATE_CHANGED_INPUT_CANARY\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if d.Summary() != before || d.ParentContext() != ContextActive || d.Phases()[StageArchiveReadHash].State != PhaseFailed {
		t.Fatal("returned slice/value, parent context or source input mutated the diagnostic snapshot")
	}
	diagnosticAssertPhases(t, d)
	diagnosticAssertPrivacy(t, d, request)
}
