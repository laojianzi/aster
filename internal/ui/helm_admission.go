package uiworkbench

import (
	"context"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
)

const (
	helmAdmissionBusy       = "Checking local journal"
	helmAdmissionCheckLabel = "Check local journal admission"
)

// The current metadata selection supplies only the journal's identity scope.
// This does not establish a supported uninstall profile or refresh any consent.
func (w *Workbench) helmAdmissionSelection() bool {
	u := &w.helmUninstall
	return u.backend != nil && helmrelease.ValidSelection(u.namespace, u.row)
}

func (w *Workbench) reviewHelmAdmission() {
	w.syncHelmUninstall()
	w.expireHelmUninstallFlow(time.Now())
	u := &w.helmUninstall
	if !w.helmUninstallEligible() || !w.helmAdmissionSelection() || u.journalPath == "" {
		return
	}
	// A failed or interrupted new check must not leave an earlier NoBlocker
	// snapshot looking current. Preparation and dry-run grants are untouched.
	u.admission = nil
	u.stopRequested = false
	epoch, backend, path, session, ns, row := u.epoch, u.backend, u.journalPath, u.session, u.namespace, u.row
	ctx, cancel := context.WithCancel(w.operationContext())
	u.cancel, u.busy, u.status = cancel, helmAdmissionBusy, "Checking existing private journal records locally; no cluster request or new record is authorized…"
	w.run(func(context.Context) {
		defer cancel()
		review, _ := backend.ReviewHelmUninstallJournalAdmission(ctx, path, session, ns, row)
		workerErr := ctx.Err()
		// The backend's existing-only reader has released its handle before a
		// callback is queued. Dropping this callback cannot retain the disk lock.
		w.emit(func() {
			if !w.helmUninstallCurrent(epoch) {
				return
			}
			w.syncHelmUninstall()
			w.expireHelmUninstallFlow(time.Now())
			if !w.helmUninstallCurrent(epoch) {
				return
			}
			current := &w.helmUninstall
			current.busy, current.cancel = "", nil
			if current.stopRequested || workerErr != nil || w.operationContext().Err() != nil {
				current.admission = nil
				current.status = "Local journal check stopped. No execution permission was granted."
				return
			}
			if review == nil || !review.Matches(backend, path, session, ns, row) {
				current.admission = nil
				current.status = "Local journal check is unavailable for this selection. No execution permission was granted."
				return
			}
			// A valid report can accompany a fixed blocker error. Display its
			// bounded reason, never a wrapped filesystem or transport error.
			current.admission = review
			current.status = helmAdmissionReasonText(review.Report())
		})
	})
}

func (w *Workbench) openHelmAdmissionHistory(id string) {
	w.syncHelmUninstall()
	u := &w.helmUninstall
	if !w.helmUninstallEligible() || !helmAdmissionRecordID(id) || u.admission == nil ||
		u.admission.Report().RecordID != id ||
		!u.admission.Matches(u.backend, u.journalPath, u.session, u.namespace, u.row) {
		return
	}
	w.openHelmRecovery()
	if w.helmUninstall.open && w.helmUninstall.mode == "history" {
		w.loadHelmRecoverySelection(id)
	}
}

func helmAdmissionRecordID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func helmAdmissionReasonText(r helmjournal.Admission) string {
	switch r.Reason {
	case helmjournal.AdmissionNoBlocker:
		return "No local journal blocker was observed in this check."
	case helmjournal.AdmissionNotInitialized:
		return "The local journal is not initialized; this check created nothing and did not establish admission."
	case helmjournal.AdmissionBusy:
		return "The local journal is in use; its records could not be checked."
	case helmjournal.AdmissionClosed:
		return "The local journal reader is closed; admission was not checked."
	case helmjournal.AdmissionInvalid:
		return "The local journal check could not validate its selection or private location."
	case helmjournal.AdmissionUnavailable:
		return "The private journal could not be safely inspected; admission was not established."
	case helmjournal.AdmissionCorrupt:
		return "Damaged or unclassifiable local history blocks new execution in this store."
	case helmjournal.AdmissionArchiveLimit:
		return "The retained archive exceeds its independent capacity limit; no record will be discarded automatically."
	case helmjournal.AdmissionRecordLimit:
		return "The local journal record limit leaves no slot for a new operation."
	case helmjournal.AdmissionOperationBytes:
		return "An existing journal record exceeds the per-operation byte limit."
	case helmjournal.AdmissionStoreBytes:
		return "The local journal exceeds its total retained-byte limit."
	case helmjournal.AdmissionReserveBytes:
		return "The local journal has insufficient space for the required 256 KiB operation reservation."
	case helmjournal.AdmissionUnresolved:
		switch r.UnresolvedKind {
		case helmjournal.UnresolvedUnclosed:
			return "A matching record has no checked terminal frame and blocks another execution."
		case helmjournal.UnresolvedUnknown:
			return "A matching record contains an Unknown effect and blocks another execution."
		case helmjournal.UnresolvedPending:
			return "A matching record contains an accepted pending effect and blocks another execution."
		case helmjournal.UnresolvedPartial:
			return "A matching record contains only partially completed effects and blocks another execution."
		default:
			return "A matching unresolved record blocks another execution."
		}
	case helmjournal.AdmissionCancelled:
		return "The local journal check was cancelled; admission was not established."
	default:
		return "The local journal check did not establish admission."
	}
}
