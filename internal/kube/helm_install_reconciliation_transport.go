package kube

import (
	"net/http"
	"reflect"
	"slices"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	strictjson "sigs.k8s.io/json"
)

type helmInstallReconciliationRead struct {
	read    helmReconciliationRead
	history helmInstallReconciliationHistory
}

type installRecoveryStorage struct {
	item     HelmInstallRecoveryItem
	revision helmrelease.Revision
	listed   bool
}

// The v2-only object builder cannot widen the old v1 observer's routes. Its
// names come from one checked historical plan, never from arbitrary callbacks.
func (r *helmInstallReconciliationRead) exact(resource, name string) ([]byte, int, error) {
	p := r.history.snapshot.Install
	if p == nil || !helmrelease.ValidNamespace(p.Namespace) || p.Namespace != r.read.namespace ||
		name == "" || len(name) > 253 || len(validation.IsDNS1123Subdomain(name)) != 0 {
		return nil, 0, ErrHelmReconciliationResponse
	}
	var path []string
	switch resource {
	case "namespaces":
		if name != p.Namespace {
			return nil, 0, ErrHelmReconciliationResponse
		}
		path = []string{"/api", "v1", "namespaces", p.Namespace}
	case "secrets":
		if name != p.StorageName {
			return nil, 0, ErrHelmReconciliationResponse
		}
		path = []string{"/api", "v1", "namespaces", p.Namespace, "secrets", p.StorageName}
	case "configmaps":
		found := false
		for _, t := range p.Targets {
			found = found || t.Name == name
		}
		if !found {
			return nil, 0, ErrHelmReconciliationResponse
		}
		return r.read.get(resource, name, "", maxLiveObjectBytes)
	default:
		return nil, 0, ErrHelmReconciliationResponse
	}
	u := r.read.b.writes.Get().AbsPath(path...).URL()
	if u.RawQuery != "" {
		return nil, 0, ErrHelmReconciliationResponse
	}
	return r.read.getMetadata(u, liveMetadataAccept, true, maxLiveObjectBytes)
}

// Strict typed NotFound is the only absence proof. Arbitrary proxy 404s, other
// resource identities, Status UIDs/causes/retry hints and nulls are not absence.
func exactInstallRecoveryAbsence(data []byte, status int, resource, name string) bool {
	if status != http.StatusNotFound || !nonNullHelmExecutionEnvelope(data) {
		return false
	}
	var s metav1.Status
	issues, err := strictjson.UnmarshalStrict(data, &s)
	return err == nil && len(issues) == 0 && s.APIVersion == "v1" && s.Kind == "Status" && s.Status == metav1.StatusFailure &&
		s.Code == 404 && s.Reason == metav1.StatusReasonNotFound && s.Details != nil && s.Details.Name == name &&
		s.Details.Kind == resource && s.Details.Group == "" && s.Details.UID == "" && len(s.Details.Causes) == 0 &&
		s.Details.RetryAfterSeconds == 0 && reflect.DeepEqual(s.ListMeta, metav1.ListMeta{})
}

func (r *helmInstallReconciliationRead) object(resource, kind, name, uid, rv string) (HelmInstallRecoveryItem, metav1.PartialObjectMetadata, error) {
	empty, metadata := HelmInstallRecoveryItem{}, metav1.PartialObjectMetadata{}
	data, status, err := r.exact(resource, name)
	if err != nil {
		return empty, metadata, err
	}
	defer clear(data)
	p := r.history.snapshot.Install
	ns := p.Namespace
	if resource == "namespaces" {
		ns = ""
	}
	item := HelmInstallRecoveryItem{Kind: kind, Namespace: ns, Name: name, HistoricalUID: uid, HistoricalResourceVersion: rv, ObservedAt: time.Now()}
	if status == http.StatusNotFound {
		if !exactInstallRecoveryAbsence(data, status, resource, name) {
			return empty, metadata, ErrHelmReconciliationResponse
		}
		item.State = HelmInstallRecoveryAbsent
		return item, metadata, r.read.active()
	}
	if !nonNullHelmExecutionEnvelope(data) {
		return empty, metadata, ErrHelmReconciliationResponse
	}
	stored := helmrelease.UninstallItem{APIVersion: "v1", Kind: kind, Namespace: ns, Name: name}
	current, err := projectLiveMetadata(data, status, stored, resource, ns, p.Release, p.Namespace)
	if err != nil || current.State != "present" || !safeHelmExecutionID(current.UID, 128) || !safeHelmExecutionID(current.ResourceVersion, 256) {
		return empty, metadata, ErrHelmReconciliationResponse
	}
	// The qualified decoder above validates the entire metadata-only envelope.
	// This local second projection is used only to cross-check storage labels;
	// no arbitrary label/annotation, managed field or response byte is returned.
	issues, err := strictjson.UnmarshalStrict(data, &metadata)
	if err != nil || len(issues) != 0 {
		return empty, metav1.PartialObjectMetadata{}, ErrHelmReconciliationResponse
	}
	item.UID, item.ResourceVersion, item.Markers = current.UID, current.ResourceVersion, current.Markers
	item.Terminating, item.Finalizers, item.Owners, item.Keep, item.Hook = current.Terminating, current.Finalizers, current.Owners, current.Keep, current.Hook
	switch {
	case uid == "":
		item.State = HelmInstallRecoveryUnattributed
	case uid != current.UID:
		item.State = HelmInstallRecoveryReplacement
	case rv != current.ResourceVersion:
		item.State = HelmInstallRecoveryModified
	default:
		item.State = HelmInstallRecoveryOriginal
	}
	return item, metadata, r.read.active()
}

func (r *helmInstallReconciliationRead) namespace() (HelmInstallRecoveryItem, error) {
	p := r.history.snapshot.Install
	item, _, err := r.object("namespaces", "Namespace", p.Namespace, p.NamespaceUID, p.NamespaceResourceVersion)
	if err != nil {
		return HelmInstallRecoveryItem{}, err
	}
	// Resource versions can advance during an offline interval or this read.
	// Namespace replacement or termination cannot authorize reads in a new scope.
	if item.State == HelmInstallRecoveryAbsent || item.UID != p.NamespaceUID || item.Terminating {
		return HelmInstallRecoveryItem{}, ErrHelmReconciliationChanged
	}
	return item, nil
}

func (r *helmInstallReconciliationRead) revisions() ([]helmrelease.Revision, error) {
	s, err := r.read.snapshotWith(helmrelease.DecodeInstallMetadataPage)
	if err != nil {
		return nil, err
	}
	if !safeHelmExecutionID(s.ResourceVersion, 256) {
		return nil, ErrHelmReconciliationResponse
	}
	var rows []helmrelease.Revision
	p := r.history.snapshot.Install
	for _, row := range s.Revisions {
		if !safeHelmExecutionID(row.UID, 128) || !safeHelmExecutionID(row.ResourceVersion, 256) {
			return nil, ErrHelmReconciliationResponse
		}
		if row.Release == p.Release || row.StorageName == p.StorageName {
			rows = append(rows, row)
		}
	}
	return rows, r.read.active()
}

func (r *helmInstallReconciliationRead) storage() (installRecoveryStorage, error) {
	s := r.history.snapshot
	first, last := s.Effects[0], s.Effects[len(s.Effects)-1]
	uid, rv := first.UID, first.ObservedResourceVersion
	// Deploy.ResourceVersion is a PATCH precondition, never its response RV.
	// AcceptedPending already records a real response even without a final GET.
	if last.ObservedResourceVersion != "" && last.UID == uid {
		rv = last.ObservedResourceVersion
	}
	item, meta, err := r.object("secrets", "Secret", s.Install.StorageName, uid, rv)
	if err != nil {
		return installRecoveryStorage{}, err
	}
	out := installRecoveryStorage{item: item}
	if item.State == HelmInstallRecoveryAbsent {
		return out, nil
	}
	owner, ownerExists := meta.Labels["owner"]
	if owner != "helm" {
		out.item.Markers = "missing"
		if ownerExists {
			out.item.Markers = "mismatch"
		}
		return out, nil
	}
	row := helmrelease.Revision{Release: meta.Labels["name"], Number: 1, Status: meta.Labels["status"], StorageName: meta.Name,
		UID: string(meta.UID), ResourceVersion: meta.ResourceVersion}
	if meta.Labels["version"] != "1" || row.Release != s.Install.Release || !helmrelease.ValidSelection(s.Install.Namespace, row) {
		return installRecoveryStorage{}, ErrHelmReconciliationResponse
	}
	out.listed, out.revision, out.item.Markers = true, row, "match"
	return out, nil
}

func (r *helmInstallReconciliationRead) storageHistory(rows []helmrelease.Revision, storage installRecoveryStorage) error {
	count := 0
	for _, row := range rows {
		if row.StorageName == r.history.snapshot.Install.StorageName {
			count++
			if !storage.listed || row != storage.revision {
				return ErrHelmReconciliationChanged
			}
		}
	}
	if storage.listed && count != 1 || !storage.listed && count != 0 {
		return ErrHelmReconciliationChanged
	}
	return nil
}

func sameInstallRecoveryStorage(a, b installRecoveryStorage) bool {
	a.item.ObservedAt, b.item.ObservedAt = time.Time{}, time.Time{}
	return a == b
}

func (r *helmInstallReconciliationRead) observe(expires time.Time) (*HelmInstallRecoveryObservation, error) {
	s := r.history.snapshot
	p := s.Install
	out := &HelmInstallRecoveryObservation{id: s.ID, namespace: p.Namespace, release: p.Release, historyOutcome: r.history.outcome, started: time.Now(), expires: expires}
	if _, err := r.namespace(); err != nil {
		return nil, err
	}
	before, err := r.revisions()
	if err != nil {
		return nil, err
	}
	storageBefore, err := r.storage()
	if err != nil {
		return nil, err
	}
	if err := r.storageHistory(before, storageBefore); err != nil {
		return nil, err
	}
	seenUIDs := map[string]bool{}
	if storageBefore.item.UID != "" {
		seenUIDs[storageBefore.item.UID] = true
	}
	for i, target := range p.Targets {
		e := s.Effects[i+1]
		item, _, err := r.object("configmaps", "ConfigMap", target.Name, e.UID, e.ObservedResourceVersion)
		if err != nil {
			return nil, err
		}
		if item.UID != "" {
			if seenUIDs[item.UID] {
				return nil, ErrHelmReconciliationResponse
			}
			seenUIDs[item.UID] = true
		}
		out.items = append(out.items, item)
	}
	storageAfter, err := r.storage()
	if err != nil {
		return nil, err
	}
	after, err := r.revisions()
	if err != nil {
		return nil, err
	}
	if err := r.storageHistory(after, storageAfter); err != nil {
		return nil, err
	}
	if !slices.Equal(before, after) || !sameInstallRecoveryStorage(storageBefore, storageAfter) {
		return nil, ErrHelmReconciliationChanged
	}
	namespace, err := r.namespace()
	if err != nil {
		return nil, err
	}
	out.storage, out.revisions = storageAfter.item, append([]helmrelease.Revision(nil), after...)
	out.namespaceUID, out.namespaceRV = namespace.UID, namespace.ResourceVersion
	return out, r.read.active()
}
