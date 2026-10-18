package kube

import (
	"context"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
)

// HelmUninstallPreparation is an inert identity projection bound to the exact
// Backend instance. It is NOT an operation.Prepared or reusable write consent.
// Raw Secret content, patches, arbitrary metadata and credentials are not held.
type HelmUninstallPreparation struct {
	owner  *backendIdentity
	result *helmrelease.UninstallPreparation
}

func (p *HelmUninstallPreparation) ID() string {
	if p == nil {
		return ""
	}
	return p.result.ID()
}
func (p *HelmUninstallPreparation) Namespace() string {
	if p == nil {
		return ""
	}
	return p.result.Namespace()
}
func (p *HelmUninstallPreparation) Revision() helmrelease.Revision {
	if p == nil {
		return helmrelease.Revision{}
	}
	return p.result.Revision()
}
func (p *HelmUninstallPreparation) Targets() []helmrelease.UninstallTarget {
	if p == nil {
		return nil
	}
	return p.result.Targets()
}
func (p *HelmUninstallPreparation) Summary() helmrelease.Inspection {
	if p == nil {
		return helmrelease.Inspection{}
	}
	return p.result.Summary()
}
func (p *HelmUninstallPreparation) ExpiresAt() time.Time {
	if p == nil {
		return time.Time{}
	}
	return p.result.ExpiresAt()
}
func (p *HelmUninstallPreparation) Matches(b *Backend, session, ns string, row helmrelease.Revision, now time.Time) bool {
	return p != nil && b != nil && b.identity != nil && p.owner == b.identity && p.result.Matches(session, ns, row, now)
}

// PrepareHelmUninstall uses a NEW full-content acknowledgement, not a preview or
// metadata-read consent. It brackets the full Secret read and live GETs with two
// complete metadata snapshots. Only GET is used, through the existing identity,
// shared rate limiter, redirects/retry protections and bounded transports.
//
// A successful result establishes sampled restrictions only. No server dry-run,
// independent execution confirmation, conditional write or UI activation is
// implemented. A future caller must revoke stale results on model changes and
// separately satisfy the execution/crash-recovery contract before ANY mutation.
func (b *Backend) PrepareHelmUninstall(ctx context.Context, ns string, row helmrelease.Revision, session string, acknowledged bool) (*HelmUninstallPreparation, error) {
	if !acknowledged {
		return nil, helmrelease.ErrPreparationConsent
	}
	if b == nil || b.identity == nil || !helmrelease.ValidPreviewSession(session) {
		return nil, helmrelease.ErrPreviewSession
	}
	if !helmrelease.ValidSelection(ns, row) || row.Status != "deployed" {
		return nil, helmrelease.ErrPreparationProfile
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, helmrelease.ReadTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := b.HelmReleaseMetadata(ctx, ns)
	if err != nil {
		return nil, err
	}
	if err := helmrelease.SoleDeployedRevision(before, ns, row); err != nil {
		return nil, err
	}
	var profile *helmrelease.UninstallProfile
	err = b.readHelmRevision(ctx, ns, row, acknowledged, func(ctx context.Context, data []byte) error {
		var err error
		profile, err = helmrelease.InspectUninstallProfile(ctx, data, ns, row, session, started)
		return err
	})
	if err != nil {
		return nil, err
	}
	items, err := b.observeHelmItems(ctx, profile.Preview(), true)
	if err != nil {
		return nil, err
	}
	after, err := b.HelmReleaseMetadata(ctx, ns)
	if err != nil {
		return nil, err
	}
	result, err := helmrelease.FinishUninstallPreparation(ctx, profile, session, before, after, items, time.Now())
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &HelmUninstallPreparation{owner: b.identity, result: result}, nil
}
