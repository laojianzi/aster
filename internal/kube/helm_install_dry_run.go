package kube

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
)

// Copies share one consumption and revocation state. This grants only GETs and
// bounded server dry-run POSTs, never persistent installation or final PATCH.
type HelmInstallDryRunConsent struct{ state *installDryRunState }
type installDryRunState struct {
	preparation HelmInstallPreparation
	candidate   helmrelease.InstallCandidate
	original    *helmrelease.InstallCandidate
	plan        *helmrelease.InstallCreatePlan
	mu          sync.Mutex
	used        bool
	revoked     atomic.Bool
	cancel      context.CancelFunc
}

func (c HelmInstallDryRunConsent) Format(f fmt.State, _ rune) {
	_, _ = io.WriteString(f, "Single-use Helm install dry-run consent")
}
func (c HelmInstallDryRunConsent) MarshalJSON() ([]byte, error) {
	return []byte(`{"kind":"helm-install-dry-run-consent"}`), nil
}
func (s *installDryRunState) matches(b *Backend, c *helmrelease.InstallCandidate, session string, now time.Time) bool {
	return s != nil && !s.revoked.Load() && c == s.original && c != nil && c.ID() == s.candidate.ID() &&
		s.preparation.Matches(b, &s.candidate, session, now)
}

// AcknowledgeHelmInstallDryRun freezes the exact request bodies and original
// wrappers. Subsequent replacement of a caller's wrappers cannot renew expiry
// or redirect this consent to another captured release.
func (b *Backend) AcknowledgeHelmInstallDryRun(ctx context.Context, p *HelmInstallPreparation, c *helmrelease.InstallCandidate, session string, acknowledged bool) (*HelmInstallDryRunConsent, error) {
	if !acknowledged {
		return nil, ErrHelmDryRunConsent
	}
	if ctx == nil || p == nil || !p.Matches(b, c, session, time.Now()) {
		return nil, helmrelease.ErrInstallStale
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := &installDryRunState{preparation: *p, candidate: *c, original: c}
	s.preparation.candidate = &s.candidate
	plan, err := helmrelease.BuildInstallCreatePlan(ctx, &s.candidate, time.Now())
	if err != nil {
		return nil, err
	}
	s.plan = plan
	if !s.matches(b, c, session, time.Now()) {
		return nil, helmrelease.ErrInstallStale
	}
	return &HelmInstallDryRunConsent{state: s}, nil
}
func (c *HelmInstallDryRunConsent) Discard() {
	if c == nil || c.state == nil {
		return
	}
	s := c.state
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked.Store(true)
	s.used = true
	if s.cancel != nil {
		s.cancel()
	}
}

type HelmInstallDryRunStep struct {
	Resource, Name string
	Verified       bool
}

// HelmInstallDryRun is an immutable sample. Even a complete report authorizes no
// future create, replay, rollback or pending->deployed transition.
type HelmInstallDryRun struct {
	state    *installDryRunState
	steps    []HelmInstallDryRunStep
	finished time.Time
	complete bool
}

func (r HelmInstallDryRun) Format(f fmt.State, _ rune) {
	_, _ = io.WriteString(f, "Inert Helm install server preflight (no execution authority)")
}
func (r HelmInstallDryRun) MarshalJSON() ([]byte, error) {
	return []byte(`{"kind":"inert-helm-install-server-preflight"}`), nil
}
func (r *HelmInstallDryRun) Steps() []HelmInstallDryRunStep {
	if r == nil {
		return nil
	}
	return append([]HelmInstallDryRunStep(nil), r.steps...)
}
func (r *HelmInstallDryRun) Complete() bool { return r != nil && r.complete }
func (r *HelmInstallDryRun) PreparationID() string {
	if r == nil || r.state == nil {
		return ""
	}
	return r.state.preparation.ID()
}
func (r *HelmInstallDryRun) ExpiresAt() time.Time {
	if r == nil || r.state == nil {
		return time.Time{}
	}
	return r.state.preparation.ExpiresAt()
}
func (r *HelmInstallDryRun) Matches(b *Backend, c *helmrelease.InstallCandidate, session string, now time.Time) bool {
	return r != nil && r.complete && !now.Before(r.finished) && r.state.matches(b, c, session, now)
}

// PreflightHelmInstall consumes consent before any network work, including a
// failed binding or already cancelled call. Reads before and after must qualify
// the original namespace and exact absent targets. There is no transparent retry.
func (b *Backend) PreflightHelmInstall(parent context.Context, grant *HelmInstallDryRunConsent, c *helmrelease.InstallCandidate, session string) (report *HelmInstallDryRun, err error) {
	if grant == nil || grant.state == nil {
		return nil, ErrHelmDryRunConsent
	}
	s := grant.state
	s.mu.Lock()
	if s.used || s.revoked.Load() {
		s.mu.Unlock()
		return nil, ErrHelmDryRunConsent
	}
	s.used = true
	if parent == nil || !s.matches(b, c, session, time.Now()) {
		s.mu.Unlock()
		return nil, helmrelease.ErrInstallStale
	}
	ctx, timeoutCancel := context.WithTimeout(parent, helmrelease.ReadTimeout)
	ctx, expiryCancel := context.WithDeadline(ctx, s.preparation.ExpiresAt())
	ctx, revokeCancel := context.WithCancel(ctx)
	s.cancel = revokeCancel
	s.mu.Unlock()
	report = &HelmInstallDryRun{state: s}
	defer func() {
		report.finished = time.Now()
		revokeCancel()
		expiryCancel()
		timeoutCancel()
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
	}()
	active := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !s.matches(b, c, session, time.Now()) {
			return helmrelease.ErrInstallStale
		}
		return nil
	}
	revalidate := func() error {
		if err := active(); err != nil {
			return err
		}
		fresh, err := b.PrepareHelmInstall(ctx, &s.candidate, session, true)
		if err != nil {
			return err
		}
		if fresh.namespaceUID != s.preparation.namespaceUID || fresh.namespaceRV != s.preparation.namespaceRV {
			return helmrelease.ErrInstallStale
		}
		return active()
	}
	if err = active(); err != nil {
		return report, err
	}
	if err = revalidate(); err != nil {
		return report, err
	}
	// Complete preparation establishes get/list and unambiguous routes. This
	// independent fresh discovery additionally requires create; it is not RBAC.
	remaining := maxLiveReadBytes
	discoveryBytes, _, e := b.helmLiveGET(ctx, []string{"/api", "v1"}, "application/json", maxLiveDiscoveryBytes, &remaining, false)
	if e != nil {
		return report, e
	}
	discovery, e := decodeLiveDiscovery(discoveryBytes, "v1")
	clear(discoveryBytes)
	if e != nil {
		return report, e
	}
	for kind, resource := range map[string]string{"Secret": "secrets", "ConfigMap": "configmaps"} {
		route, e := resolveLiveRoute(discovery, kind)
		if e != nil || route.resource != resource || !route.namespaced {
			return report, helmrelease.ErrLiveRouting
		}
		matches := 0
		for _, r := range discovery.APIResources {
			if r.Name == resource {
				matches++
				if r.Kind != kind || !hasVerb(r.Verbs, "create") {
					return report, helmrelease.ErrLiveRouting
				}
			}
		}
		if matches != 1 {
			return report, helmrelease.ErrLiveRouting
		}
	}
	responseBudget := helmrelease.MaxInstallCreateBytes
	for i := 0; i < s.plan.Len(); i++ {
		if err = active(); err != nil {
			return report, err
		}
		resource, name := s.plan.Identity(i)
		body := s.plan.SensitiveBody(i)
		response, attempted, requestErr := b.helmInstallDryRunPOST(ctx, s.candidate.Namespace(), resource, body, &responseBudget)
		clear(body)
		if attempted {
			report.steps = append(report.steps, HelmInstallDryRunStep{Resource: resource, Name: name})
		}
		if requestErr != nil {
			clear(response)
			return report, requestErr
		}
		validationErr := helmrelease.ValidateInstallCreateResponse(ctx, s.plan, i, response)
		clear(response)
		if validationErr != nil {
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
			return report, ErrHelmDryRunResponse
		}
		report.steps[len(report.steps)-1].Verified = true
	}
	if err = revalidate(); err != nil {
		return report, err
	}
	if err = active(); err != nil {
		return report, err
	}
	report.complete = true
	return report, nil
}
