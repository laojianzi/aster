package kube

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	"k8s.io/apimachinery/pkg/util/validation"
)

type HelmInstallRecoveryTargetState string

const (
	HelmInstallRecoveryOriginal     HelmInstallRecoveryTargetState = "original-present"
	HelmInstallRecoveryModified     HelmInstallRecoveryTargetState = "original-modified"
	HelmInstallRecoveryReplacement  HelmInstallRecoveryTargetState = "replacement-present"
	HelmInstallRecoveryAbsent       HelmInstallRecoveryTargetState = "absent"
	HelmInstallRecoveryUnattributed HelmInstallRecoveryTargetState = "unattributed-present"
)

// These are sampled metadata facts, not proof of ownership, unchanged content,
// creation by this attempt, a successful deploy, readiness or write eligibility.
// An empty historical UID always leaves a present name unattributed.
type HelmInstallRecoveryItem struct {
	Kind, Namespace, Name                       string
	HistoricalUID, HistoricalResourceVersion    string
	State                                       HelmInstallRecoveryTargetState
	UID, ResourceVersion, Markers               string
	Terminating, Finalizers, Owners, Keep, Hook bool
	ObservedAt                                  time.Time
}

type HelmInstallRecoveryObservation struct {
	id, namespace, release, historyOutcome string
	namespaceUID, namespaceRV              string
	storage                                HelmInstallRecoveryItem
	items                                  []HelmInstallRecoveryItem
	revisions                              []helmrelease.Revision
	started, finished, expires             time.Time
}

func (o *HelmInstallRecoveryObservation) ID() string {
	if o == nil {
		return ""
	}
	return o.id
}
func (o *HelmInstallRecoveryObservation) Namespace() string {
	if o == nil {
		return ""
	}
	return o.namespace
}
func (o *HelmInstallRecoveryObservation) Release() string {
	if o == nil {
		return ""
	}
	return o.release
}
func (o *HelmInstallRecoveryObservation) NamespaceIdentity() (string, string) {
	if o == nil {
		return "", ""
	}
	return o.namespaceUID, o.namespaceRV
}
func (o *HelmInstallRecoveryObservation) HistoryOutcome() string {
	if o == nil {
		return helmjournal.NotStarted
	}
	return o.historyOutcome
}
func (o *HelmInstallRecoveryObservation) Storage() HelmInstallRecoveryItem {
	if o == nil {
		return HelmInstallRecoveryItem{}
	}
	return o.storage
}
func (o *HelmInstallRecoveryObservation) Items() []HelmInstallRecoveryItem {
	if o == nil {
		return nil
	}
	return append([]HelmInstallRecoveryItem(nil), o.items...)
}
func (o *HelmInstallRecoveryObservation) Revisions() []helmrelease.Revision {
	if o == nil {
		return nil
	}
	return append([]helmrelease.Revision(nil), o.revisions...)
}
func (o *HelmInstallRecoveryObservation) StartedAt() time.Time {
	if o == nil {
		return time.Time{}
	}
	return o.started
}
func (o *HelmInstallRecoveryObservation) FinishedAt() time.Time {
	if o == nil {
		return time.Time{}
	}
	return o.finished
}
func (o *HelmInstallRecoveryObservation) ExpiresAt() time.Time {
	if o == nil {
		return time.Time{}
	}
	return o.expires
}

type HelmInstallReconciliationConsent struct {
	state *helmInstallReconciliationConsentState
}

type helmInstallReconciliationConsentState struct {
	owner              *backendIdentity
	recovery           *helmInstallRecoveryState
	history            helmInstallReconciliationHistory
	session, namespace string
	created, expires   time.Time
	mu                 sync.Mutex
	used               bool
	cancel             context.CancelFunc
	revoked            atomic.Bool
}

type helmInstallReconciliationHistory struct {
	snapshot helmjournal.Snapshot
	outcome  string
}

func (c HelmInstallReconciliationConsent) Format(f fmt.State, _ rune) {
	_, _ = io.WriteString(f, "Single-use Helm install recovery metadata confirmation")
}
func (c HelmInstallReconciliationConsent) MarshalJSON() ([]byte, error) {
	return []byte(`{"kind":"helm-install-recovery-read-confirmation"}`), nil
}

func validHelmRecoveryHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func freezeHelmInstallReconciliation(r *HelmInstallRecovery) (helmInstallReconciliationHistory, bool) {
	empty := helmInstallReconciliationHistory{}
	if r == nil || r.state == nil || r.Corrupt() {
		return empty, false
	}
	s := cloneHelmInstallSnapshot(r.state.snapshot)
	p := s.Install
	if s.Version != 2 || s.Action != "install" || p == nil || !validHelmRecoveryHex(s.ID, 32) || !helmReconciliationDigest(s.Digest) ||
		!helmReconciliationDigest(p.Binding) || !validHelmRecoveryHex(p.CandidateID, 32) || !validHelmRecoveryHex(p.PreparationID, 32) ||
		!helmrelease.ValidNamespace(p.Namespace) || !safeHelmExecutionID(p.NamespaceUID, 128) || !safeHelmExecutionID(p.NamespaceResourceVersion, 256) ||
		p.Release == "" || len(p.Release) > 53 || len(validation.IsDNS1123Label(p.Release)) != 0 ||
		p.StorageName != "sh.helm.release.v1."+p.Release+".v1" || !helmReconciliationDigest(p.StorageDigest) ||
		len(p.Targets) < 1 || len(p.Targets) > helmrelease.MaxPreviewObjects || len(s.Effects) != len(p.Targets)+2 {
		return empty, false
	}
	seen := make(map[string]bool, len(p.Targets))
	for _, t := range p.Targets {
		if t.Name == "" || len(t.Name) > 253 || len(validation.IsDNS1123Subdomain(t.Name)) != 0 || seen[t.Name] || !helmReconciliationDigest(t.Digest) {
			return empty, false
		}
		seen[t.Name] = true
	}
	for i, e := range s.Effects {
		kind, name := "create-configmap", ""
		switch i {
		case 0:
			kind, name = "create-secret", p.StorageName
		case len(s.Effects) - 1:
			kind, name = "deploy", p.StorageName
		default:
			name = p.Targets[i-1].Name
		}
		if e.Kind != kind || e.Namespace != p.Namespace || e.Name != name ||
			e.UID != "" && !safeHelmExecutionID(e.UID, 128) || e.ResourceVersion != "" && !safeHelmExecutionID(e.ResourceVersion, 256) ||
			e.ObservedResourceVersion != "" && !safeHelmExecutionID(e.ObservedResourceVersion, 256) {
			return empty, false
		}
		switch e.Outcome {
		case helmjournal.NotStarted, helmjournal.Rejected, helmjournal.Unknown:
		case helmjournal.AcceptedPending, helmjournal.Completed:
			if e.UID == "" || e.ObservedResourceVersion == "" {
				return empty, false
			}
		default:
			return empty, false
		}
	}
	return helmInstallReconciliationHistory{snapshot: s, outcome: r.Outcome()}, true
}

// This independent acknowledgement grants bounded metadata GETs for exactly
// this loaded history and the current Backend/session. Historical execution
// expiry is irrelevant to the new read grant, and no old write grant is revived.
func (b *Backend) AcknowledgeHelmInstallReconciliation(r *HelmInstallRecovery, session, ns string, acknowledged bool) (*HelmInstallReconciliationConsent, error) {
	if !acknowledged || b == nil || b.identity == nil || !helmrelease.ValidPreviewSession(session) || !helmrelease.ValidNamespace(ns) {
		return nil, ErrHelmReconciliationConsent
	}
	h, ok := freezeHelmInstallReconciliation(r)
	if !ok || h.snapshot.Install.Namespace != ns {
		return nil, ErrHelmReconciliationConsent
	}
	binding, err := b.helmUninstallJournalBinding()
	if err != nil || binding != h.snapshot.Install.Binding {
		return nil, ErrHelmReconciliationConsent
	}
	now := time.Now()
	return &HelmInstallReconciliationConsent{state: &helmInstallReconciliationConsentState{owner: b.identity,
		recovery: r.state, history: h, session: session, namespace: ns, created: now, expires: now.Add(helmReconciliationLifetime)}}, nil
}

func (s *helmInstallReconciliationConsentState) matches(b *Backend, recovery *helmInstallRecoveryState, session, ns string, now time.Time) bool {
	return s != nil && b != nil && b.identity != nil && s.owner == b.identity && recovery != nil && s.recovery == recovery &&
		s.session == session && s.namespace == ns && !now.Before(s.created) && now.Before(s.expires)
}

func (c *HelmInstallReconciliationConsent) Discard() {
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

// ObserveHelmInstallRecovery consumes the fresh read grant even for invalid or
// canceled attempts. Namespace and complete release-history reads bracket the
// exact reserved Secret and target metadata reads. No WAL writer, execution,
// replay, adoption, uncertainty clearing or full-Secret fallback is reachable.
func (b *Backend) ObserveHelmInstallRecovery(parent context.Context, c *HelmInstallReconciliationConsent, recovery *HelmInstallRecovery, session, ns string) (*HelmInstallRecoveryObservation, error) {
	if c == nil || c.state == nil {
		return nil, ErrHelmReconciliationConsent
	}
	s := c.state
	var origin *helmInstallRecoveryState
	if recovery != nil {
		origin = recovery.state
	}
	s.mu.Lock()
	if s.used || s.revoked.Load() {
		s.mu.Unlock()
		return nil, ErrHelmReconciliationConsent
	}
	s.used = true
	if parent == nil || !s.matches(b, origin, session, ns, time.Now()) {
		s.mu.Unlock()
		return nil, ErrHelmReconciliationConsent
	}
	// Keep the current concrete transport stable if the caller later overwrites
	// its exported wrapper. Discard remains the explicit in-flight revocation.
	ownedBackend := *b
	b = &ownedBackend
	ctx, timeoutCancel := context.WithTimeout(parent, helmrelease.ReadTimeout)
	ctx, expiryCancel := context.WithDeadline(ctx, s.expires)
	ctx, revokeCancel := context.WithCancel(ctx)
	s.cancel = revokeCancel
	s.mu.Unlock()
	defer func() {
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
		if s.revoked.Load() || !s.matches(b, origin, session, ns, time.Now()) {
			return ErrHelmReconciliationConsent
		}
		return nil
	}
	if err := active(); err != nil {
		return nil, err
	}
	binding, err := b.helmUninstallJournalBinding()
	if err != nil || binding != s.history.snapshot.Install.Binding {
		return nil, ErrHelmReconciliationChanged
	}
	dispatch := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		return active()
	}
	p := s.history.snapshot.Install
	run := helmInstallReconciliationRead{read: helmReconciliationRead{b: b, ctx: ctx, active: active, dispatch: dispatch,
		namespace: ns, row: helmrelease.Revision{Release: p.Release}, remaining: 2*helmrelease.MaxTotalBytes + maxLiveReadBytes}, history: s.history}
	out, err := run.observe(s.expires)
	if err != nil {
		return nil, err
	}
	// Completion and Discard share the same order as dispatch. A canceled,
	// revoked or expired result cannot be published as a complete observation.
	s.mu.Lock()
	finished := time.Now()
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if s.revoked.Load() || !s.matches(b, origin, session, ns, finished) {
		s.mu.Unlock()
		return nil, ErrHelmReconciliationConsent
	}
	out.finished = finished
	s.mu.Unlock()
	return out, nil
}
