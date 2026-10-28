package kube

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
)

var (
	ErrHelmInstallExecutionBudget   = errors.New("The reviewed Helm install cannot complete within the existing execution budgets; no operation was started.")
	ErrHelmInstallExecutionConsent  = errors.New("Helm install requires fresh independent single-use write confirmation.")
	ErrHelmInstallExecutionResponse = errors.New("Helm install received an unsupported or inconsistent response.")
	ErrHelmInstallExecutionChanged  = errors.New("The reviewed Helm install namespace, release history or object changed.")
	ErrHelmInstallExecutionRejected = errors.New("The exact Helm install request was rejected; no retry is authorized.")
	ErrHelmInstallExecutionUnknown  = errors.New("The Helm install request may have committed; stop without replay or rollback.")
)

// This grants backend-only execution, not desktop activation. Copies share one
// consumption/revocation state. No grant is serialized into the journal.
type HelmInstallExecutionConsent struct{ state *installExecutionConsentState }
type installExecutionConsentState struct {
	origin   *installDryRunState
	finished time.Time
	store    *helmjournal.Store
	binding  string
	mu       sync.Mutex
	used     bool
	revoked  atomic.Bool
	cancel   context.CancelFunc
}

func (c HelmInstallExecutionConsent) Format(f fmt.State, _ rune) {
	_, _ = io.WriteString(f, "Single-use Helm install execution consent")
}
func (c HelmInstallExecutionConsent) MarshalJSON() ([]byte, error) {
	return []byte(`{"kind":"helm-install-execution-consent"}`), nil
}

// AcknowledgeHelmInstallExecution separately acknowledges persistent creation,
// sensitive exact reads, partial/Unknown effects and use of the existing private
// journal. The historically named journal handle is shared with uninstall: it
// must not open a second directory or bypass cross-operation admission.
// Minting neither performs I/O nor renews the original preflight deadline.
func (b *Backend) AcknowledgeHelmInstallExecution(ctx context.Context, report *HelmInstallDryRun, c *helmrelease.InstallCandidate, session string, acknowledged bool, journal *HelmUninstallJournal) (*HelmInstallExecutionConsent, error) {
	if !acknowledged || ctx == nil || report == nil || !report.complete || report.state == nil || report.finished.IsZero() || journal == nil || journal.store == nil {
		return nil, ErrHelmInstallExecutionConsent
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binding, err := b.helmUninstallJournalBinding()
	if err != nil {
		return nil, ErrHelmExecutionJournal
	}
	origin := report.state
	origin.mu.Lock()
	defer origin.mu.Unlock()
	if !origin.used || origin.executionMinted || origin.revoked.Load() {
		return nil, ErrHelmInstallExecutionConsent
	}
	if !report.Matches(b, c, session, time.Now()) || origin.plan == nil {
		return nil, helmrelease.ErrInstallStale
	}
	// Preserve the original store's shared lifetime even if the public wrapper is
	// later reassigned. Preparation/candidate/request bytes were frozen at preflight.
	store := *journal.store
	origin.executionMinted = true
	return &HelmInstallExecutionConsent{state: &installExecutionConsentState{origin: origin, finished: report.finished, store: &store, binding: binding}}, nil
}
func (c *HelmInstallExecutionConsent) Discard() {
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
func (s *installExecutionConsentState) matches(b *Backend, c *helmrelease.InstallCandidate, session string) bool {
	return s != nil && s.origin != nil && !s.revoked.Load() && !time.Now().Before(s.finished) && s.origin.matches(b, c, session, time.Now())
}

// Only immutable historical identities are returned; no client, grant or raw
// payload survives in this result. JournalComplete is separate from server facts.
type HelmInstallExecution struct {
	id, preparationID, outcome string
	effects                    []helmjournal.Effect
	started, finished, expires time.Time
	journalComplete            bool
}

func (r *HelmInstallExecution) JournalID() string {
	if r == nil {
		return ""
	}
	return r.id
}
func (r *HelmInstallExecution) PreparationID() string {
	if r == nil {
		return ""
	}
	return r.preparationID
}
func (r *HelmInstallExecution) Outcome() string {
	if r == nil || r.outcome == "" {
		return helmjournal.NotStarted
	}
	return r.outcome
}
func (r *HelmInstallExecution) Effects() []helmjournal.Effect {
	if r == nil {
		return nil
	}
	return append([]helmjournal.Effect(nil), r.effects...)
}
func (r *HelmInstallExecution) JournalComplete() bool { return r != nil && r.journalComplete }
func (r *HelmInstallExecution) ExpiresAt() time.Time {
	if r == nil {
		return time.Time{}
	}
	return r.expires
}
func (r *HelmInstallExecution) StartedAt() time.Time {
	if r == nil {
		return time.Time{}
	}
	return r.started
}
func (r *HelmInstallExecution) FinishedAt() time.Time {
	if r == nil {
		return time.Time{}
	}
	return r.finished
}

func installRequestDigest(body []byte) string {
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

// ExecuteHelmInstall consumes the independent grant even on canceled or invalid
// calls. Every possible write goes through a synced WAL intent. Dispatch has
// the original expiry AND the existing ten-second execution limit. Blocking OS
// sync may exceed that deadline; authority is checked again after sync returns.
func (b *Backend) ExecuteHelmInstall(parent context.Context, grant *HelmInstallExecutionConsent, c *helmrelease.InstallCandidate, session string) (out *HelmInstallExecution, err error) {
	if grant == nil || grant.state == nil {
		return nil, ErrHelmInstallExecutionConsent
	}
	s := grant.state
	s.mu.Lock()
	if s.used || s.revoked.Load() {
		s.mu.Unlock()
		return nil, ErrHelmInstallExecutionConsent
	}
	s.used = true
	if parent == nil || !s.matches(b, c, session) {
		s.mu.Unlock()
		return nil, helmrelease.ErrInstallStale
	}
	origin := s.origin
	ctx, timeoutCancel := context.WithTimeout(parent, helmrelease.ReadTimeout)
	ctx, expiryCancel := context.WithDeadline(ctx, origin.preparation.ExpiresAt())
	ctx, revokeCancel := context.WithCancel(ctx)
	s.cancel = revokeCancel
	origin.mu.Lock()
	origin.cancel = revokeCancel
	if origin.revoked.Load() {
		revokeCancel()
	}
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
		if !s.matches(b, c, session) {
			return helmrelease.ErrInstallStale
		}
		return nil
	}
	dispatch := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		origin.mu.Lock()
		defer origin.mu.Unlock()
		return active()
	}
	if err = active(); err != nil {
		return nil, err
	}
	now := time.Now()
	p := helmjournal.InstallPlan{Binding: s.binding, CandidateID: origin.candidate.ID(), PreparationID: origin.preparation.ID(), Namespace: origin.candidate.Namespace(), NamespaceUID: origin.preparation.namespaceUID, NamespaceResourceVersion: origin.preparation.namespaceRV, Release: origin.candidate.Release(), StorageName: origin.candidate.StorageName(), Started: now, Expires: origin.preparation.ExpiresAt()}
	out = &HelmInstallExecution{preparationID: p.PreparationID, started: now, expires: p.Expires, outcome: helmjournal.NotStarted}
	for i := 0; i < origin.plan.Len(); i++ {
		resource, name := origin.plan.Identity(i)
		body := origin.plan.SensitiveBody(i)
		digest := installRequestDigest(body)
		clear(body)
		kind := "create-configmap"
		if i == 0 {
			p.StorageDigest = digest
			kind = "create-secret"
		} else {
			p.Targets = append(p.Targets, helmjournal.InstallTarget{Name: name, Digest: digest})
		}
		if resource != "secrets" && resource != "configmaps" {
			return out, ErrHelmInstallExecutionResponse
		}
		out.effects = append(out.effects, helmjournal.Effect{Kind: kind, Namespace: p.Namespace, Name: name, ContentDigest: digest, Outcome: helmjournal.NotStarted})
	}
	out.effects = append(out.effects, helmjournal.Effect{Kind: "deploy", Namespace: p.Namespace, Name: p.StorageName, Outcome: helmjournal.NotStarted})
	defer func() { out.finished = time.Now(); out.outcome = installExecutionOutcome(out.effects) }()
	if err = b.installExecutionBudget(ctx, &origin.candidate, origin.plan); err != nil {
		return out, err
	}
	writer, e := s.store.BeginInstall(p)
	if e != nil {
		return out, errors.Join(ErrHelmExecutionJournal, e)
	}
	out.id = writer.ID()
	defer func() {
		if e := writer.Abort(); e != nil {
			out.journalComplete = false
			err = errors.Join(err, ErrHelmExecutionJournal, e)
		}
	}()
	runner := installExecutionRun{b: b, ctx: ctx, active: active, dispatch: dispatch, state: origin, session: session, journal: writer, out: out, remaining: installExecutionReadBytes}
	err = runner.run()
	if e = writer.Finish(time.Now()); e != nil {
		err = errors.Join(err, ErrHelmExecutionJournal, e)
	} else {
		out.journalComplete = true
	}
	return out, err
}

// Keep the existing shared response allowance unchanged. Admission only rejects
// provably impossible work; it neither reserves capacity nor extends authority.
const installExecutionReadBytes = 4*helmrelease.MaxInstallCreateBytes + maxLiveReadBytes

func (b *Backend) installExecutionBudget(ctx context.Context, c *helmrelease.InstallCandidate, plan *helmrelease.InstallCreatePlan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n := plan.Len() - 1
	if n < 1 || n > helmrelease.MaxPreviewObjects {
		return ErrHelmInstallExecutionResponse
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return ErrHelmInstallExecutionResponse
	}
	// Fresh preparation N+7, discovery 1, Secret stage 11, ConfigMap stages
	// 13*N and final transition 21+2*N. Pagination can only increase this.
	rateBudget := func() error {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return context.DeadlineExceeded
		}
		if installRateCapacityInsufficient(b.ownedRateLimit, 16*n+40, remaining) {
			return ErrHelmInstallExecutionBudget
		}
		return ctx.Err()
	}
	if err := rateBudget(); err != nil {
		return err
	}
	pending, patch, err := helmrelease.InstallExecutionSizeBounds(ctx, c, plan)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrHelmInstallExecutionResponse
	}
	// Pending Secret response + initial observation, two guards per ConfigMap
	// and two pre-patch guards each require the same encoded release value.
	// Count only mandatory data bytes, not request JSON/metadata, so server
	// serialization cannot make this response-size lower bound too large.
	if pending > installExecutionReadBytes/(2*n+4) || patch > helmrelease.MaxSecretBytes {
		return ErrHelmInstallExecutionBudget
	}
	// Local bounded encoding also consumes time. Recheck the actual remaining
	// deadline before opening the WAL, rather than reusing pre-encoding time.
	return rateBudget()
}

func installRateCapacityInsufficient(limit *backendRateLimit, requests int, remaining time.Duration) bool {
	if limit == nil || limit.burst <= 0 || limit.qps <= 0 || math.IsNaN(float64(limit.qps)) {
		return false
	}
	// A full initial bucket is the most optimistic possible starting point.
	// Round refill upward; no QPS() assumption is made about opaque limiters.
	capacity := float64(limit.burst) + math.Ceil(float64(limit.qps)*remaining.Seconds())
	return float64(requests) > capacity
}

func installExecutionOutcome(effects []helmjournal.Effect) string {
	all, completed, pending, rejected := len(effects) > 0, false, false, false
	for _, e := range effects {
		if e.Outcome == helmjournal.Unknown {
			return helmjournal.Unknown
		}
		all = all && e.Outcome == helmjournal.Completed
		completed = completed || e.Outcome == helmjournal.Completed
		pending = pending || e.Outcome == helmjournal.AcceptedPending
		rejected = rejected || e.Outcome == helmjournal.Rejected
	}
	if all {
		return helmjournal.Completed
	}
	if completed {
		return "Partial"
	}
	if pending {
		return helmjournal.AcceptedPending
	}
	if rejected {
		return helmjournal.Rejected
	}
	return helmjournal.NotStarted
}
