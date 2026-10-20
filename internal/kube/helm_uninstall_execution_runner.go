package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	helmExecutionAbsenceTimeout = 3 * time.Second
	helmExecutionPollInterval   = 50 * time.Millisecond
)

// Only this synchronous invocation owns sensitive bytes and the Backend. None
// of them is retained by the public result or an execution consent. Existing
// per-read budgets remain, and all additional storage/live/write responses share
// four MiB. Repeated complete metadata snapshots retain their own fixed page,
// count and byte bounds, under the same outer ten-second deadline.
type helmExecutionRun struct {
	b                *Backend
	ctx              context.Context
	active, dispatch func() error
	out              *HelmUninstallExecution
	journal          *helmjournal.Writer
	session, ns      string
	row              helmrelease.Revision
	source           []byte
	remaining        int
}

func (r *helmExecutionRun) run(p *HelmUninstallPreparation) error {
	defer func() { clear(r.source); r.source = nil }()
	if err := r.active(); err != nil {
		return err
	}
	// Pin a complete source representation before current admission, so an
	// inconsistent same-RV source response during later reads also fails closed.
	data, _, err := r.read(r.ctx, "secrets", r.row.StorageName, helmrelease.MaxSecretBytes, false)
	if err != nil {
		return err
	}
	r.source = data
	profile, err := helmrelease.InspectUninstallProfile(r.ctx, data, r.ns, r.row, r.session, r.out.started)
	if err != nil {
		return helmExecutionReadError(r.ctx, err)
	}
	items := profile.Preview().Items()
	if len(items) != len(r.out.targets) {
		return ErrHelmExecutionChanged
	}
	for i, item := range items {
		t := r.out.targets[i]
		if item.APIVersion != t.APIVersion || item.Kind != t.Kind || item.Name != t.Name ||
			(item.Namespace != "" && item.Namespace != t.Namespace) || t.Namespace != r.ns || t.Resource != "configmaps" {
			return ErrHelmExecutionChanged
		}
	}
	// The independent execution confirmation explicitly covers these new reads
	// and dry-runs. The old successful report never becomes a mutation request,
	// and this private preflight cannot renew the original preparation expiry.
	dryConsent, err := r.b.AcknowledgeHelmUninstallDryRun(p, r.session, r.ns, r.row, true)
	if err != nil {
		return err
	}
	defer dryConsent.Discard()
	fresh, err := r.b.PreflightHelmUninstall(r.ctx, dryConsent, r.session, r.ns, r.row)
	if err != nil {
		return helmExecutionReadError(r.ctx, err)
	}
	if !slices.Equal(fresh.Targets(), r.out.targets) {
		return ErrHelmExecutionChanged
	}
	if err := r.guardSource(); err != nil {
		return err
	}
	if err := r.active(); err != nil {
		return err
	}
	patch, err := helmrelease.BuildUninstallStatusPatch(r.ctx, r.source, r.ns, r.row, helmrelease.BeginUninstallStatus, time.Now())
	if err != nil {
		return helmExecutionReadError(r.ctx, err)
	}
	err = r.storageEffect(0, patch, "uninstalling")
	clear(patch)
	if err != nil {
		return err
	}
	if err := r.guardSource(); err != nil {
		return err
	}
	r.out.effects[0].Outcome = HelmExecutionCompleted
	if err := r.recordEffect(0); err != nil {
		return err
	}

	for i, target := range r.out.targets {
		// Complete source history and exact stored bytes are checked around the
		// immediate target read. Prior observations never substitute for current
		// UID/RV preconditions in the individual DELETE itself.
		if err := r.guardSource(); err != nil {
			return err
		}
		if err := r.checkTarget(target); err != nil {
			return err
		}
		if err := r.guardSource(); err != nil {
			return err
		}
		uid, rv := types.UID(target.UID), target.ResourceVersion
		policy := metav1.DeletePropagationOrphan
		body, err := json.Marshal(metav1.DeleteOptions{
			TypeMeta:          metav1.TypeMeta{APIVersion: "v1", Kind: "DeleteOptions"},
			Preconditions:     &metav1.Preconditions{UID: &uid, ResourceVersion: &rv},
			PropagationPolicy: &policy,
		})
		if err != nil {
			return ErrHelmExecutionResponse
		}
		effect := &r.out.effects[i+1]
		response, dispatched, rejected, err := r.mutate(i+1, http.MethodDelete, "configmaps", target.Name, body, maxLiveObjectBytes)
		clear(body)
		if err != nil {
			return r.mutationError(i+1, dispatched, rejected, err)
		}
		if err := validateHelmExecutionDelete(response, target, r.row.Release); err != nil {
			clear(response)
			return r.mutationError(i+1, true, false, err)
		}
		var envelope struct {
			Metadata struct {
				ResourceVersion string `json:"resourceVersion"`
			} `json:"metadata"`
		}
		_ = json.Unmarshal(response, &envelope)
		clear(response)
		effect.ObservedResourceVersion = envelope.Metadata.ResourceVersion
		effect.Outcome = HelmExecutionAcceptedPending
		if err := r.recordEffect(i + 1); err != nil {
			return err
		}
		if err := r.active(); err != nil {
			return err
		}
		if err := r.awaitAbsence(target); err != nil {
			return err
		}
		effect.Outcome = HelmExecutionCompleted
		if err := r.recordEffect(i + 1); err != nil {
			return err
		}
		if err := r.guardSource(); err != nil {
			return err
		}
	}

	// Final admission uses the ACTUAL acknowledged uninstalling RV and original
	// deletion timestamp, never the hypothetical RV produced by the first dry-run.
	if err := r.guardSource(); err != nil {
		return err
	}
	if err := r.allAbsent(); err != nil {
		return err
	}
	patch, err = helmrelease.BuildUninstallStatusPatch(r.ctx, r.source, r.ns, r.row, helmrelease.FinishUninstallStatus, time.Now())
	if err != nil {
		return helmExecutionReadError(r.ctx, err)
	}
	defer clear(patch)
	response, err := r.b.helmDryRunRequest(r.ctx, http.MethodPatch, r.ns, "secrets", r.row.StorageName, patch, helmrelease.MaxSecretBytes, &r.remaining)
	if err != nil {
		return helmExecutionReadError(r.ctx, err)
	}
	err = validateHelmDryRunStorage(r.ctx, r.source, patch, response)
	clear(response)
	if err != nil {
		return helmExecutionReadError(r.ctx, err)
	}
	if err := r.guardSource(); err != nil {
		return err
	}
	if err := r.allAbsent(); err != nil {
		return err
	}
	if err := r.guardSource(); err != nil {
		return err
	}
	last := len(r.out.effects) - 1
	r.out.effects[last].ResourceVersion = r.row.ResourceVersion
	if err := r.storageEffect(last, patch, "uninstalled"); err != nil {
		return err
	}
	if err := r.guardSource(); err != nil {
		return err
	}
	if err := r.allAbsent(); err != nil {
		return err
	}
	if err := r.guardSource(); err != nil {
		return err
	}
	r.out.effects[last].Outcome = HelmExecutionCompleted
	if err := r.recordEffect(last); err != nil {
		return err
	}
	// All required final observations have already qualified inside the deadline.
	// A subsequent discard cannot erase these completed historical facts.
	return nil
}

func (r *helmExecutionRun) storageEffect(index int, patch []byte, nextStatus string) error {
	effect := &r.out.effects[index]
	response, dispatched, rejected, err := r.mutate(index, http.MethodPatch, "secrets", r.row.StorageName, patch, helmrelease.MaxSecretBytes)
	if err != nil {
		return r.mutationError(index, dispatched, rejected, err)
	}
	defer clear(response)
	next, err := validateHelmExecutionStorage(r.ctx, r.source, patch, response, r.ns, r.row, nextStatus)
	if err != nil {
		return r.mutationError(index, true, false, err)
	}
	effect.Outcome = HelmExecutionAcceptedPending
	effect.ObservedResourceVersion = next.ResourceVersion
	r.row, r.out.storage = next, next
	if err := r.recordEffect(index); err != nil {
		return err
	}
	clear(r.source)
	r.source = bytes.Clone(response)
	return r.active()
}

func (r *helmExecutionRun) recordEffect(index int) error {
	if r.journal == nil || r.out == nil || index < 0 || index >= len(r.out.effects) {
		return ErrHelmExecutionJournal
	}
	e := r.out.effects[index]
	if err := r.journal.Record(index, string(e.Outcome), e.ObservedResourceVersion); err != nil {
		return errors.Join(ErrHelmExecutionJournal, err)
	}
	return nil
}

func (r *helmExecutionRun) mutationError(index int, dispatched, rejected bool, err error) error {
	err = helmExecutionMutationError(&r.out.effects[index], dispatched, rejected, err)
	if dispatched {
		if journalErr := r.recordEffect(index); journalErr != nil {
			return errors.Join(err, journalErr)
		}
	}
	return err
}

// Read failure after a known acknowledgement is not an unknown write. The
// acknowledgement remains AcceptedPending and earlier completed effects remain.
func helmExecutionMutationError(effect *HelmUninstallExecutionEffect, dispatched, rejected bool, err error) error {
	if !dispatched {
		return err
	}
	if rejected {
		effect.Outcome = HelmExecutionRejected
		return ErrHelmExecutionRejected
	}
	effect.Outcome = HelmExecutionUnknown
	// Retain only fixed private errors/context values, never a transport URL,
	// server message, untrusted response body, label or release payload.
	if errors.Is(err, context.Canceled) {
		return errors.Join(ErrHelmExecutionUnknown, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(ErrHelmExecutionUnknown, context.DeadlineExceeded)
	}
	return ErrHelmExecutionUnknown
}

func helmExecutionReadError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, helmrelease.ErrPreparationStale) || errors.Is(err, helmrelease.ErrLiveStale) ||
		errors.Is(err, helmrelease.ErrInspectionStale) || errors.Is(err, helmrelease.ErrPreparationProfile) {
		return ErrHelmExecutionChanged
	}
	return ErrHelmExecutionResponse
}

// A complete namespace snapshot must contain exactly this selected release
// revision, including its latest known status/RV. Other releases may coexist.
func (r *helmExecutionRun) soleRevision() error {
	if err := r.active(); err != nil {
		return err
	}
	s, err := r.b.HelmReleaseMetadata(r.ctx, r.ns)
	if err != nil {
		return helmExecutionReadError(r.ctx, err)
	}
	rows, err := helmrelease.LatestRevisionSet(s, r.ns, r.row)
	if err != nil || len(rows) != 1 || rows[0] != r.row {
		return ErrHelmExecutionChanged
	}
	return r.active()
}

func (r *helmExecutionRun) guardSource() error {
	if err := r.soleRevision(); err != nil {
		return err
	}
	data, _, err := r.read(r.ctx, "secrets", r.row.StorageName, helmrelease.MaxSecretBytes, false)
	if err != nil {
		return err
	}
	defer clear(data)
	if _, err := helmrelease.InspectSecret(r.ctx, data, r.ns, r.row); err != nil {
		return helmExecutionReadError(r.ctx, err)
	}
	if !sameHelmExecutionStorage(data, r.source) {
		return ErrHelmExecutionChanged
	}
	return r.soleRevision()
}

func (r *helmExecutionRun) checkTarget(target helmrelease.UninstallTarget) error {
	data, status, err := r.read(r.ctx, "configmaps", target.Name, maxLiveObjectBytes, true)
	if err != nil {
		return err
	}
	defer clear(data)
	if status != http.StatusOK {
		return ErrHelmExecutionChanged
	}
	item := helmrelease.UninstallItem{APIVersion: "v1", Kind: "ConfigMap", Name: target.Name, Namespace: r.ns}
	projected, err := projectLiveMetadata(data, status, item, "configmaps", r.ns, r.row.Release, r.ns)
	if err != nil {
		return ErrHelmExecutionResponse
	}
	if projected.UID != target.UID || projected.ResourceVersion != target.ResourceVersion || projected.Markers != "match" ||
		projected.Terminating || projected.Finalizers || projected.Owners || projected.Hook || projected.Keep {
		return ErrHelmExecutionChanged
	}
	return r.active()
}

// Absence polling is GET-only and separately bounded to three seconds. It never
// resends DELETE or upgrades admission/permission failure to absence. The whole
// executor still has the same ten-second/original-expiry deadline.
func (r *helmExecutionRun) awaitAbsence(target helmrelease.UninstallTarget) error {
	ctx, cancel := context.WithTimeout(r.ctx, helmExecutionAbsenceTimeout)
	defer cancel()
	for {
		if err := r.active(); err != nil {
			return err
		}
		data, status, err := r.read(ctx, "configmaps", target.Name, maxLiveObjectBytes, true)
		if err != nil {
			if r.ctx.Err() == nil && ctx.Err() != nil {
				return ErrHelmExecutionPending
			}
			return err
		}
		absent, err := inspectHelmExecutionAbsence(data, status, target, r.row.Release)
		clear(data)
		if err != nil {
			return err
		}
		if err := r.active(); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ErrHelmExecutionPending
		}
		if absent {
			return nil
		}
		timer := time.NewTimer(helmExecutionPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if err := r.active(); err != nil {
				return err
			}
			return ErrHelmExecutionPending
		case <-timer.C:
		}
	}
}

func (r *helmExecutionRun) allAbsent() error {
	for _, target := range r.out.targets {
		data, status, err := r.read(r.ctx, "configmaps", target.Name, maxLiveObjectBytes, true)
		if err != nil {
			return err
		}
		absent, err := inspectHelmExecutionAbsence(data, status, target, r.row.Release)
		clear(data)
		if err != nil {
			return err
		}
		if !absent {
			return ErrHelmExecutionChanged
		}
		if err := r.active(); err != nil {
			return err
		}
	}
	return r.active()
}
