package uiworkbench

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
)

const (
	helmPrepareConsentLabel  = "Allow sensitive release reads and restricted uninstall checks"
	helmDryRunConsentLabel   = "Allow server dry-run PATCH and DELETE with Orphan policy"
	helmExecuteConsentLabel  = "Delete reviewed ConfigMaps, retain release history, and save private recovery records"
	helmRecoveryConsentLabel = "Allow current metadata GETs for this recovery record"
)

// Only the owning UI goroutine mutates this state. Workers capture exact inputs;
// journal handles and execution grants never cross back through a UI callback.
type helmUninstallState struct {
	open                                            bool
	mode                                            string
	epoch, contextEpoch                             uint64
	backend                                         *kube.Backend
	session, namespace, contextName, journalPath    string
	row                                             helmrelease.Revision
	busy, status                                    string
	cancel                                          context.CancelFunc
	stopRequested, attempted                        bool
	prepareAck, dryRunAck, executeAck, reconcileAck bool
	confirmation                                    string
	confirmationComposing                           bool
	preparation                                     *kube.HelmUninstallPreparation
	dryRunConsent                                   *kube.HelmUninstallDryRunConsent
	dryRunParentStop                                func() bool
	dryRun                                          *kube.HelmUninstallDryRun
	execution                                       *kube.HelmUninstallExecution
	history                                         []helmJournalEntry
	selectedID                                      string
	recovery                                        *kube.HelmUninstallRecovery
	recoveryErr                                     error
	observation                                     *kube.HelmUninstallRecoveryObservation
	reconcileConsent                                *kube.HelmUninstallReconciliationConsent
	admission                                       *kube.HelmJournalAdmissionReview
}

func (u *helmUninstallState) revoke() {
	if u.cancel != nil {
		u.cancel()
	}
	if u.dryRunConsent != nil {
		u.dryRunConsent.Discard()
	}
	if u.dryRunParentStop != nil {
		u.dryRunParentStop()
		u.dryRunParentStop = nil
	}
	if u.reconcileConsent != nil {
		u.reconcileConsent.Discard()
	}
	u.prepareAck, u.dryRunAck, u.executeAck, u.reconcileAck = false, false, false, false
	u.confirmation, u.confirmationComposing = "", false
	u.admission = nil
}

func (w *Workbench) clearHelmUninstall() {
	u := &w.helmUninstall
	u.revoke()
	w.helmUninstall = helmUninstallState{epoch: u.epoch + 1}
}

func (w *Workbench) helmUninstallScopeMatches() bool {
	u := &w.helmUninstall
	if !u.open || u.backend != w.backend || u.contextEpoch != w.contextEpoch ||
		u.session != w.sessionID || u.namespace != strings.TrimSpace(w.namespace) ||
		u.contextName != w.currentContext || u.journalPath != w.helmJournalPath {
		return false
	}
	if u.row.StorageName != "" {
		r := &w.helmReleases
		if !r.open || r.backend != u.backend || r.contextEpoch != u.contextEpoch ||
			r.session != u.session || r.namespace != u.namespace || r.selected != u.row {
			return false
		}
	}
	return true
}

func (w *Workbench) syncHelmUninstall() {
	if !w.helmUninstall.open {
		return
	}
	if !w.helmUninstallScopeMatches() || w.ctx.Err() != nil ||
		(w.helmUninstall.backend != nil && !w.credentialExpiry.IsZero() && !time.Now().Before(w.credentialExpiry)) {
		w.clearHelmUninstall()
	}
}

func (w *Workbench) helmUninstallEligible() bool {
	return w.helmUninstallScopeMatches() && w.helmUninstall.busy == "" &&
		w.pendingWrites == 0 && w.life != nil && w.ctx.Err() == nil
}

func (w *Workbench) helmUninstallCurrent(epoch uint64) bool {
	return w.helmUninstall.epoch == epoch && w.helmUninstallScopeMatches()
}

func (w *Workbench) openHelmUninstall() {
	w.syncHelmReleases()
	r := &w.helmReleases
	if !r.open || r.busy || r.inspecting || w.backend == nil || w.pendingWrites != 0 ||
		!helmrelease.ValidSelection(r.namespace, r.selected) || r.selected.Status != "deployed" {
		return
	}
	row := r.selected
	w.clearHelmInspection()
	r.selected = row // Keep only the current metadata selection, never read consent.
	w.helmUninstall = helmUninstallState{open: true, mode: "uninstall", epoch: w.helmUninstall.epoch,
		backend: w.backend, contextEpoch: w.contextEpoch, session: w.sessionID,
		namespace: strings.TrimSpace(w.namespace), contextName: w.currentContext,
		journalPath: w.helmJournalPath, row: row,
		status: "Review the restricted profile, then explicitly allow preparation reads."}
}

// This entry is available while disconnected. Merely opening it performs no
// filesystem, kubeconfig or network operation. Reload is a separate local action.
func (w *Workbench) openHelmRecovery() {
	if w.pendingWrites != 0 || w.detail != nil || w.delivery.open {
		return
	}
	w.syncHelmReleases()
	r := &w.helmReleases
	row := helmrelease.Revision{}
	if r.open && !r.busy && !r.inspecting && r.backend == w.backend && r.session == w.sessionID &&
		r.namespace == strings.TrimSpace(w.namespace) {
		row = r.selected
	}
	w.clearHelmInspection()
	r.selected = row
	w.helmUninstall = helmUninstallState{open: true, mode: "history", epoch: w.helmUninstall.epoch,
		backend: w.backend, contextEpoch: w.contextEpoch, session: w.sessionID,
		namespace: strings.TrimSpace(w.namespace), contextName: w.currentContext,
		journalPath: w.helmJournalPath, row: row,
		status: "Choose Reload local history to read private records. No cluster read is automatic."}
}

func (w *Workbench) stopHelmUninstall() {
	u := &w.helmUninstall
	if !u.open {
		return
	}
	u.revoke()
	u.preparation, u.dryRun = nil, nil
	if u.busy != "" {
		u.stopRequested = true
		u.status = "Stopping; waiting for the current worker to finish and release its journal. Dispatched effects may have committed."
		if u.busy == helmAdmissionBusy {
			u.status = "Stopping the local journal check; waiting for its worker to release the private journal."
		}
	} else {
		u.status = "Pending confirmations revoked. Retained results describe historical effects only."
	}
}

func (w *Workbench) expireHelmUninstallFlow(now time.Time) {
	u := &w.helmUninstall
	if !u.open || u.preparation == nil || now.Before(u.preparation.ExpiresAt()) {
		return
	}
	u.revoke()
	u.preparation, u.dryRun = nil, nil
	if u.busy != "" {
		u.stopRequested = true
		u.status = "Review expired; waiting for the current operation to stop. Previously dispatched effects remain historical."
	} else if u.execution == nil {
		u.status = "Review expired. A new preparation and separate confirmations are required."
	}
}

func (w *Workbench) prepareHelmUninstall() {
	w.syncHelmUninstall()
	u := &w.helmUninstall
	if !w.helmUninstallEligible() || u.mode != "uninstall" || !u.prepareAck || u.attempted || u.backend == nil {
		return
	}
	u.revoke()
	u.preparation, u.dryRun, u.dryRunConsent = nil, nil, nil
	u.stopRequested = false
	epoch, backend, session, ns, row := u.epoch, u.backend, u.session, u.namespace, u.row
	ctx, cancel := context.WithCancel(w.operationContext())
	u.cancel, u.busy, u.status = cancel, "Preparing", "Reading and checking the exact selected revision and bounded ConfigMap identities…"
	w.run(func(context.Context) {
		defer cancel()
		result, err := backend.PrepareHelmUninstall(ctx, ns, row, session, true)
		workerErr := ctx.Err()
		w.emit(func() {
			if !w.helmUninstallCurrent(epoch) {
				return
			}
			current := &w.helmUninstall
			current.busy, current.cancel = "", nil
			if current.stopRequested || workerErr != nil || w.operationContext().Err() != nil || err != nil {
				current.status = helmUninstallError(err, workerErr, "Preparation stopped; no persistent request was authorized.")
				return
			}
			if !result.Matches(backend, session, ns, row, time.Now()) {
				current.status = "Preparation expired before presentation; explicitly prepare again."
				return
			}
			current.preparation = result
			current.status = "Review every target identity, then separately authorize server dry-run."
		})
	})
}

func (w *Workbench) preflightHelmUninstall() {
	w.syncHelmUninstall()
	w.expireHelmUninstallFlow(time.Now())
	u := &w.helmUninstall
	if !w.helmUninstallEligible() || u.mode != "uninstall" || !u.dryRunAck || u.attempted ||
		!u.preparation.Matches(u.backend, u.session, u.namespace, u.row, time.Now()) {
		return
	}
	u.dryRunAck, u.executeAck, u.confirmation, u.confirmationComposing = false, false, "", false
	if u.dryRunConsent != nil {
		u.dryRunConsent.Discard()
	}
	if u.dryRunParentStop != nil {
		u.dryRunParentStop()
	}
	u.dryRun = nil
	consent, err := u.backend.AcknowledgeHelmUninstallDryRun(u.preparation, u.session, u.namespace, u.row, true)
	if err != nil {
		u.status = helmUninstallError(err, nil, "Dry-run confirmation was rejected.")
		return
	}
	u.dryRunConsent, u.stopRequested = consent, false
	// The successful dry-run survives its own worker, but never the current
	// connection/application context, even if no UI completion can be delivered.
	stopParent := context.AfterFunc(w.operationContext(), consent.Discard)
	u.dryRunParentStop = stopParent
	epoch, backend, session, ns, row := u.epoch, u.backend, u.session, u.namespace, u.row
	ctx, cancel := context.WithCancel(w.operationContext())
	u.cancel, u.busy, u.status = cancel, "DryRun", "Checking bounded server dry-run admission; persistent execution requires a separate confirmation…"
	w.run(func(context.Context) {
		defer cancel()
		result, err := backend.PreflightHelmUninstall(ctx, consent, session, ns, row)
		workerErr := ctx.Err()
		// Keep a successful grant alive for its one independent execution mint.
		// UI lifecycle revocation and the original expiry remain authoritative.
		if err != nil || workerErr != nil {
			consent.Discard()
			stopParent()
		}
		w.emit(func() {
			if !w.helmUninstallCurrent(epoch) {
				consent.Discard()
				return
			}
			current := &w.helmUninstall
			current.busy, current.cancel = "", nil
			if current.stopRequested || workerErr != nil || w.operationContext().Err() != nil || err != nil {
				consent.Discard()
				current.status = helmUninstallError(err, workerErr, "Dry-run stopped; persistent execution was not authorized.")
				return
			}
			if !result.Matches(backend, session, ns, row, time.Now()) {
				consent.Discard()
				current.status = "Dry-run review expired; explicitly prepare again."
				return
			}
			current.dryRun = result
			current.status = "Dry-run finished. Review retained history, orphan consequences and the private journal before confirming execution."
		})
	})
}

func (w *Workbench) executeHelmUninstall() {
	w.syncHelmUninstall()
	w.expireHelmUninstallFlow(time.Now())
	u := &w.helmUninstall
	if !w.helmUninstallEligible() || u.mode != "uninstall" || u.attempted || !u.executeAck ||
		u.confirmationComposing || u.confirmation != u.row.Release || u.journalPath == "" ||
		!u.dryRun.Matches(u.backend, u.session, u.namespace, u.row, time.Now()) {
		return
	}
	epoch, backend, session, ns, row, path, dryRun := u.epoch, u.backend, u.session, u.namespace, u.row, u.journalPath, u.dryRun
	u.admission = nil
	grant := u.dryRunConsent
	stopParent := u.dryRunParentStop
	u.prepareAck, u.dryRunAck, u.executeAck = false, false, false
	u.confirmation, u.confirmationComposing = "", false
	u.attempted, u.stopRequested = true, false
	ctx, cancel := context.WithCancel(w.operationContext())
	u.cancel, u.busy, u.status = cancel, "Executing", "Executing the independently confirmed effects and saving private recovery records…"
	w.pendingWrites++
	w.run(func(context.Context) {
		defer cancel()
		defer grant.Discard()
		if stopParent != nil {
			defer stopParent()
		}
		result, err := executeHelmJournal(ctx, path, backend, dryRun, session, ns, row)
		// The helper has already closed the journal, even if this window no longer
		// accepts callbacks. A live workspace always settles accounting first.
		w.emit(func() {
			w.pendingWrites--
			if !w.helmUninstallCurrent(epoch) {
				return
			}
			current := &w.helmUninstall
			current.busy, current.cancel = "", nil
			current.preparation, current.dryRun = nil, nil
			current.execution = result
			current.status = "Execution attempt finished. Review server effects and journal completion separately."
			if err != nil {
				current.status = helmUninstallError(err, nil, "Execution stopped; inspect recorded effects before any further action.")
			}
			if result != nil {
				current.status = fmt.Sprintf("Server outcome: %s. %s", result.Outcome(), current.status)
			}
		})
	})
}

func (w *Workbench) loadHelmRecovery() {
	w.loadHelmRecoverySelection("")
}

// A blocker link uses the existing bounded history load and details. It does
// not retain an execution acknowledgement or mint a current-observation grant.
func (w *Workbench) loadHelmRecoverySelection(selectedID string) {
	w.syncHelmUninstall()
	u := &w.helmUninstall
	if !w.helmUninstallEligible() || u.mode != "history" || u.journalPath == "" {
		return
	}
	u.revoke()
	u.stopRequested = false
	u.history, u.recovery, u.recoveryErr, u.observation = nil, nil, nil, nil
	u.selectedID = ""
	epoch, path := u.epoch, u.journalPath
	ctx, cancel := context.WithCancel(w.ctx) // Offline: deliberately not connectionCtx.
	u.cancel, u.busy, u.status = cancel, "Loading history", "Reading bounded private local history; no cluster requests…"
	w.run(func(context.Context) {
		defer cancel()
		entries, err := loadHelmJournalHistory(ctx, path)
		workerErr := ctx.Err()
		w.emit(func() {
			if !w.helmUninstallCurrent(epoch) {
				return
			}
			current := &w.helmUninstall
			current.busy, current.cancel = "", nil
			if current.stopRequested || workerErr != nil || err != nil {
				current.status = helmUninstallError(err, workerErr, "Local history read stopped.")
				return
			}
			current.history = entries
			current.status = fmt.Sprintf("%d local record(s). Select a record to review immutable history.", len(entries))
			if selectedID != "" {
				for _, entry := range entries {
					if entry.id == selectedID {
						w.selectHelmRecovery(selectedID)
						return
					}
				}
				current.status = "The previously reported record is no longer in this local history. No observation was authorized."
			}
		})
	})
}

func (w *Workbench) selectHelmRecovery(id string) {
	w.syncHelmUninstall()
	u := &w.helmUninstall
	if !w.helmUninstallEligible() || u.mode != "history" {
		return
	}
	for _, entry := range u.history {
		if entry.id != id {
			continue
		}
		u.revoke()
		u.selectedID, u.recovery, u.recoveryErr = entry.id, entry.recovery, entry.err
		u.observation = nil
		u.status = "Local history selected. Current cluster facts require a new metadata selection and explicit GET confirmation."
		if entry.err != nil || entry.recovery == nil || entry.recovery.Corrupt() {
			u.status = "Damaged record: only the checked prefix can be shown. Network observation is unavailable; records remain retained."
		}
		return
	}
}

func (w *Workbench) observeHelmRecovery() {
	w.syncHelmUninstall()
	u := &w.helmUninstall
	if !w.helmUninstallEligible() || u.mode != "history" || !u.reconcileAck || u.backend == nil ||
		u.recovery == nil || u.recoveryErr != nil || u.recovery.Corrupt() || !helmrelease.ValidSelection(u.namespace, u.row) {
		return
	}
	u.reconcileAck = false
	u.observation = nil
	consent, err := u.backend.AcknowledgeHelmUninstallReconciliation(u.recovery, u.session, u.namespace, u.row, true)
	if err != nil {
		u.status = helmUninstallError(err, nil, "The current connection or selected revision does not match this history.")
		return
	}
	u.reconcileConsent, u.stopRequested = consent, false
	epoch, id, backend, session, ns, row := u.epoch, u.selectedID, u.backend, u.session, u.namespace, u.row
	ctx, cancel := context.WithCancel(w.operationContext())
	u.cancel, u.busy, u.status = cancel, "Observing", "Reading current metadata with the new confirmation; historical outcomes remain unchanged…"
	w.run(func(context.Context) {
		defer cancel()
		defer consent.Discard()
		result, err := backend.ObserveHelmUninstallRecovery(ctx, consent, session, ns, row)
		workerErr := ctx.Err()
		w.emit(func() {
			if !w.helmUninstallCurrent(epoch) || w.helmUninstall.selectedID != id {
				return
			}
			current := &w.helmUninstall
			current.busy, current.cancel = "", nil
			if current.stopRequested || workerErr != nil || w.operationContext().Err() != nil || err != nil {
				current.status = helmUninstallError(err, workerErr, "Current metadata observation stopped; no partial result retained.")
				return
			}
			current.observation = result
			current.status = "Current metadata sampled. Historical outcomes and unresolved-operation protection remain unchanged."
		})
	})
}

// Backend/journal errors are intentionally fixed. Do not surface raw transport,
// filesystem paths, credential material, release contents or composed errors.
func helmUninstallError(err, ctxErr error, fallback string) string {
	switch {
	case errors.Is(err, helmjournal.ErrUnresolved):
		return "Retained unresolved history blocks another execution. Review local history and explicitly observe current metadata; this does not clear the block."
	case errors.Is(err, helmjournal.ErrCorrupt):
		return "Private history is damaged. Retain the records; further execution is blocked."
	case errors.Is(err, helmjournal.ErrBusy):
		return "The private journal is in use. Wait for its current operation to finish."
	case errors.Is(err, helmjournal.ErrUnavailable), errors.Is(err, helmjournal.ErrClosed), errors.Is(err, helmjournal.ErrBudget), errors.Is(err, kube.ErrHelmExecutionJournal):
		return "Private recovery records could not be safely opened or saved. Review the separately reported server outcome."
	case errors.Is(err, context.DeadlineExceeded), errors.Is(ctxErr, context.DeadlineExceeded):
		return "The bounded operation timed out. Dispatched effects may have committed; inspect history."
	case errors.Is(err, context.Canceled), errors.Is(ctxErr, context.Canceled):
		return fallback
	}
	// Return the fixed sentinel's message, never the possibly wrapped error.
	for _, safe := range []error{helmrelease.ErrPreparationProfile, helmrelease.ErrPreparationStale,
		helmrelease.ErrPreparationConsent, kube.ErrHelmDryRunConsent, kube.ErrHelmDryRunRejected,
		kube.ErrHelmDryRunResponse, kube.ErrHelmExecutionConsent, kube.ErrHelmExecutionChanged,
		kube.ErrHelmExecutionUnknown, kube.ErrHelmExecutionPending, kube.ErrHelmExecutionRejected,
		kube.ErrHelmExecutionResponse, kube.ErrHelmReconciliationConsent,
		kube.ErrHelmReconciliationChanged, kube.ErrHelmReconciliationResponse, kube.ErrHelmReconciliationBudget} {
		if errors.Is(err, safe) {
			return safe.Error()
		}
	}
	if err != nil {
		return "The bounded Helm operation did not qualify or finish. Review the selected identity and any recorded effects before a new explicit review."
	}
	return fallback
}
