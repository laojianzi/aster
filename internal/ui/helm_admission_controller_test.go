package uiworkbench

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/kube"
)

func TestNativeHelmAdmissionLifecycle(t *testing.T) {
	for _, change := range []string{"namespace", "session", "selection", "backend", "journal-path", "credential-expiry", "preparation-expiry", "stop", "window-close", "pane-reopen", "application-cancel", "connection-cancel"} {
		t.Run(change, func(t *testing.T) {
			f := newNativeHelmUninstallFixture(t, 1)
			h := newHelmAdmissionController(t, f)
			w := h.w
			path := w.helmJournalPath
			helmAdmissionInitialize(t, path)
			connection, cancelConnection := context.WithCancel(w.ctx)
			defer cancelConnection()
			w.connectionCtx = connection
			if change == "preparation-expiry" {
				w.helmUninstall.prepareAck = true
				w.prepareHelmUninstall()
				h.finish()
				if w.helmUninstall.preparation == nil {
					t.Fatal("preparation fixture failed")
				}
			}
			w.reviewHelmAdmission()
			h.finish()
			if w.helmUninstall.admission == nil || w.helmUninstall.admission.Report().Reason != helmjournal.AdmissionNoBlocker {
				t.Fatal("initial local check did not finish", w.helmUninstall.status)
			}
			files := helmAdmissionFiles(t, path)
			_, _, _, _, requests := f.snapshot(t)
			w.reviewHelmAdmission()
			if w.helmUninstall.admission != nil || w.helmUninstall.busy != helmAdmissionBusy || w.pendingWrites != 0 {
				t.Fatal("new read retained stale admission or write accounting")
			}
			completion := h.next() // actual worker has closed; UI publication is delayed
			switch change {
			case "namespace":
				w.namespace = "different"
			case "session":
				w.sessionID = "different-session"
			case "selection":
				w.helmReleases.selected.ResourceVersion = "different-rv"
			case "backend":
				w.backend = f.backend(t)
			case "journal-path":
				w.helmJournalPath += "-different"
			case "credential-expiry":
				w.credentialExpiry = time.Now().Add(-time.Second)
			case "preparation-expiry":
				w.expireHelmUninstallFlow(w.helmUninstall.preparation.ExpiresAt())
			case "stop":
				w.stopHelmUninstall()
			case "window-close":
				w.Close()
			case "pane-reopen":
				w.clearHelmUninstall()
				w.openHelmUninstall()
			case "application-cancel":
				w.cancel()
			case "connection-cancel":
				cancelConnection()
			}
			// A discarded callback must not be necessary to release the reader.
			store, err := helmjournal.Open(path)
			nativeHelmJournalMust(t, err)
			nativeHelmJournalMust(t, store.Close())
			completion()
			if w.helmUninstall.admission != nil || w.pendingWrites != 0 {
				t.Fatal("old local result survived its scope or lifetime", change)
			}
			helmAdmissionUnchanged(t, f, path, files, len(requests))
		})
	}
}

func TestNativeHelmAdmissionNoExecutionAuthority(t *testing.T) {
	f := newNativeHelmUninstallFixture(t, 1)
	h := newHelmAdmissionController(t, f)
	w := h.w
	path := w.helmJournalPath
	helmAdmissionInitialize(t, path)
	w.helmUninstall.prepareAck = true
	w.prepareHelmUninstall()
	h.finish()
	w.helmUninstall.dryRunAck = true
	w.preflightHelmUninstall()
	h.finish()
	s := w.helmUninstall
	if s.preparation == nil || s.dryRun == nil {
		t.Fatal("independent preparation and dry-run fixture failed", s.status)
	}
	files := helmAdmissionFiles(t, path)
	_, _, _, _, requests := f.snapshot(t)
	w.reviewHelmAdmission()
	h.finish()
	if w.helmUninstall.admission == nil || w.helmUninstall.admission.Report().Reason != helmjournal.AdmissionNoBlocker ||
		w.helmUninstall.preparation != s.preparation || w.helmUninstall.dryRun != s.dryRun || w.helmUninstall.dryRunConsent != s.dryRunConsent ||
		!s.dryRun.Matches(s.backend, s.session, s.namespace, s.row, time.Now()) || w.helmUninstall.executeAck || w.helmUninstall.confirmation != "" {
		t.Fatal("local review changed a grant or supplied execution confirmation")
	}
	helmAdmissionUnchanged(t, f, path, files, len(requests))
	w.executeHelmUninstall()
	if w.helmUninstall.attempted || w.pendingWrites != 0 {
		t.Fatal("NoBlocker authorized execution")
	}
	// Another independently confirmed attempt can change the store after the
	// snapshot. The original UI must still reach Begin's actual locked guard.
	id := helmAdmissionUnknown(t, f, h.ctx, s.backend, path, s.session, s.namespace, s.row, s.preparation)
	files = helmAdmissionFiles(t, path)
	_, _, _, _, requests = f.snapshot(t)
	w.helmUninstall.executeAck, w.helmUninstall.confirmation = true, s.row.Release
	w.executeHelmUninstall()
	if w.helmUninstall.admission != nil {
		t.Fatal("execution retained an obsolete NoBlocker display")
	}
	h.finish()
	result := w.helmUninstall.execution
	if result == nil || result.Outcome() != kube.HelmExecutionNotStarted || result.JournalID() != "" ||
		!strings.Contains(w.helmUninstall.status, "Retained unresolved history blocks another execution") || w.pendingWrites != 0 {
		t.Fatal("actual Begin bypassed newer Unknown history", w.helmUninstall.status)
	}
	helmAdmissionUnchanged(t, f, path, files, len(requests))
	entries, err := loadHelmJournalHistory(h.ctx, path)
	nativeHelmJournalMust(t, err)
	if len(entries) != 1 || entries[0].id != id || entries[0].recovery.Outcome() != kube.HelmExecutionUnknown {
		t.Fatal("the local review or rejected execution rewrote uncertainty")
	}
}
