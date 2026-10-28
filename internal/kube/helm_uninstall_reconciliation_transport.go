package kube

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
	"k8s.io/apimachinery/pkg/util/validation"
)

// This reader intentionally has no execution runner, patch, delete, dry-run or
// retry path. Its fixed routes admit only complete namespace Secret metadata
// lists and individual core/v1 ConfigMap metadata reads.
type helmReconciliationRead struct {
	b                *Backend
	ctx              context.Context
	active, dispatch func() error
	namespace        string
	row              helmrelease.Revision
	remaining        int
}

func (r *helmReconciliationRead) failure(fallback error) error {
	if err := r.active(); err != nil {
		return err
	}
	return fallback
}

func (r *helmReconciliationRead) get(resource, name, token string, limit int) ([]byte, int, error) {
	if r.namespace == "" || len(validation.IsDNS1123Label(r.namespace)) != 0 ||
		name != "" && (len(name) > 253 || len(validation.IsDNS1123Subdomain(name)) != 0) ||
		resource != "secrets" && resource != "configmaps" ||
		resource == "secrets" && name != "" || resource == "configmaps" && (name == "" || token != "") {
		return nil, 0, ErrHelmReconciliationResponse
	}
	if err := r.active(); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || r.remaining <= 0 {
		return nil, 0, ErrHelmReconciliationBudget
	}
	path := []string{"/api", "v1", "namespaces", r.namespace, resource}
	if name != "" {
		path = append(path, name)
	}
	u := r.b.writes.Get().AbsPath(path...).URL()
	if u.RawQuery != "" {
		return nil, 0, ErrHelmReconciliationResponse
	}
	accept := liveMetadataAccept
	if resource == "secrets" {
		accept = helmrelease.Accept
		query := u.Query()
		query.Set("labelSelector", "owner=helm")
		query.Set("limit", strconv.Itoa(helmrelease.PageSize))
		if token != "" {
			query.Set("continue", token)
		}
		u.RawQuery = query.Encode()
	}
	return r.getMetadata(u, accept, resource == "configmaps", limit)
}

// Only the checked route builders call this shared bounded GET transport. The
// legacy get entry point retains its original route/verb restrictions.
func (r *helmReconciliationRead) getMetadata(u *url.URL, accept string, allowMissing bool, limit int) ([]byte, int, error) {
	if err := r.active(); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || r.remaining <= 0 {
		return nil, 0, ErrHelmReconciliationBudget
	}
	if err := r.b.config.RateLimiter.Wait(r.ctx); err != nil {
		return nil, 0, r.failure(ErrHelmReconciliationResponse)
	}
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, ErrHelmReconciliationResponse
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Encoding", "identity")
	if err := r.dispatch(); err != nil {
		return nil, 0, err
	}
	resp, err := r.b.metricsHTTP.Do(req)
	if err != nil {
		return nil, 0, r.failure(ErrHelmReconciliationResponse)
	}
	defer resp.Body.Close()
	if err := r.active(); err != nil {
		return nil, 0, err
	}
	if !validHelmExecutionHeaders(resp) || resp.Uncompressed {
		return nil, 0, ErrHelmReconciliationResponse
	}
	if resp.StatusCode != http.StatusOK && (!allowMissing || resp.StatusCode != http.StatusNotFound) {
		// Do not consume or retain untrusted error messages, credential diagnostics
		// or arbitrary proxy bodies. Failure is never projected as absence.
		return nil, 0, ErrHelmReconciliationResponse
	}
	if resp.StatusCode == http.StatusNotFound {
		limit = min(limit, 4096)
	}
	limit = min(limit, r.remaining)
	if resp.ContentLength > int64(limit) {
		return nil, 0, ErrHelmReconciliationBudget
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	closeErr := resp.Body.Close()
	if err != nil || closeErr != nil || len(data) > limit || resp.ContentLength >= 0 && int64(len(data)) != resp.ContentLength || len(resp.Trailer.Values("Warning")) != 0 {
		oversized := len(data) > limit
		clear(data)
		if oversized {
			return nil, 0, r.failure(ErrHelmReconciliationBudget)
		}
		return nil, 0, r.failure(ErrHelmReconciliationResponse)
	}
	if err := r.active(); err != nil {
		clear(data)
		return nil, 0, err
	}
	r.remaining -= len(data)
	return data, resp.StatusCode, nil
}

// Reuse the qualified pure metadata decoder and all established namespace
// pagination/count/byte rules. Independent collection RVs may advance due to
// unrelated releases; each snapshot itself must remain internally consistent.
func (r *helmReconciliationRead) snapshot() (helmrelease.Snapshot, error) {
	return r.snapshotWith(helmrelease.DecodePage)
}

func (r *helmReconciliationRead) snapshotWith(decode func([]byte, string) (helmrelease.Page, error)) (helmrelease.Snapshot, error) {
	empty := helmrelease.Snapshot{}
	result := helmrelease.Snapshot{Namespace: r.namespace}
	next := ""
	tokens, names, uids := map[string]bool{}, map[string]bool{}, map[string]bool{}
	remaining := helmrelease.MaxTotalBytes
	for count := 0; count < helmrelease.MaxPages; count++ {
		data, _, err := r.get("secrets", "", next, min(remaining, helmrelease.MaxPageBytes))
		if err != nil {
			return empty, err
		}
		remaining -= len(data)
		page, err := decode(data, r.namespace)
		clear(data)
		if err != nil {
			return empty, r.failure(ErrHelmReconciliationResponse)
		}
		if result.ResourceVersion == "" {
			result.ResourceVersion = page.ResourceVersion
		}
		if result.ResourceVersion != page.ResourceVersion {
			return empty, ErrHelmReconciliationResponse
		}
		if len(result.Revisions)+len(page.Revisions) > helmrelease.MaxRevisions {
			return empty, ErrHelmReconciliationBudget
		}
		for i, row := range page.Revisions {
			if names[row.StorageName] || uids[page.UIDs[i]] {
				return empty, ErrHelmReconciliationResponse
			}
			names[row.StorageName], uids[page.UIDs[i]] = true, true
			result.Revisions = append(result.Revisions, row)
		}
		if err := r.active(); err != nil {
			return empty, err
		}
		if page.Continue == "" {
			helmrelease.Sort(result.Revisions)
			result.ObservedAt = time.Now()
			return result, nil
		}
		if tokens[page.Continue] {
			return empty, ErrHelmReconciliationResponse
		}
		tokens[page.Continue] = true
		next = page.Continue
	}
	return empty, ErrHelmReconciliationBudget
}

func (r *helmReconciliationRead) soleRevision() error {
	snapshot, err := r.snapshot()
	if err != nil {
		return err
	}
	rows, err := helmrelease.LatestRevisionSet(snapshot, r.namespace, r.row)
	if err != nil || len(rows) != 1 || rows[0] != r.row {
		return r.failure(ErrHelmReconciliationChanged)
	}
	return r.active()
}

func (r *helmReconciliationRead) target(target helmrelease.UninstallTarget) (HelmUninstallRecoveryItem, error) {
	empty := HelmUninstallRecoveryItem{}
	if !validHelmReconciliationTarget(r.namespace, target) {
		return empty, ErrHelmReconciliationResponse
	}
	data, status, err := r.get("configmaps", target.Name, "", maxLiveObjectBytes)
	if err != nil {
		return empty, err
	}
	defer clear(data)
	if !nonNullHelmExecutionEnvelope(data) {
		return empty, ErrHelmReconciliationResponse
	}
	item := HelmUninstallRecoveryItem{Target: target, ObservedAt: time.Now()}
	if status == http.StatusNotFound {
		// Reuse only the pure exact-NotFound validator, never execution's mutable
		// runner or live-object eligibility rules. A replacement is a readable
		// current fact here, while it would stop an executor.
		absent, err := inspectHelmExecutionAbsence(data, status, target, r.row.Release)
		if err != nil || !absent {
			return empty, ErrHelmReconciliationResponse
		}
		item.State = HelmRecoveryTargetAbsent
		return item, r.active()
	}
	stored := helmrelease.UninstallItem{APIVersion: "v1", Kind: "ConfigMap", Namespace: target.Namespace, Name: target.Name}
	current, err := projectLiveMetadata(data, status, stored, "configmaps", target.Namespace, r.row.Release, r.namespace)
	if err != nil || current.State != "present" || !safeHelmExecutionID(current.UID, 128) || !safeHelmExecutionID(current.ResourceVersion, 256) {
		return empty, ErrHelmReconciliationResponse
	}
	item.UID, item.ResourceVersion, item.Markers = current.UID, current.ResourceVersion, current.Markers
	item.Terminating, item.Finalizers, item.Owners, item.Keep, item.Hook = current.Terminating, current.Finalizers, current.Owners, current.Keep, current.Hook
	switch {
	case current.UID != target.UID:
		item.State = HelmRecoveryTargetReplacement
	case current.ResourceVersion != target.ResourceVersion:
		item.State = HelmRecoveryTargetModified
	default:
		item.State = HelmRecoveryTargetOriginal
	}
	return item, r.active()
}
