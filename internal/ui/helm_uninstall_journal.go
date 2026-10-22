package uiworkbench

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
)

// The desktop uses one stable private directory across workspaces and restarts.
// Merely constructing the workbench does not create or open it. There is no
// alternate-directory retry that could bypass an unresolved historical record.
func defaultHelmJournalPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", helmjournal.ErrUnavailable
	}
	return filepath.Join(home, ".aster-helm-uninstall"), nil
}

type helmJournalEntry struct {
	id       string
	recovery *kube.HelmUninstallRecovery
	err      error
}

// Disk work belongs to a worker, including opening and closing the OS lock.
// Closing must not depend on a UI callback: a closed window drops callbacks.
// A corrupt record remains visible with its checked prefix and per-record error;
// any other failure prevents publishing a deceptively complete inventory.
func loadHelmJournalHistory(ctx context.Context, path string) (entries []helmJournalEntry, err error) {
	if ctx == nil {
		return nil, helmjournal.ErrInvalid
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	journal, err := kube.OpenHelmUninstallJournal(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := journal.Close(); closeErr != nil {
			entries = nil
			err = errors.Join(err, closeErr)
		}
	}()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	ids, err := journal.IDs()
	if err != nil {
		return nil, err
	}
	archivedIDs, err := journal.ArchivedIDs()
	if err != nil {
		return nil, err
	}
	entries = make([]helmJournalEntry, 0, len(ids)+len(archivedIDs))
	for group, list := range [][]string{ids, archivedIDs} {
		for _, id := range list {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			var recovery *kube.HelmUninstallRecovery
			var readErr error
			if group == 0 {
				recovery, readErr = journal.Recover(id)
			} else {
				recovery, readErr = journal.RecoverArchived(id)
			}
			if readErr != nil && !errors.Is(readErr, helmjournal.ErrCorrupt) {
				return nil, readErr
			}
			if recovery == nil {
				return nil, helmjournal.ErrUnavailable
			}
			entries = append(entries, helmJournalEntry{id: id, recovery: recovery, err: readErr})
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// The UI calls this only after its independent final confirmation, capturing
// the exact path, backend, report, session, namespace and selected revision.
// Cancellation revokes the newly minted consent even if it races its creation.
// Every exit closes the private journal without relying on emit or widget life.
func executeHelmJournal(ctx context.Context, path string, backend *kube.Backend, dryRun *kube.HelmUninstallDryRun, session, ns string, row helmrelease.Revision) (report *kube.HelmUninstallExecution, err error) {
	if ctx == nil {
		return nil, helmjournal.ErrInvalid
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	journal, err := kube.OpenHelmUninstallJournal(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		// A Close error must not erase independently established server effects.
		err = errors.Join(err, journal.Close())
	}()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	consent, err := backend.AcknowledgeHelmUninstallExecution(dryRun, session, ns, row, true, journal)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, consent.Discard)
	defer func() {
		stop()
		consent.Discard()
	}()
	return backend.ExecuteHelmUninstall(ctx, consent, session, ns, row)
}
