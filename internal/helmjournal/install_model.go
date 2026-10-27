package helmjournal

import (
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
	"k8s.io/apimachinery/pkg/util/validation"
)

// InstallPlan is an inert pre-create identity. There is deliberately no storage
// UID/RV, credential, session, request body or execution consent in this schema.
// Content digests describe exact frozen create bytes, not authentic ownership.
// The connection Binding must use the existing shared connection digest, not a
// new action-specific digest that could bypass unresolved uninstall history.
type InstallPlan struct {
	Binding                  string          `json:"binding"`
	CandidateID              string          `json:"candidateID"`
	PreparationID            string          `json:"preparationID"`
	Namespace                string          `json:"namespace"`
	NamespaceUID             string          `json:"namespaceUID"`
	NamespaceResourceVersion string          `json:"namespaceResourceVersion"`
	Release                  string          `json:"release"`
	StorageName              string          `json:"storageName"`
	StorageDigest            string          `json:"storageDigest"`
	Targets                  []InstallTarget `json:"targets"`
	Started                  time.Time       `json:"started"`
	Expires                  time.Time       `json:"expires"`
}

type InstallTarget struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

func validInstallRelease(name string) bool {
	return name != "" && len(name) <= 53 && len(validation.IsDNS1123Label(name)) == 0
}

func validInstallPlan(p InstallPlan) bool {
	if !validHex(p.Binding, 64) || !validHex(p.CandidateID, 32) || !validHex(p.PreparationID, 32) ||
		!helmrelease.ValidNamespace(p.Namespace) || !validIdentity(p.NamespaceUID, 128) ||
		!validIdentity(p.NamespaceResourceVersion, 256) || !validInstallRelease(p.Release) ||
		p.StorageName != "sh.helm.release.v1."+p.Release+".v1" || !validHex(p.StorageDigest, 64) ||
		!validTime(p.Started) || !validTime(p.Expires) || !p.Expires.After(p.Started) ||
		p.Expires.Sub(p.Started) > helmrelease.PreparationLifetime || len(p.Targets) < 1 || len(p.Targets) > 32 {
		return false
	}
	seen := make(map[string]bool, len(p.Targets))
	for _, t := range p.Targets {
		if t.Name == "" || len(t.Name) > 253 || len(validation.IsDNS1123Subdomain(t.Name)) != 0 || !validHex(t.Digest, 64) || seen[t.Name] {
			return false
		}
		seen[t.Name] = true
	}
	return true
}

func cloneInstallPlan(p InstallPlan) InstallPlan {
	p.Targets = append([]InstallTarget(nil), p.Targets...)
	return p
}

func initialInstallSnapshot(id string, p InstallPlan) Snapshot {
	p = cloneInstallPlan(p)
	s := Snapshot{ID: id, Version: 2, Action: "install", Install: &p, Integrity: IntegrityValid}
	s.Effects = append(s.Effects, Effect{Kind: "create-secret", Namespace: p.Namespace, Name: p.StorageName, ContentDigest: p.StorageDigest, Outcome: NotStarted})
	for _, t := range p.Targets {
		s.Effects = append(s.Effects, Effect{Kind: "create-configmap", Namespace: p.Namespace, Name: t.Name, ContentDigest: t.Digest, Outcome: NotStarted})
	}
	s.Effects = append(s.Effects, Effect{Kind: "deploy", Namespace: p.Namespace, Name: p.StorageName, Outcome: NotStarted})
	return s
}

// Installation has no pre-create UID. Cross-operation matching deliberately
// ignores namespace UID/revision so replacement cannot evade an unresolved name.
// Pure v1-v1 checks retain the original storage-UID predicate.
func sameAdmissionRelease(s Snapshot, q AdmissionScope) bool {
	if s.Version == 2 && s.Install != nil {
		p := s.Install
		return p.Binding == q.Binding && p.Namespace == q.Namespace && (q.Release == "" || p.Release == q.Release)
	}
	if s.Plan.Binding != q.Binding || s.Plan.Namespace != q.Namespace {
		return false
	}
	if q.StorageUID == "" {
		return s.Plan.Revision.Release == q.Release
	}
	return s.Plan.Revision.UID == q.StorageUID
}

// InstallRecovery is an immutable, offline view. There is intentionally no
// resume method, writer handle, network callback or authority reconstruction.
type InstallRecovery struct{ snapshot Snapshot }

func (r *InstallRecovery) Snapshot() Snapshot {
	if r == nil {
		return Snapshot{}
	}
	return cloneSnapshot(r.snapshot)
}
func (s *Store) RecoverInstall(id string, archived bool) (*InstallRecovery, error) {
	snap, _, err := s.LoadIdentified(id, archived)
	if snap.Version != 2 || snap.Install == nil {
		if err != nil {
			return nil, err
		}
		return nil, ErrInvalid
	}
	return &InstallRecovery{snapshot: cloneSnapshot(snap)}, err
}
