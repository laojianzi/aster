package uiworkbench

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/kube"
)

const helmArchiveReviewBusy = "Reviewing local archive"
const helmArchiveAckLabel = "Retain this original record in the local archive; free active quota, not disk space"

func (w *Workbench) prepareHelmArchive() {
	w.syncHelmUninstall()
	u := &w.helmUninstall
	if !w.helmUninstallEligible() || u.mode != "history" || u.recovery == nil || u.recoveryErr != nil ||
		u.recovery.Archived() || u.archiveReload || u.journalPath == "" {
		return
	}
	u.revoke()
	u.archiveResult = nil
	u.stopRequested = false
	epoch, id, path, history := u.epoch, u.selectedID, u.journalPath, u.recovery
	ctx, cancel := context.WithCancel(w.operationContext())
	u.cancel, u.busy, u.status = cancel, "Preparing local archive", "Reviewing the exact loaded local record; no cluster request is authorized."
	w.run(func(context.Context) {
		defer cancel()
		session, err := kube.StartHelmJournalArchive(ctx, path, history)
		if err != nil {
			w.emit(func() {
				if !w.helmUninstallCurrent(epoch) || w.helmUninstall.selectedID != id || w.helmUninstall.recovery != history {
					return
				}
				x := &w.helmUninstall
				x.busy, x.cancel = "", nil
				x.status = helmArchiveError(err)
			})
			return
		}
		defer session.Cancel()
		w.emit(func() {
			if !w.helmUninstallCurrent(epoch) || w.helmUninstall.selectedID != id || w.helmUninstall.recovery != history ||
				w.helmUninstall.stopRequested || ctx.Err() != nil {
				session.Cancel()
				return
			}
			x := &w.helmUninstall
			x.archive = session
			x.busy, x.status = helmArchiveReviewBusy, "Review retained history and separately confirm the exact record ID. This review holds the journal lock briefly."
		})
		<-session.Done() // The Store is already closed, even when emit drops callbacks.
		result, resultErr, _ := session.Result()
		w.emit(func() {
			if !w.helmUninstallCurrent(epoch) || w.helmUninstall.selectedID != id || w.helmUninstall.recovery != history {
				return
			}
			x := &w.helmUninstall
			x.archive, x.cancel, x.busy = nil, nil, ""
			x.archiveAck, x.archiveComposing, x.archiveConfirmation = false, false, ""
			x.archiveResult = &result
			x.archiveReload = true // A new review requires a fresh read, never this old snapshot.
			x.admission = nil
			x.status = fmt.Sprintf("Local archive outcome: %s. Original bytes remain retained. Reload local history before another archive review.", result.Outcome)
			if resultErr != nil {
				x.status += " " + helmArchiveError(resultErr)
			}
		})
	})
}

func (w *Workbench) helmArchiveCanConfirm() bool {
	u := &w.helmUninstall
	return w.helmUninstallScopeMatches() && w.ctx.Err() == nil && w.pendingWrites == 0 &&
		u.mode == "history" && u.busy == helmArchiveReviewBusy && !u.stopRequested && u.archive != nil &&
		u.recovery != nil && u.selectedID == u.recovery.ID() && u.archive.ID() == u.selectedID && u.archive.MatchesHistory(u.recovery) &&
		time.Now().Before(u.archive.ExpiresAt())
}
func (w *Workbench) confirmHelmArchive() {
	w.syncHelmUninstall()
	u := &w.helmUninstall
	if !w.helmArchiveCanConfirm() || !u.archiveAck || u.archiveComposing || u.archiveConfirmation != u.selectedID {
		return
	}
	err := u.archive.Confirm(u.archiveConfirmation, true)
	u.archiveAck, u.archiveConfirmation, u.archiveComposing = false, "", false
	u.archiveReload = true
	u.busy = "Archiving local history"
	u.status = "Archiving only this checked local record; an uncertain storage result will not be retried."
	if err != nil {
		u.archive.Cancel()
		u.status = helmArchiveError(err)
	}
}

func helmArchiveError(err error) string {
	switch {
	case errors.Is(err, helmjournal.ErrBudget):
		return "The bounded archive or journal capacity is exhausted. Originals remain retained; no automatic deletion is available."
	case errors.Is(err, helmjournal.ErrArchiveConsent):
		return "Archive confirmation or the loaded file identity is stale, expired or already consumed. Reload history and review again."
	case errors.Is(err, helmjournal.ErrUnresolved):
		return "This record still has unresolved effects and cannot be archived. Current observations cannot clear that protection."
	case errors.Is(err, context.DeadlineExceeded):
		return "The archive review expired and its journal lock was released."
	case errors.Is(err, context.Canceled):
		return "The archive session was cancelled. A separately verified Archived outcome is not undone."
	default:
		return helmUninstallError(err, nil, "The local archive operation did not qualify. Retain the original history.")
	}
}

func (w *Workbench) helmArchiveView(c *ui.Context) {
	u := &w.helmUninstall
	r := u.recovery
	if r == nil {
		return
	}
	location := "Active"
	if r.Archived() {
		location = "Archived"
	}
	ui.Text(c, "Record location: "+location+" · original bytes retained").Label("Helm archive location").FontSize(12)
	ui.Text(c, "Archival only frees active journal quota. It does not delete disk bytes, clear Unknown, or authorize any cluster request.").Label("Helm archive boundary").FontSize(11)
	if result := u.archiveResult; result != nil {
		ui.Text(c, "Local archive outcome: "+string(result.Outcome)).Label("Helm archive outcome").Bold().FontSize(13)
	}
	if u.archiveReload {
		ui.Text(c, "Reload local history before a new archive review. The selected history remains a pre-operation snapshot.").Label("Helm archive reload needed").FontSize(11)
	}
	ui.Button(c, "Review local archive").Disabled(!w.helmUninstallEligible() || r.Archived() || r.Corrupt() || u.recoveryErr != nil || u.archiveReload || u.journalPath == "").OnClick(w.prepareHelmArchive)
	if session := u.archive; session != nil {
		inv := session.Inventory()
		ui.Text(c, fmt.Sprintf("Active: %d / %d records · %d / %d bytes", inv.Records, helmjournal.MaxRecords, inv.Bytes, helmjournal.MaxStoreBytes)).Label("Helm archive active capacity").FontSize(11)
		ui.Text(c, fmt.Sprintf("Archived: %d / %d records · %d / %d bytes; this record retains %d bytes", inv.ArchivedRecords, helmjournal.MaxArchivedRecords, inv.ArchivedBytes, helmjournal.MaxArchivedBytes, session.Bytes())).Label("Helm archive retained capacity").FontSize(11)
		ui.Text(c, "Archive review expires: "+session.ExpiresAt().UTC().Format("15:04:05 UTC")).Label("Helm archive expiry").FontSize(11)
		if d := time.Until(session.ExpiresAt()); d > 0 {
			c.After(d)
		}
		editable := w.helmArchiveCanConfirm()
		guardedCheckbox(c, fmt.Sprintf("helm.archive.ack.%d.%s", u.epoch, u.selectedID), &u.archiveAck, helmArchiveAckLabel, editable).Label("Helm archive acknowledgement")
		u.archiveComposing = guardedTextInput(c, fmt.Sprintf("helm.archive.id.%d.%s", u.epoch, u.selectedID), &u.archiveConfirmation, editable).Label("Helm archive exact ID").Placeholder("Type exact record ID: " + u.selectedID).Composing()
		ui.Button(c, "Archive this local record").Disabled(!editable || !u.archiveAck || u.archiveComposing || u.archiveConfirmation != u.selectedID).OnClick(w.confirmHelmArchive)
	}
}
