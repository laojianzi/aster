package kube

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
)

// This API deliberately has no desktop call site. A future UI must separately
// qualify confirmation/lifecycle handling and private crash recovery before it
// exposes persistent uninstall. No operation or consent is restored on restart.
var (
	ErrHelmExecutionConsent  = errors.New("Helm uninstall requires fresh, independent, single-use execution confirmation.")
	ErrHelmExecutionRejected = errors.New("The exact Helm uninstall effect was rejected by the server.")
	ErrHelmExecutionResponse = errors.New("Helm uninstall received an unsupported or inconsistent response.")
	ErrHelmExecutionUnknown  = errors.New("The Helm uninstall effect may have committed; stop and explicitly reconcile before any further action.")
	ErrHelmExecutionPending  = errors.New("The accepted Helm uninstall effect has not been verified complete within the observation budget.")
	ErrHelmExecutionChanged  = errors.New("The reviewed Helm source, revision history or object identity changed; execution stopped.")
)

type HelmUninstallExecutionOutcome string

const (
	HelmExecutionNotStarted      HelmUninstallExecutionOutcome = "NotStarted"
	HelmExecutionRejected        HelmUninstallExecutionOutcome = "Rejected"
	HelmExecutionAcceptedPending HelmUninstallExecutionOutcome = "AcceptedPending"
	HelmExecutionPartial         HelmUninstallExecutionOutcome = "Partial"
	HelmExecutionUnknown         HelmUninstallExecutionOutcome = "Unknown"
	HelmExecutionCompleted       HelmUninstallExecutionOutcome = "Completed"
)

// Each entry describes one ordered persistent request, never a dry-run. A
// DELETE entry completes only on a subsequent exact absence observation. The
// two storage entries complete only after their precise response and a fresh
// retained-storage/history observation agree. ResourceVersion is the opaque
// request precondition; ObservedResourceVersion is a validated response value.
// Unknown retains all earlier effects and must never be interpreted as no-op.
type HelmUninstallExecutionEffect struct {
	Kind                    string // begin, delete, finish
	Namespace, Name         string
	UID, ResourceVersion    string
	ObservedResourceVersion string
	Outcome                 HelmUninstallExecutionOutcome
}

// The finished report is immutable historical information, even when expired
// or when its consent is discarded. It holds no client, credential, raw storage,
// patch, arbitrary metadata or renewable authority. Accessors return copies.
type HelmUninstallExecution struct {
	preparationID              string
	revision, storage          helmrelease.Revision
	targets                    []helmrelease.UninstallTarget
	effects                    []HelmUninstallExecutionEffect
	outcome                    HelmUninstallExecutionOutcome
	started, finished, expires time.Time
}

func (r *HelmUninstallExecution) PreparationID() string {
	if r == nil {
		return ""
	}
	return r.preparationID
}
func (r *HelmUninstallExecution) Revision() helmrelease.Revision {
	if r == nil {
		return helmrelease.Revision{}
	}
	return r.revision
}
func (r *HelmUninstallExecution) Storage() helmrelease.Revision {
	if r == nil {
		return helmrelease.Revision{}
	}
	return r.storage
}
func (r *HelmUninstallExecution) Targets() []helmrelease.UninstallTarget {
	if r == nil {
		return nil
	}
	return append([]helmrelease.UninstallTarget(nil), r.targets...)
}
func (r *HelmUninstallExecution) Effects() []HelmUninstallExecutionEffect {
	if r == nil {
		return nil
	}
	return append([]HelmUninstallExecutionEffect(nil), r.effects...)
}
func (r *HelmUninstallExecution) Outcome() HelmUninstallExecutionOutcome {
	if r == nil || r.outcome == "" {
		return HelmExecutionNotStarted
	}
	return r.outcome
}
func (r *HelmUninstallExecution) ExpiresAt() time.Time {
	if r == nil {
		return time.Time{}
	}
	return r.expires
}
func (r *HelmUninstallExecution) StartedAt() time.Time {
	if r == nil {
		return time.Time{}
	}
	return r.started
}
func (r *HelmUninstallExecution) FinishedAt() time.Time {
	if r == nil {
		return time.Time{}
	}
	return r.finished
}

type HelmUninstallExecutionConsent struct{ state *helmExecutionConsentState }
type helmExecutionConsentState struct {
	// Public wrapper structs can be reassigned by their owners. Retain a private
	// preparation-wrapper copy and the completed report's timestamp; never read
	// either caller-owned wrapper after this grant has been minted.
	preparation *HelmUninstallPreparation
	finished    time.Time
	origin      *helmDryRunConsentState
	mu          sync.Mutex
	used        bool
	cancel      context.CancelFunc
	revoked     atomic.Bool
}

// AcknowledgeHelmUninstallExecution is called only after independently confirming
// the exact current cluster/namespace/release and target identities, retained
// history, orphan consequences and ordered partial/Unknown outcomes. It also
// authorizes the fresh sensitive reads and server dry-runs required immediately
// before these conditional effects. GET/dry-run agreement alone cannot mint it.
// It never extends the original preparation's thirty-second lifetime.
func (b *Backend) AcknowledgeHelmUninstallExecution(r *HelmUninstallDryRun, session, ns string, row helmrelease.Revision, acknowledged bool) (*HelmUninstallExecutionConsent, error) {
	if !acknowledged || r == nil || r.state == nil || r.finished.IsZero() {
		return nil, ErrHelmExecutionConsent
	}
	s, finished := r.state, r.finished
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.executionMinted || !s.used || s.revoked.Load() {
		return nil, ErrHelmExecutionConsent
	}
	if s.preparation == nil {
		return nil, helmrelease.ErrPreparationStale
	}
	p := *s.preparation
	if now := time.Now(); now.Before(finished) || !p.Matches(b, session, ns, row, now) {
		return nil, helmrelease.ErrPreparationStale
	}
	s.executionMinted = true
	return &HelmUninstallExecutionConsent{state: &helmExecutionConsentState{preparation: &p, finished: finished, origin: s}}, nil
}

func (s *helmExecutionConsentState) matches(b *Backend, session, ns string, row helmrelease.Revision, now time.Time) bool {
	return s != nil && s.origin != nil && !s.origin.revoked.Load() && !now.Before(s.finished) &&
		s.preparation.Matches(b, session, ns, row, now)
}

// Discard is idempotent and cancels work already in flight. It cannot retract a
// dispatched request, erase its outcome or renew the original report's grant.
// A copied handle shares precisely this consumption and cancellation state.
func (c *HelmUninstallExecutionConsent) Discard() {
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

// ExecuteHelmUninstall consumes independent execution consent before any I/O,
// including failed/canceled attempts. Its total real deadline is ten seconds or
// the ORIGINAL preparation expiry, whichever arrives first. No retry, parallel
// write, force, history purge, CLI, automatic reconciliation or rollback exists.
func (b *Backend) ExecuteHelmUninstall(ctx context.Context, c *HelmUninstallExecutionConsent, session, ns string, row helmrelease.Revision) (*HelmUninstallExecution, error) {
	if c == nil || c.state == nil || c.state.preparation == nil || c.state.origin == nil {
		return nil, ErrHelmExecutionConsent
	}
	s := c.state
	s.mu.Lock()
	if s.used || s.revoked.Load() {
		s.mu.Unlock()
		return nil, ErrHelmExecutionConsent
	}
	s.used = true
	origin := s.origin
	p := s.preparation
	out := newHelmExecutionReport(p)
	if !s.matches(b, session, ns, row, time.Now()) {
		s.mu.Unlock()
		out.finished = time.Now()
		return out, helmrelease.ErrPreparationStale
	}
	ctx, timeoutCancel := context.WithTimeout(ctx, helmrelease.ReadTimeout)
	ctx, expiryCancel := context.WithDeadline(ctx, p.ExpiresAt())
	ctx, revokeCancel := context.WithCancel(ctx)
	s.cancel = revokeCancel
	// Original dry-run Discard also revokes execution. The report's single mint
	// means no competing execution can own this cancellation slot.
	origin.mu.Lock()
	if origin.revoked.Load() {
		revokeCancel()
	}
	origin.cancel = revokeCancel
	origin.mu.Unlock()
	s.mu.Unlock()
	defer func() {
		revokeCancel()
		expiryCancel()
		timeoutCancel()
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
		origin.mu.Lock()
		origin.cancel = nil
		origin.mu.Unlock()
	}()
	active := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.revoked.Load() || !s.matches(b, session, ns, row, time.Now()) {
			return helmrelease.ErrPreparationStale
		}
		return nil
	}
	// This is the dispatch linearization point. Discard before it prevents the
	// call; Discard after it cancels a potentially in-flight effect, hence Unknown
	// if no authoritative acknowledgement can be verified. Locks never span I/O.
	dispatch := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		origin.mu.Lock()
		defer origin.mu.Unlock()
		return active()
	}
	runner := helmExecutionRun{b: b, ctx: ctx, active: active, dispatch: dispatch,
		out: out, session: session, ns: ns, row: row,
		remaining: helmrelease.MaxSecretBytes + maxLiveReadBytes}
	err := runner.run(p)
	out.finished = time.Now()
	out.outcome = helmExecutionOutcome(out.effects, err)
	return out, err
}

func newHelmExecutionReport(p *HelmUninstallPreparation) *HelmUninstallExecution {
	r := &HelmUninstallExecution{preparationID: p.ID(), revision: p.Revision(), storage: p.Revision(),
		targets: p.Targets(), expires: p.ExpiresAt(), started: time.Now(), outcome: HelmExecutionNotStarted}
	storage := HelmUninstallExecutionEffect{Kind: "begin", Namespace: p.Namespace(), Name: r.revision.StorageName,
		UID: r.revision.UID, ResourceVersion: r.revision.ResourceVersion, Outcome: HelmExecutionNotStarted}
	r.effects = append(r.effects, storage)
	for _, target := range r.targets {
		r.effects = append(r.effects, HelmUninstallExecutionEffect{Kind: "delete", Namespace: target.Namespace,
			Name: target.Name, UID: target.UID, ResourceVersion: target.ResourceVersion, Outcome: HelmExecutionNotStarted})
	}
	storage.Kind, storage.ResourceVersion = "finish", ""
	r.effects = append(r.effects, storage)
	return r
}

func helmExecutionOutcome(effects []HelmUninstallExecutionEffect, err error) HelmUninstallExecutionOutcome {
	completed, pending, rejected := false, false, false
	all := len(effects) > 0
	for _, e := range effects {
		if e.Outcome == HelmExecutionUnknown {
			return HelmExecutionUnknown
		}
		completed = completed || e.Outcome == HelmExecutionCompleted
		pending = pending || e.Outcome == HelmExecutionAcceptedPending
		rejected = rejected || e.Outcome == HelmExecutionRejected
		all = all && e.Outcome == HelmExecutionCompleted
	}
	if err == nil && all {
		return HelmExecutionCompleted
	}
	if completed {
		return HelmExecutionPartial
	}
	if pending {
		return HelmExecutionAcceptedPending
	}
	if rejected {
		return HelmExecutionRejected
	}
	return HelmExecutionNotStarted
}
