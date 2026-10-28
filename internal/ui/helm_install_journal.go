package uiworkbench

import (
	"context"
	"errors"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
)

// The independently confirmed worker owns both the shared private journal and
// the single-use execution grant. Opening, admission and closing never depend
// on a UI callback. The existing install path rechecks cross-operation admission
// under this lock; an invented uninstall revision is neither needed nor valid.
func executeHelmInstallJournal(ctx context.Context, path string, backend *kube.Backend, dryRun *kube.HelmInstallDryRun, candidate *helmrelease.InstallCandidate, session string) (report *kube.HelmInstallExecution, err error) {
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
		// Failure to close or save cannot erase independently established effects.
		err = errors.Join(err, journal.Close())
	}()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	consent, err := backend.AcknowledgeHelmInstallExecution(ctx, dryRun, candidate, session, true, journal)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, consent.Discard)
	defer func() {
		stop()
		consent.Discard()
	}()
	return backend.ExecuteHelmInstall(ctx, consent, candidate, session)
}
