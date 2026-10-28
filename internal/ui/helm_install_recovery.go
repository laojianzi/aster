package uiworkbench

import "context"

// Installation recovery has no pre-create storage UID and may have no current
// Helm revision at all. Its new read confirmation is bound to the exact loaded
// record, Backend, session and namespace, independently of execution consent.
func (w *Workbench) observeHelmInstallRecovery() {
	w.syncHelmUninstall()
	u := &w.helmUninstall
	if !w.helmUninstallEligible() || u.mode != "history" || !u.reconcileAck || u.backend == nil ||
		u.installRecovery == nil || u.recoveryErr != nil || u.installRecovery.Corrupt() ||
		u.selectedID != u.installRecovery.ID() {
		return
	}
	u.reconcileAck = false
	u.installObservation = nil
	consent, err := u.backend.AcknowledgeHelmInstallReconciliation(u.installRecovery, u.session, u.namespace, true)
	if err != nil {
		u.status = helmUninstallError(err, nil, "The current connection or namespace does not match this installation history.")
		return
	}
	u.installReconcileConsent, u.stopRequested = consent, false
	epoch, id, history := u.epoch, u.selectedID, u.installRecovery
	backend, session, ns := u.backend, u.session, u.namespace
	ctx, cancel := context.WithCancel(w.operationContext())
	stop := context.AfterFunc(ctx, consent.Discard)
	u.cancel, u.busy, u.status = cancel, "Observing", "Reading current installation metadata with the new confirmation; historical outcomes remain unchanged…"
	w.run(func(context.Context) {
		defer cancel()
		defer stop()
		defer consent.Discard()
		result, err := backend.ObserveHelmInstallRecovery(ctx, consent, history, session, ns)
		workerErr := ctx.Err()
		w.emit(func() {
			if !w.helmUninstallCurrent(epoch) || w.helmUninstall.selectedID != id ||
				w.helmUninstall.installRecovery != history {
				return
			}
			current := &w.helmUninstall
			current.busy, current.cancel, current.installReconcileConsent = "", nil, nil
			if current.stopRequested || workerErr != nil || w.operationContext().Err() != nil || err != nil {
				current.status = helmUninstallError(err, workerErr, "Current installation metadata observation stopped; no partial result retained.")
				return
			}
			current.installObservation = result
			current.status = "Current installation metadata sampled. Historical outcomes and unresolved-operation protection remain unchanged."
		})
	})
}
