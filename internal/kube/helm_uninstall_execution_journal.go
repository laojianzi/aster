package kube

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	"k8s.io/client-go/rest"
)

// Storage errors never include local paths, request bodies or transport errors.
var ErrHelmExecutionJournal = errors.New("The private Helm recovery journal could not be committed; execution stopped.")

// A journal owns a private, explicitly selected directory. Copies share the
// concrete store's lock and lifetime. Neither this handle nor its files confer
// Kubernetes authority. There is deliberately no resume, replay or repair API.
type HelmUninstallJournal struct{ store *helmjournal.Store }

func OpenHelmUninstallJournal(dir string) (*HelmUninstallJournal, error) {
	s, err := helmjournal.Open(dir)
	if err != nil {
		return nil, err
	}
	return &HelmUninstallJournal{store: s}, nil
}

func (j *HelmUninstallJournal) IDs() ([]string, error) {
	if j == nil || j.store == nil {
		return nil, helmjournal.ErrClosed
	}
	return j.store.List()
}

func (j *HelmUninstallJournal) Close() error {
	if j == nil || j.store == nil {
		return nil
	}
	return j.store.Close()
}

// Recovery is an inert, immutable projection of bounded, untrusted historical
// bytes. It contains no original session, consent, client, credential or payload.
// Complete means a checked terminal frame, not a successful server outcome.
type HelmUninstallRecovery struct {
	id, preparationID, namespace, binding, digest string
	revision                                      helmrelease.Revision
	targets                                       []helmrelease.UninstallTarget
	effects                                       []HelmUninstallExecutionEffect
	outcome                                       HelmUninstallExecutionOutcome
	started, finished, expires                    time.Time
	complete, corrupt                             bool
	identity                                      *helmjournal.HistoryIdentity
	archived                                      bool
}

func (j *HelmUninstallJournal) Recover(id string) (*HelmUninstallRecovery, error) {
	return j.recoverIdentified(id, false)
}

func (j *HelmUninstallJournal) recoverIdentified(id string, archived bool) (*HelmUninstallRecovery, error) {
	if j == nil || j.store == nil {
		return nil, helmjournal.ErrClosed
	}
	s, identity, err := j.store.LoadIdentified(id, archived)
	if err != nil && !errors.Is(err, helmjournal.ErrCorrupt) {
		return nil, err
	}
	// Installation history has no legacy uninstall identity or reconciliation authority.
	if s.Version == 2 || s.Install != nil {
		return nil, helmjournal.ErrInvalid
	}
	return helmUninstallRecoveryFromSnapshot(s, identity, archived), err
}

// The mixed-version inventory dispatches one checked load. Keep the original
// v1 projection shared with its existing version-specific accessors.
func helmUninstallRecoveryFromSnapshot(s helmjournal.Snapshot, identity *helmjournal.HistoryIdentity, archived bool) *HelmUninstallRecovery {
	r := &HelmUninstallRecovery{id: s.ID, preparationID: s.Plan.PreparationID,
		namespace: s.Plan.Namespace, binding: s.Plan.Binding, digest: s.Digest,
		revision: s.Plan.Revision, targets: append([]helmrelease.UninstallTarget(nil), s.Plan.Targets...),
		started: s.Plan.Started, finished: s.Finished, expires: s.Plan.Expires,
		complete: s.Closed && !s.Corrupt, corrupt: s.Corrupt, identity: identity, archived: archived}
	for _, e := range s.Effects {
		r.effects = append(r.effects, HelmUninstallExecutionEffect{Kind: e.Kind, Namespace: e.Namespace,
			Name: e.Name, UID: e.UID, ResourceVersion: e.ResourceVersion,
			ObservedResourceVersion: e.ObservedResourceVersion, Outcome: HelmUninstallExecutionOutcome(e.Outcome)})
	}
	r.outcome = helmExecutionOutcome(r.effects, nil)
	if !r.complete {
		// A missing terminal or damaged tail can hide later intent. Preserve the
		// checked per-effect prefix without inventing a clean final outcome.
		r.outcome = HelmExecutionUnknown
	}
	return r
}

func (r *HelmUninstallRecovery) ID() string {
	if r == nil {
		return ""
	}
	return r.id
}
func (r *HelmUninstallRecovery) PreparationID() string {
	if r == nil {
		return ""
	}
	return r.preparationID
}
func (r *HelmUninstallRecovery) Namespace() string {
	if r == nil {
		return ""
	}
	return r.namespace
}
func (r *HelmUninstallRecovery) Revision() helmrelease.Revision {
	if r == nil {
		return helmrelease.Revision{}
	}
	return r.revision
}
func (r *HelmUninstallRecovery) Targets() []helmrelease.UninstallTarget {
	if r == nil {
		return nil
	}
	return append([]helmrelease.UninstallTarget(nil), r.targets...)
}
func (r *HelmUninstallRecovery) Effects() []HelmUninstallExecutionEffect {
	if r == nil {
		return nil
	}
	return append([]HelmUninstallExecutionEffect(nil), r.effects...)
}
func (r *HelmUninstallRecovery) Outcome() HelmUninstallExecutionOutcome {
	if r == nil || r.outcome == "" {
		return HelmExecutionNotStarted
	}
	return r.outcome
}
func (r *HelmUninstallRecovery) StartedAt() time.Time {
	if r == nil {
		return time.Time{}
	}
	return r.started
}
func (r *HelmUninstallRecovery) FinishedAt() time.Time {
	if r == nil {
		return time.Time{}
	}
	return r.finished
}
func (r *HelmUninstallRecovery) ExpiresAt() time.Time {
	if r == nil {
		return time.Time{}
	}
	return r.expires
}
func (r *HelmUninstallRecovery) Complete() bool { return r != nil && r.complete }
func (r *HelmUninstallRecovery) Corrupt() bool  { return r == nil || r.corrupt }

// This digest only rejects different connection configurations. It does not
// authenticate a journal, identify a physical cluster, identify a credential,
// or authorize any request. The current Backend and explicit new confirmation
// are still required. Capture it before the internal redirect wrapper is added.
func helmUninstallConnectionBinding(c *rest.Config) string {
	if c == nil || c.Transport != nil || c.Dial != nil || c.Proxy != nil ||
		c.CAFile != "" && len(c.CAData) == 0 || len(c.CAData) > 1<<20 {
		return ""
	}
	u, err := url.Parse(c.Host)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" {
		return ""
	}
	u.Scheme, u.Host = strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	ca := sha256.Sum256(c.CAData)
	// Authentication material, files, exec arguments and raw server errors must
	// never enter this projection. JSON fixes field boundaries and map order.
	data, err := json.Marshal(struct {
		Purpose, Endpoint, ServerName, CA string
		Insecure                          bool
		NextProtos                        []string
		Impersonate                       rest.ImpersonationConfig
	}{"aster/helm-uninstall/connection/v1", u.String(), c.ServerName, hex.EncodeToString(ca[:]),
		c.Insecure, c.NextProtos, c.Impersonate})
	if err != nil {
		return ""
	}
	defer clear(data)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (b *Backend) helmUninstallJournalBinding() (string, error) {
	if b == nil || b.identity == nil || b.helmJournalBinding == "" {
		return "", ErrHelmExecutionJournal
	}
	return b.helmJournalBinding, nil
}
