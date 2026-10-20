package kube

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
	"k8s.io/apimachinery/pkg/util/validation"
)

const helmReconciliationLifetime = 30 * time.Second

var (
	ErrHelmReconciliationConsent  = errors.New("Helm recovery observations require fresh, independent, single-use read confirmation.")
	ErrHelmReconciliationChanged  = errors.New("The selected Helm recovery connection, release or revision history changed; observation stopped.")
	ErrHelmReconciliationResponse = errors.New("Helm recovery metadata is unavailable, unsupported or inconsistent; no complete observation was produced.")
	ErrHelmReconciliationBudget   = errors.New("Helm recovery metadata exceeded the bounded observation budget.")
)

type HelmUninstallRecoveryTargetState string

const (
	HelmRecoveryTargetOriginal    HelmUninstallRecoveryTargetState = "original-present"
	HelmRecoveryTargetModified    HelmUninstallRecoveryTargetState = "original-modified"
	HelmRecoveryTargetReplacement HelmUninstallRecoveryTargetState = "replacement-present"
	HelmRecoveryTargetAbsent      HelmUninstallRecoveryTargetState = "absent"
)

// A recovered target is an independently sampled current fact beside an inert
// historical identity. No causal attribution, authentic Helm ownership, full
// manifest agreement or write eligibility is inferred from metadata agreement.
type HelmUninstallRecoveryItem struct {
	Target                                      helmrelease.UninstallTarget
	State                                       HelmUninstallRecoveryTargetState
	UID, ResourceVersion, Markers               string
	Terminating, Finalizers, Owners, Keep, Hook bool
	ObservedAt                                  time.Time
}

// A completed GET-only observation remains historical information after expiry
// or Discard. It contains no Backend, credential, raw metadata or execution
// capability. In particular HistoryOutcome is never changed by current reads.
type HelmUninstallRecoveryObservation struct {
	id, namespace              string
	revision                   helmrelease.Revision
	historyOutcome             HelmUninstallExecutionOutcome
	items                      []HelmUninstallRecoveryItem
	started, finished, expires time.Time
}

func (o *HelmUninstallRecoveryObservation) ID() string {
	if o == nil {
		return ""
	}
	return o.id
}
func (o *HelmUninstallRecoveryObservation) Namespace() string {
	if o == nil {
		return ""
	}
	return o.namespace
}
func (o *HelmUninstallRecoveryObservation) Revision() helmrelease.Revision {
	if o == nil {
		return helmrelease.Revision{}
	}
	return o.revision
}
func (o *HelmUninstallRecoveryObservation) HistoryOutcome() HelmUninstallExecutionOutcome {
	if o == nil {
		return HelmExecutionNotStarted
	}
	return o.historyOutcome
}
func (o *HelmUninstallRecoveryObservation) Items() []HelmUninstallRecoveryItem {
	if o == nil {
		return nil
	}
	return append([]HelmUninstallRecoveryItem(nil), o.items...)
}
func (o *HelmUninstallRecoveryObservation) StartedAt() time.Time {
	if o == nil {
		return time.Time{}
	}
	return o.started
}
func (o *HelmUninstallRecoveryObservation) FinishedAt() time.Time {
	if o == nil {
		return time.Time{}
	}
	return o.finished
}
func (o *HelmUninstallRecoveryObservation) ExpiresAt() time.Time {
	if o == nil {
		return time.Time{}
	}
	return o.expires
}

type HelmUninstallReconciliationConsent struct {
	state *helmReconciliationConsentState
}

type helmReconciliationConsentState struct {
	owner              *backendIdentity
	history            helmReconciliationHistory
	session, namespace string
	row                helmrelease.Revision
	created, expires   time.Time
	mu                 sync.Mutex
	used               bool
	cancel             context.CancelFunc
	revoked            atomic.Bool
}

// Freeze value projections, never the caller-owned recovery wrapper. A parsed
// recovery is not an execution preparation and no old consent is reconstructed.
type helmReconciliationHistory struct {
	id, preparationID, binding, digest, namespace string
	revision                                      helmrelease.Revision
	targets                                       []helmrelease.UninstallTarget
	outcome                                       HelmUninstallExecutionOutcome
}

func freezeHelmReconciliationHistory(r *HelmUninstallRecovery) (helmReconciliationHistory, bool) {
	if r == nil || r.Corrupt() {
		return helmReconciliationHistory{}, false
	}
	h := helmReconciliationHistory{id: r.ID(), preparationID: r.PreparationID(), binding: r.binding,
		digest: r.digest, namespace: r.Namespace(), revision: r.Revision(), targets: r.Targets(), outcome: r.Outcome()}
	if !safeHelmExecutionID(h.id, 128) || !safeHelmExecutionID(h.preparationID, 128) ||
		!helmReconciliationDigest(h.binding) || !helmReconciliationDigest(h.digest) ||
		!validHelmReconciliationRow(h.namespace, h.revision) || h.revision.Status != "deployed" ||
		len(h.targets) == 0 || len(h.targets) > helmrelease.MaxPreviewObjects {
		return helmReconciliationHistory{}, false
	}
	switch h.outcome {
	case HelmExecutionNotStarted, HelmExecutionRejected, HelmExecutionAcceptedPending,
		HelmExecutionPartial, HelmExecutionUnknown, HelmExecutionCompleted:
	default:
		return helmReconciliationHistory{}, false
	}
	seenNames, seenUIDs := map[string]bool{}, map[string]bool{}
	for _, t := range h.targets {
		if !validHelmReconciliationTarget(h.namespace, t) || seenNames[t.Name] || seenUIDs[t.UID] {
			return helmReconciliationHistory{}, false
		}
		seenNames[t.Name], seenUIDs[t.UID] = true, true
	}
	return h, true
}

func validHelmReconciliationTarget(ns string, t helmrelease.UninstallTarget) bool {
	return t.APIVersion == "v1" && t.Kind == "ConfigMap" && t.Resource == "configmaps" && t.Namespace == ns &&
		t.Name != "" && len(t.Name) <= 253 && len(validation.IsDNS1123Subdomain(t.Name)) == 0 &&
		safeHelmExecutionID(t.UID, 128) && safeHelmExecutionID(t.ResourceVersion, 256)
}

func helmReconciliationDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, ch := range s {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

func validHelmReconciliationRow(ns string, row helmrelease.Revision) bool {
	if !helmrelease.ValidSelection(ns, row) || !safeHelmExecutionID(row.UID, 128) || !safeHelmExecutionID(row.ResourceVersion, 256) {
		return false
	}
	return row.Status == "deployed" || row.Status == "uninstalling" || row.Status == "uninstalled"
}

func sameHelmReconciliationLineage(a, b helmrelease.Revision) bool {
	return a.Release == b.Release && a.Number == b.Number && a.StorageName == b.StorageName && a.UID == b.UID
}

// This NEW acknowledgement covers only the exact current identity's metadata
// GETs for the reviewed recovery record. Historical expiry is intentionally not
// consulted: it cannot extend an old grant, and the new grant permits no write.
// The native workbench requires this independent acknowledgement after selecting
// a current metadata revision. Offline history and CLI paths confer no grant.
func (b *Backend) AcknowledgeHelmUninstallReconciliation(r *HelmUninstallRecovery, session, ns string, row helmrelease.Revision, acknowledged bool) (*HelmUninstallReconciliationConsent, error) {
	if !acknowledged || b == nil || b.identity == nil || !helmrelease.ValidPreviewSession(session) || !validHelmReconciliationRow(ns, row) {
		return nil, ErrHelmReconciliationConsent
	}
	h, ok := freezeHelmReconciliationHistory(r)
	if !ok || ns != h.namespace || !sameHelmReconciliationLineage(h.revision, row) {
		return nil, ErrHelmReconciliationConsent
	}
	binding, err := b.helmUninstallJournalBinding()
	if err != nil || binding != h.binding {
		return nil, ErrHelmReconciliationConsent
	}
	now := time.Now()
	return &HelmUninstallReconciliationConsent{state: &helmReconciliationConsentState{owner: b.identity,
		history: h, session: session, namespace: ns, row: row, created: now, expires: now.Add(helmReconciliationLifetime)}}, nil
}

func (s *helmReconciliationConsentState) matches(b *Backend, session, ns string, row helmrelease.Revision, now time.Time) bool {
	return s != nil && b != nil && b.identity != nil && s.owner == b.identity && s.session == session && s.namespace == ns &&
		s.row == row && !now.Before(s.created) && now.Before(s.expires)
}

func (c *HelmUninstallReconciliationConsent) Discard() {
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

// ObserveHelmUninstallRecovery consumes the new grant before any I/O, including
// canceled or stale attempts. Two complete namespace snapshots bracket the whole
// target GET set under one ten-second deadline. The runner has no mutation method, never
// retries, and neither changes the historical report nor renews execution.
func (b *Backend) ObserveHelmUninstallRecovery(ctx context.Context, c *HelmUninstallReconciliationConsent, session, ns string, row helmrelease.Revision) (*HelmUninstallRecoveryObservation, error) {
	if c == nil || c.state == nil {
		return nil, ErrHelmReconciliationConsent
	}
	s := c.state
	s.mu.Lock()
	if s.used || s.revoked.Load() {
		s.mu.Unlock()
		return nil, ErrHelmReconciliationConsent
	}
	s.used = true
	if !s.matches(b, session, ns, row, time.Now()) {
		s.mu.Unlock()
		return nil, ErrHelmReconciliationConsent
	}
	ctx, timeoutCancel := context.WithTimeout(ctx, helmrelease.ReadTimeout)
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
		if s.revoked.Load() || !s.matches(b, session, ns, row, time.Now()) {
			return ErrHelmReconciliationConsent
		}
		return nil
	}
	if err := active(); err != nil {
		return nil, err
	}
	binding, err := b.helmUninstallJournalBinding()
	if err != nil || binding != s.history.binding {
		return nil, ErrHelmReconciliationChanged
	}
	// The pre-Do check is the read dispatch boundary. Discard after this check
	// cancels the in-flight GET; its response cannot become a completed report.
	dispatch := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		return active()
	}
	run := helmReconciliationRead{b: b, ctx: ctx, active: active, dispatch: dispatch,
		namespace: ns, row: row, remaining: 2*helmrelease.MaxTotalBytes + maxLiveReadBytes}
	out := &HelmUninstallRecoveryObservation{id: s.history.id, namespace: ns, revision: row,
		historyOutcome: s.history.outcome, started: time.Now(), expires: s.expires}
	if err := run.soleRevision(); err != nil {
		return nil, err
	}
	for _, target := range s.history.targets {
		item, err := run.target(target)
		if err != nil {
			return nil, err
		}
		out.items = append(out.items, item)
	}
	if err := run.soleRevision(); err != nil {
		return nil, err
	}
	// This critical section orders completion against Discard. Capture one time
	// for both lifetime validation and the immutable completion stamp, so a read
	// finishing at expiry cannot publish an already-expired completed report.
	s.mu.Lock()
	finished := time.Now()
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if s.revoked.Load() || !s.matches(b, session, ns, row, finished) {
		s.mu.Unlock()
		return nil, ErrHelmReconciliationConsent
	}
	out.finished = finished
	s.mu.Unlock()
	return out, nil
}
