package kube

import (
	"errors"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
)

// HelmJournalRecovery is a typed, inert inventory entry from a single checked
// local load. Version zero preserves the legacy corrupt-prefix presentation
// when no first frame could be established. It never enables an observation.
type HelmJournalRecovery struct {
	version   int
	uninstall *HelmUninstallRecovery
	install   *HelmInstallRecovery
}

func (r *HelmJournalRecovery) Version() int {
	if r == nil {
		return 0
	}
	return r.version
}
func (r *HelmJournalRecovery) Uninstall() *HelmUninstallRecovery {
	if r == nil {
		return nil
	}
	return r.uninstall
}
func (r *HelmJournalRecovery) Install() *HelmInstallRecovery {
	if r == nil {
		return nil
	}
	return r.install
}

// RecoverHistory uses the journal's existing lock and a single LoadIdentified
// call. No failed parse is retried as a different schema; corrupt checked
// prefixes remain visible beside their error instead of hiding the inventory.
func (j *HelmUninstallJournal) RecoverHistory(id string, archived bool) (*HelmJournalRecovery, error) {
	if j == nil || j.store == nil {
		return nil, helmjournal.ErrClosed
	}
	s, identity, err := j.store.LoadIdentified(id, archived)
	if err != nil && !errors.Is(err, helmjournal.ErrCorrupt) {
		return nil, err
	}
	// An ineligible record moved into the archive can have a valid checked
	// prefix while LoadIdentified still returns ErrCorrupt. The wrapper must
	// retain that failure even if a caller neglects the accompanying error.
	if errors.Is(err, helmjournal.ErrCorrupt) {
		s.Corrupt, s.Integrity = true, helmjournal.IntegrityCorrupt
	}
	switch {
	case s.Version == 2 && s.Install != nil:
		return &HelmJournalRecovery{version: 2, install: helmInstallRecoveryFromSnapshot(s, archived)}, err
	case (s.Version == 1 || s.Version == 0 && s.Corrupt) && s.Install == nil:
		return &HelmJournalRecovery{version: s.Version, uninstall: helmUninstallRecoveryFromSnapshot(s, identity, archived)}, err
	default:
		return nil, helmjournal.ErrInvalid
	}
}

// HelmInstallRecovery contains immutable checked historical identities only.
// It has no writer, Backend, original session, credential or payload. Its
// private state also distinguishes separate loads of the same journal ID.
type HelmInstallRecovery struct{ state *helmInstallRecoveryState }

type helmInstallRecoveryState struct {
	snapshot helmjournal.Snapshot
	archived bool
}

func (j *HelmUninstallJournal) RecoverInstall(id string, archived bool) (*HelmInstallRecovery, error) {
	if j == nil || j.store == nil {
		return nil, helmjournal.ErrClosed
	}
	r, err := j.store.RecoverInstall(id, archived)
	if r == nil || err != nil && !errors.Is(err, helmjournal.ErrCorrupt) {
		return nil, err
	}
	s := r.Snapshot()
	if errors.Is(err, helmjournal.ErrCorrupt) {
		s.Corrupt, s.Integrity = true, helmjournal.IntegrityCorrupt
	}
	return helmInstallRecoveryFromSnapshot(s, archived), err
}

func cloneHelmInstallSnapshot(s helmjournal.Snapshot) helmjournal.Snapshot {
	s.Plan.Targets = append(s.Plan.Targets[:0:0], s.Plan.Targets...)
	if s.Install != nil {
		p := *s.Install
		p.Targets = append([]helmjournal.InstallTarget(nil), p.Targets...)
		s.Install = &p
	}
	s.Effects = append([]helmjournal.Effect(nil), s.Effects...)
	return s
}

func helmInstallRecoveryFromSnapshot(s helmjournal.Snapshot, archived bool) *HelmInstallRecovery {
	return &HelmInstallRecovery{state: &helmInstallRecoveryState{snapshot: cloneHelmInstallSnapshot(s), archived: archived}}
}

func (r *HelmInstallRecovery) plan() *helmjournal.InstallPlan {
	if r == nil || r.state == nil {
		return nil
	}
	return r.state.snapshot.Install
}
func (r *HelmInstallRecovery) ID() string {
	if r == nil || r.state == nil {
		return ""
	}
	return r.state.snapshot.ID
}
func (r *HelmInstallRecovery) CandidateID() string {
	if p := r.plan(); p != nil {
		return p.CandidateID
	}
	return ""
}
func (r *HelmInstallRecovery) PreparationID() string {
	if p := r.plan(); p != nil {
		return p.PreparationID
	}
	return ""
}
func (r *HelmInstallRecovery) Namespace() string {
	if p := r.plan(); p != nil {
		return p.Namespace
	}
	return ""
}
func (r *HelmInstallRecovery) NamespaceIdentity() (string, string) {
	if p := r.plan(); p != nil {
		return p.NamespaceUID, p.NamespaceResourceVersion
	}
	return "", ""
}
func (r *HelmInstallRecovery) Release() string {
	if p := r.plan(); p != nil {
		return p.Release
	}
	return ""
}
func (r *HelmInstallRecovery) StorageName() string {
	if p := r.plan(); p != nil {
		return p.StorageName
	}
	return ""
}
func (r *HelmInstallRecovery) Targets() []helmjournal.InstallTarget {
	if p := r.plan(); p != nil {
		return append([]helmjournal.InstallTarget(nil), p.Targets...)
	}
	return nil
}
func (r *HelmInstallRecovery) Effects() []helmjournal.Effect {
	if r == nil || r.state == nil {
		return nil
	}
	return append([]helmjournal.Effect(nil), r.state.snapshot.Effects...)
}
func (r *HelmInstallRecovery) Outcome() string {
	if r == nil || r.state == nil {
		return helmjournal.NotStarted
	}
	if !r.Complete() {
		return helmjournal.Unknown
	}
	return installExecutionOutcome(r.state.snapshot.Effects)
}
func (r *HelmInstallRecovery) StartedAt() time.Time {
	if p := r.plan(); p != nil {
		return p.Started
	}
	return time.Time{}
}
func (r *HelmInstallRecovery) FinishedAt() time.Time {
	if r == nil || r.state == nil {
		return time.Time{}
	}
	return r.state.snapshot.Finished
}
func (r *HelmInstallRecovery) ExpiresAt() time.Time {
	if p := r.plan(); p != nil {
		return p.Expires
	}
	return time.Time{}
}
func (r *HelmInstallRecovery) Complete() bool {
	return r != nil && r.state != nil && r.state.snapshot.Closed && !r.Corrupt()
}
func (r *HelmInstallRecovery) Corrupt() bool {
	return r == nil || r.state == nil || r.state.snapshot.Corrupt || r.state.snapshot.Integrity != helmjournal.IntegrityValid
}
func (r *HelmInstallRecovery) Archived() bool {
	return r != nil && r.state != nil && r.state.archived
}
