package uiworkbench

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/helmrender"
	"github.com/laojianzi/aster/internal/kube"
)

// Only the owning UI goroutine mutates this state. Workers receive exact input
// snapshots. Neither a journal handle nor a single-use grant is UI state.
type helmInstallState struct {
	open                                         bool
	epoch, paneEpoch, contextEpoch               uint64
	backend                                      *kube.Backend
	session, namespace, contextName, journalPath string
	archive, chart, values, release              string
	trusted, capabilityAck, composing            bool
	prepareAck, dryRunAck, executeAck            bool
	confirmation                                 string
	confirmationComposing                        bool
	busy, status                                 string
	attempted, stopRequested                     bool
	candidate                                    *helmrelease.InstallCandidate
	preparation                                  *kube.HelmInstallPreparation
	dryRun                                       *kube.HelmInstallDryRun
	execution                                    *kube.HelmInstallExecution
	capturedRequest                              helmrender.InstallRequest
	flowCtx                                      context.Context
	flowCancel, cancel                           context.CancelFunc
}

func (s *helmInstallState) request() helmrender.InstallRequest {
	return helmrender.InstallRequest{HelmArchive: s.archive, ChartArchive: s.chart,
		ValuesFile: s.values, Release: s.release, Namespace: s.namespace,
		CapabilityAssumption: helmrelease.InstallCapabilityAssumption, Trusted: s.trusted}
}

func (s *helmInstallState) inputsMatch() bool {
	return s.trusted && s.capabilityAck && s.capturedRequest == s.request()
}

func (s *helmInstallState) revoke() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.flowCancel != nil {
		s.flowCancel()
	}
	s.cancel, s.flowCancel, s.flowCtx = nil, nil, nil
	s.prepareAck, s.dryRunAck, s.executeAck = false, false, false
	s.confirmation, s.confirmationComposing = "", false
}

func (w *Workbench) clearHelmInstall() {
	s := &w.helmInstall
	s.revoke()
	w.helmInstall = helmInstallState{epoch: s.epoch + 1}
}

func (w *Workbench) helmInstallScopeMatches() bool {
	s := &w.helmInstall
	return s.open && s.backend != nil && s.backend == w.backend && s.contextEpoch == w.contextEpoch &&
		s.session == w.sessionID && s.namespace == strings.TrimSpace(w.namespace) &&
		s.contextName == w.currentContext && s.journalPath == w.helmJournalPath
}

func (w *Workbench) syncHelmInstall() {
	if !w.helmInstall.open {
		return
	}
	if !w.helmInstallScopeMatches() || w.ctx == nil || w.ctx.Err() != nil ||
		w.operationContext().Err() != nil ||
		(!w.credentialExpiry.IsZero() && !time.Now().Before(w.credentialExpiry)) {
		w.clearHelmInstall()
		return
	}
	// This checks selected input strings and acknowledgements only. Captured
	// files are never stat'ed, reopened, watched or rendered again downstream.
	if w.helmInstall.candidate != nil && !w.helmInstall.inputsMatch() {
		w.helmInstallInputsChanged()
	}
}

func (w *Workbench) helmInstallEligible() bool {
	return w.helmInstallScopeMatches() && w.helmInstall.busy == "" && w.pendingWrites == 0 &&
		w.life != nil && w.ctx != nil && w.ctx.Err() == nil && w.operationContext().Err() == nil
}

func (w *Workbench) helmInstallCurrent(epoch uint64) bool {
	return w.helmInstall.epoch == epoch && w.helmInstallScopeMatches()
}

// Opening is inert: no capture, network read or private journal open occurs.
func (w *Workbench) openHelmInstall() {
	ns := strings.TrimSpace(w.namespace)
	if w.backend == nil || w.detail != nil || w.delivery.open || w.pendingWrites != 0 ||
		w.life == nil || w.ctx == nil || w.ctx.Err() != nil || w.operationContext().Err() != nil ||
		!helmrelease.ValidNamespace(ns) ||
		(!w.credentialExpiry.IsZero() && !time.Now().Before(w.credentialExpiry)) {
		return
	}
	w.clearHelmReleases()
	w.helmInstall = helmInstallState{open: true, epoch: w.helmInstall.epoch, paneEpoch: w.helmInstall.epoch,
		backend: w.backend, contextEpoch: w.contextEpoch, session: w.sessionID,
		namespace: ns, contextName: w.currentContext, journalPath: w.helmJournalPath,
		status: "Select trusted local inputs and accept the fixed capability assumption before capturing."}
}

func (w *Workbench) helmInstallInputsChanged() {
	w.invalidateHelmInstallInputs(false)
}

func (w *Workbench) helmInstallTrustChanged() {
	w.invalidateHelmInstallInputs(true)
}

func (w *Workbench) invalidateHelmInstallInputs(preserveTrust bool) {
	s := &w.helmInstall
	if !s.open {
		return
	}
	if s.attempted {
		// Disabled selectors cannot retarget an attempted write. Keep its epoch
		// so the worker can still publish historical effects after cancellation;
		// dropping that result must never look like a NotStarted attempt.
		if request := s.capturedRequest; request.Namespace != "" {
			s.archive, s.chart, s.values, s.release = request.HelmArchive, request.ChartArchive, request.ValuesFile, request.Release
			s.trusted, s.capabilityAck = request.Trusted, true
		}
		s.composing = false
		w.stopHelmInstall()
		return
	}
	s.revoke()
	s.epoch++ // Old controls and queued callbacks cannot act on the new inputs.
	s.candidate, s.preparation, s.dryRun = nil, nil, nil
	s.capturedRequest = helmrender.InstallRequest{}
	s.busy, s.stopRequested = "", false
	if !preserveTrust {
		s.trusted, s.capabilityAck = false, false
	}
	s.status = "Input selection changed. Capture again after explicitly accepting these inputs."
}

func (w *Workbench) stopHelmInstall() {
	s := &w.helmInstall
	if !s.open {
		return
	}
	s.revoke()
	s.preparation, s.dryRun = nil, nil
	if s.busy != "" {
		s.stopRequested = true
		s.status = "Stopping; waiting for the current worker to finish. Persistent execution was not authorized."
		if s.busy == "Executing" {
			s.status = "Stopping; waiting for the execution worker and private journal to close. Dispatched effects may have committed."
		}
	} else {
		s.status = "Pending confirmations revoked. Retained results describe historical effects only."
	}
}

func (w *Workbench) expireHelmInstallFlow(now time.Time) {
	s := &w.helmInstall
	if !s.open || s.candidate == nil {
		return
	}
	captureExpired := !s.candidate.Valid(now)
	reviewExpired := s.preparation != nil && !now.Before(s.preparation.ExpiresAt())
	if !captureExpired && !reviewExpired {
		return
	}
	s.revoke()
	s.preparation, s.dryRun = nil, nil
	if captureExpired {
		s.candidate = nil
		s.capturedRequest = helmrender.InstallRequest{}
	}
	if s.busy != "" {
		s.stopRequested = true
		s.status = "Review expired; waiting for the current worker to stop. Persistent execution was not authorized."
		if s.busy == "Executing" {
			s.status = "Review expired; waiting for the current execution to stop. Previously dispatched effects remain historical."
		}
	} else if !s.attempted {
		s.status = "Review expired. Capture or prepare again with fresh, separate confirmations."
	}
}

func (w *Workbench) captureHelmInstall() {
	w.syncHelmInstall()
	s := &w.helmInstall
	if !w.helmInstallEligible() || s.attempted || s.composing || !s.trusted || !s.capabilityAck {
		return
	}
	s.revoke()
	s.epoch++
	s.candidate, s.preparation, s.dryRun, s.execution = nil, nil, nil, nil
	s.capturedRequest = helmrender.InstallRequest{}
	s.stopRequested = false
	epoch, request := s.epoch, s.request()
	flow, flowCancel := context.WithCancel(w.operationContext())
	ctx, cancel := context.WithCancel(flow)
	s.flowCtx, s.flowCancel, s.cancel = flow, flowCancel, cancel
	s.busy, s.status = "Capturing", "Capturing the trusted chart once with the pinned isolated Helm client; no cluster requests…"
	if err := w.life.Go(func(context.Context) {
		defer cancel()
		result, err := helmrender.CaptureInstall(ctx, request)
		workerErr := ctx.Err()
		w.emit(func() {
			if !w.helmInstallCurrent(epoch) {
				flowCancel()
				return
			}
			current := &w.helmInstall
			current.busy, current.cancel = "", nil
			if current.stopRequested || workerErr != nil || flow.Err() != nil || err != nil ||
				!current.trusted || !current.capabilityAck || current.composing || current.request() != request {
				current.revoke()
				current.status = helmInstallError(err, workerErr, "Capture stopped; no candidate or cluster authority was retained.")
				return
			}
			if !result.Valid(time.Now()) || result.Namespace() != request.Namespace || result.Release() != request.Release {
				current.revoke()
				current.status = "The captured candidate expired or did not match these inputs. Explicitly capture again."
				return
			}
			current.candidate, current.capturedRequest = result, request
			bounded, boundCancel := context.WithDeadline(flow, result.ExpiresAt())
			current.flowCtx = bounded
			current.flowCancel = func() { boundCancel(); flowCancel() }
			current.status = "Captured once. Review the frozen target inventory, then separately allow current-identity preparation reads."
		})
	}); err != nil {
		s.revoke()
		s.busy = ""
		s.status = "The workspace closed before capture could start."
	}
}

func (w *Workbench) prepareHelmInstall() {
	w.syncHelmInstall()
	w.expireHelmInstallFlow(time.Now())
	s := &w.helmInstall
	if !w.helmInstallEligible() || s.attempted || s.composing || !s.prepareAck ||
		!s.inputsMatch() || !s.candidate.Valid(time.Now()) {
		return
	}
	s.revoke()
	s.epoch++ // A new explicit preparation invalidates the previous review's controls.
	s.preparation, s.dryRun = nil, nil
	s.stopRequested = false
	epoch, backend, session, candidate, request := s.epoch, s.backend, s.session, s.candidate, s.capturedRequest
	flow, flowCancel := context.WithDeadline(w.operationContext(), candidate.ExpiresAt())
	ctx, cancel := context.WithCancel(flow)
	s.flowCtx, s.flowCancel, s.cancel = flow, flowCancel, cancel
	s.busy, s.status = "Preparing", "Reading the current namespace, all release history and exact absent target identities…"
	if err := w.life.Go(func(context.Context) {
		defer cancel()
		result, err := backend.PrepareHelmInstall(ctx, candidate, session, true)
		workerErr := ctx.Err()
		w.emit(func() {
			if !w.helmInstallCurrent(epoch) {
				flowCancel()
				return
			}
			current := &w.helmInstall
			current.busy, current.cancel = "", nil
			if current.stopRequested || workerErr != nil || flow.Err() != nil || err != nil || !current.inputsMatch() ||
				current.candidate != candidate || current.capturedRequest != request {
				current.revoke()
				current.status = helmInstallError(err, workerErr, "Preparation stopped; no server dry-run or persistent request was authorized.")
				return
			}
			if !result.Matches(backend, candidate, session, time.Now()) {
				current.revoke()
				current.status = "Preparation expired before presentation; explicitly prepare again."
				return
			}
			current.preparation = result
			bounded, boundCancel := context.WithDeadline(flow, result.ExpiresAt())
			current.flowCtx = bounded
			current.flowCancel = func() { boundCancel(); flowCancel() }
			current.status = "Review the sampled namespace and frozen targets, then separately authorize sensitive server dry-run requests."
		})
	}); err != nil {
		s.revoke()
		s.busy = ""
		s.status = "The workspace closed before preparation could start."
	}
}

func (w *Workbench) preflightHelmInstall() {
	w.syncHelmInstall()
	w.expireHelmInstallFlow(time.Now())
	s := &w.helmInstall
	if !w.helmInstallEligible() || s.attempted || s.composing || !s.dryRunAck || !s.inputsMatch() ||
		s.dryRun != nil || s.flowCtx == nil || s.flowCtx.Err() != nil ||
		!s.preparation.Matches(s.backend, s.candidate, s.session, time.Now()) {
		return
	}
	s.prepareAck, s.dryRunAck, s.executeAck = false, false, false
	s.confirmation, s.confirmationComposing = "", false
	s.stopRequested = false
	epoch, backend, session, candidate, preparation, flow := s.epoch, s.backend, s.session, s.candidate, s.preparation, s.flowCtx
	ctx, cancel := context.WithCancel(flow)
	s.cancel, s.busy, s.status = cancel, "DryRun", "Checking the frozen bodies with sensitive server dry-run requests; persistent creation remains separately confirmed…"
	if err := w.life.Go(func(context.Context) {
		defer cancel()
		// Encoding and consent live in this worker. A successful origin survives
		// this worker, but never the flow's original expiry or cancellation.
		consent, err := backend.AcknowledgeHelmInstallDryRun(ctx, preparation, candidate, session, true)
		var result *kube.HelmInstallDryRun
		var stopOrigin func() bool
		if err == nil {
			stopOrigin = context.AfterFunc(flow, consent.Discard)
			result, err = backend.PreflightHelmInstall(ctx, consent, candidate, session)
		}
		discard := func() {
			consent.Discard()
			if stopOrigin != nil {
				stopOrigin()
			}
		}
		workerErr := ctx.Err()
		if err != nil || workerErr != nil {
			discard()
		}
		w.emit(func() {
			if !w.helmInstallCurrent(epoch) {
				discard()
				return
			}
			current := &w.helmInstall
			current.busy, current.cancel = "", nil
			if current.stopRequested || workerErr != nil || flow.Err() != nil || err != nil || !current.inputsMatch() ||
				current.candidate != candidate || current.preparation != preparation {
				discard()
				current.revoke()
				current.preparation, current.dryRun = nil, nil
				current.status = helmInstallError(err, workerErr, "Server dry-run stopped; persistent creation was not authorized.")
				return
			}
			if !result.Matches(backend, candidate, session, time.Now()) {
				discard()
				current.revoke()
				current.preparation, current.dryRun = nil, nil
				current.status = "Server dry-run expired before presentation; explicitly prepare again."
				return
			}
			current.dryRun = result
			current.status = "Server dry-run finished. Review persistent effects, possible partial or uncertain outcomes, and the private journal before the final confirmation."
		})
	}); err != nil {
		s.revoke()
		s.preparation, s.dryRun = nil, nil
		s.busy = ""
		s.status = "The workspace closed before server dry-run could start."
	}
}

func (w *Workbench) executeHelmInstall() {
	w.syncHelmInstall()
	w.expireHelmInstallFlow(time.Now())
	s := &w.helmInstall
	if !w.helmInstallEligible() || s.attempted || s.composing || !s.executeAck || !s.inputsMatch() ||
		s.confirmationComposing || s.candidate == nil || s.confirmation != s.candidate.Release() || s.journalPath == "" ||
		s.flowCtx == nil || s.flowCtx.Err() != nil || !s.dryRun.Matches(s.backend, s.candidate, s.session, time.Now()) {
		return
	}
	epoch, backend, session, candidate, path, dryRun, flowCancel := s.epoch, s.backend, s.session, s.candidate, s.journalPath, s.dryRun, s.flowCancel
	s.prepareAck, s.dryRunAck, s.executeAck = false, false, false
	s.confirmation, s.confirmationComposing = "", false
	s.attempted, s.stopRequested = true, false
	ctx, cancel := context.WithCancel(s.flowCtx)
	s.cancel, s.busy, s.status = cancel, "Executing", "Executing the independently confirmed creation and saving private recovery records…"
	w.pendingWrites++
	if err := w.life.Go(func(context.Context) {
		defer cancel()
		result, err := executeHelmInstallJournal(ctx, path, backend, dryRun, candidate, session)
		// Close, grant revocation and flow revocation must finish even when a
		// closed window drops this callback. Historical effects survive all of them.
		flowCancel()
		w.emit(func() {
			w.pendingWrites--
			if !w.helmInstallCurrent(epoch) {
				return
			}
			current := &w.helmInstall
			current.revoke()
			current.busy = ""
			current.preparation, current.dryRun = nil, nil
			current.execution = result
			current.status = "Execution attempt finished. Review server effects and journal completion separately."
			if err != nil {
				current.status = helmInstallError(err, nil, "Execution stopped; inspect recorded effects before any further action.")
			}
			if result != nil {
				current.status = fmt.Sprintf("Server outcome: %s. %s", result.Outcome(), current.status)
			}
		})
	}); err != nil {
		w.pendingWrites--
		s.revoke()
		s.preparation, s.dryRun = nil, nil
		s.busy = ""
		s.status = "The workspace closed before execution could start. This confirmation remains consumed."
	}
}

// This explicit action reuses mixed offline history; it grants no current GETs
// and cannot restore execution authority from any retained record.
func (w *Workbench) openHelmInstallHistory() {
	w.syncHelmInstall()
	if !w.helmInstallEligible() || !w.helmInstall.attempted {
		return
	}
	id := w.helmInstall.execution.JournalID()
	w.openHelmRecovery()
	w.loadHelmRecoverySelection(id)
}

// Never expose arbitrary transport errors, paths, chart content or responses.
func helmInstallError(err, ctxErr error, fallback string) string {
	switch {
	case errors.Is(err, helmjournal.ErrUnresolved):
		return "Retained unresolved history blocks another execution. Review local history and separately observe current metadata; this does not clear the block."
	case errors.Is(err, helmjournal.ErrCorrupt):
		return "Private history is damaged. Retain the records; further execution is blocked."
	case errors.Is(err, helmjournal.ErrBusy):
		return "The private journal is in use. Wait for its current operation to finish."
	case errors.Is(err, helmjournal.ErrUnavailable), errors.Is(err, helmjournal.ErrClosed), errors.Is(err, helmjournal.ErrBudget), errors.Is(err, kube.ErrHelmExecutionJournal):
		return "Private recovery records could not be safely opened or saved. Review the separately reported server outcome."
	case errors.Is(err, context.DeadlineExceeded), errors.Is(ctxErr, context.DeadlineExceeded):
		return "The bounded installation stage timed out. " + fallback
	case errors.Is(err, context.Canceled), errors.Is(ctxErr, context.Canceled):
		return fallback
	}
	for _, safe := range []error{helmrender.ErrInput, helmrender.ErrDistribution, helmrender.ErrRender,
		helmrender.ErrOutput, helmrender.ErrBusy, helmrelease.ErrInstallCapture, helmrelease.ErrInstallConsent,
		helmrelease.ErrInstallStale, helmrelease.ErrInstallConflict, helmrelease.ErrInstallCodec,
		kube.ErrHelmDryRunConsent, kube.ErrHelmDryRunRejected, kube.ErrHelmDryRunResponse,
		kube.ErrHelmInstallExecutionBudget, kube.ErrHelmInstallExecutionConsent, kube.ErrHelmInstallExecutionResponse,
		kube.ErrHelmInstallExecutionChanged, kube.ErrHelmInstallExecutionRejected, kube.ErrHelmInstallExecutionUnknown} {
		if errors.Is(err, safe) {
			return safe.Error()
		}
	}
	if err != nil {
		return "The bounded installation stage did not qualify or finish. Review the selected identity and any recorded effects before a new explicit review."
	}
	return fallback
}
