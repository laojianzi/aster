package kube

import (
	"context"
	"io"
	"net/http"

	"github.com/laojianzi/aster/internal/helmrelease"
)

// InspectHelmRelease reads exactly the selected object, never a full list. A
// successful summary binds only the returned object version, not future state.
func (b *Backend) InspectHelmRelease(ctx context.Context, namespace string, row helmrelease.Revision, acknowledged bool) (helmrelease.Inspection, error) {
	empty := helmrelease.Inspection{}
	if !acknowledged {
		return empty, helmrelease.ErrInspectionConsent
	}
	if !helmrelease.ValidSelection(namespace, row) {
		return empty, helmrelease.ErrInspectionSelection
	}
	ctx, cancel := context.WithTimeout(ctx, helmrelease.ReadTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if err := b.config.RateLimiter.Wait(ctx); err != nil {
		if ctx.Err() != nil {
			return empty, ctx.Err()
		}
		return empty, helmrelease.ErrInspectionUnavailable
	}
	u := b.writes.Get().AbsPath("/api", "v1", "namespaces", namespace, "secrets", row.StorageName).URL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return empty, helmrelease.ErrInspectionUnavailable
	}
	req.Header.Set("Accept", "application/json")
	response, err := b.metricsHTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return empty, ctx.Err()
		}
		return empty, helmrelease.ErrInspectionUnavailable
	}
	defer response.Body.Close()
	// No error-body consumption, raw text, redirects, fallback or automatic retry.
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden, http.StatusUnauthorized:
		return empty, helmrelease.ErrInspectionForbidden
	case http.StatusNotFound, http.StatusGone, http.StatusConflict:
		return empty, helmrelease.ErrInspectionStale
	default:
		return empty, helmrelease.ErrInspectionUnavailable
	}
	if response.ContentLength > helmrelease.MaxSecretBytes {
		return empty, helmrelease.ErrInspectionBudget
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, helmrelease.MaxSecretBytes+1))
	defer clear(data)
	if ctx.Err() != nil {
		return empty, ctx.Err()
	}
	if len(data) > helmrelease.MaxSecretBytes {
		return empty, helmrelease.ErrInspectionBudget
	}
	if err != nil {
		return empty, helmrelease.ErrInspectionUnavailable
	}
	return helmrelease.InspectSecret(ctx, data, namespace, row)
}
