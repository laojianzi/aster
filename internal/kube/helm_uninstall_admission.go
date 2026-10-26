package kube

import (
	"context"
	"errors"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
)

var ErrHelmJournalAdmission = errors.New("The local Helm journal check requires the current connection and a valid selected metadata identity.")

// HelmJournalAdmissionReview is an inert snapshot of local journal policy. It
// does not validate an uninstall profile, authenticate a cluster or carry any
// preparation, consent, client or execution authority. Even NoBlocker must be
// checked again by Begin while holding the execution journal lock.
type HelmJournalAdmissionReview struct {
	owner                             *backendIdentity
	binding, path, session, namespace string
	row                               helmrelease.Revision
	report                            helmjournal.Admission
	checked                           time.Time
}

func (r *HelmJournalAdmissionReview) Report() helmjournal.Admission {
	if r == nil {
		return helmjournal.Admission{Reason: helmjournal.AdmissionInvalid}
	}
	return r.report
}

func (r *HelmJournalAdmissionReview) CheckedAt() time.Time {
	if r == nil {
		return time.Time{}
	}
	return r.checked
}

// Matches binds presentation to the exact current selection, including its
// observed status and resourceVersion. The journal's underlying historical
// v1 guard retains binding/namespace/storage UID. Pending installs additionally
// block the same binding/namespace/release before any storage UID exists.
func (r *HelmJournalAdmissionReview) Matches(b *Backend, dir, session, ns string, row helmrelease.Revision) bool {
	return r != nil && b != nil && b.identity != nil && r.owner == b.identity &&
		r.binding != "" && r.binding == b.helmJournalBinding && r.path == dir &&
		r.session == session && r.namespace == ns && r.row == row
}

// ReviewHelmUninstallJournalAdmission reads only an already initialized local
// store. It uses the connection binding frozen when this Backend was created;
// it never reloads configuration, fetches metadata or release payloads, or
// obtains or consumes any consent. An uninstalling/otherwise valid metadata
// row can explain retained uncertainty without first preparing another write.
// A fixed blocker error may accompany a report suitable for display. Invalid
// scope or cancellation returns no snapshot that could look current.
func (b *Backend) ReviewHelmUninstallJournalAdmission(ctx context.Context, dir, session, ns string, row helmrelease.Revision) (*HelmJournalAdmissionReview, error) {
	if ctx == nil {
		return nil, ErrHelmJournalAdmission
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil || b.identity == nil || !helmrelease.ValidPreviewSession(session) || dir == "" ||
		!helmrelease.ValidSelection(ns, row) || !safeHelmExecutionID(row.UID, 128) ||
		!safeHelmExecutionID(row.ResourceVersion, 256) {
		return nil, ErrHelmJournalAdmission
	}
	binding, err := b.helmUninstallJournalBinding()
	if err != nil {
		return nil, ErrHelmJournalAdmission
	}
	report, err := helmjournal.ReviewAdmission(ctx, dir, helmjournal.AdmissionScope{
		Binding: binding, Namespace: ns, StorageUID: row.UID, Release: row.Release,
	})
	if stopped := ctx.Err(); stopped != nil {
		return nil, stopped
	}
	if report.Reason == helmjournal.AdmissionInvalid {
		return nil, ErrHelmJournalAdmission
	}
	if report.Reason == helmjournal.AdmissionCancelled {
		return nil, err
	}
	return &HelmJournalAdmissionReview{owner: b.identity, binding: binding, path: dir,
		session: session, namespace: ns, row: row, report: report, checked: time.Now()}, err
}
