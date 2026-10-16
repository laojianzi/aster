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
	var result helmrelease.Inspection
	err := b.readHelmRevision(ctx, namespace, row, acknowledged, func(ctx context.Context, data []byte) error {
		var err error
		result, err = helmrelease.InspectSecret(ctx, data, namespace, row)
		return err
	})
	return result, err
}

// The callback is internal and runs within the same timeout as rate limiting
// and transport. Neither caller can bypass consent or return raw Secret bytes.
func (b *Backend) readHelmRevision(ctx context.Context, namespace string, row helmrelease.Revision, acknowledged bool, inspect func(context.Context, []byte) error) error {
	if !acknowledged {
		return helmrelease.ErrInspectionConsent
	}
	if !helmrelease.ValidSelection(namespace, row) {
		return helmrelease.ErrInspectionSelection
	}
	ctx, cancel := context.WithTimeout(ctx, helmrelease.ReadTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.config.RateLimiter.Wait(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return helmrelease.ErrInspectionUnavailable
	}
	u := b.writes.Get().AbsPath("/api", "v1", "namespaces", namespace, "secrets", row.StorageName).URL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return helmrelease.ErrInspectionUnavailable
	}
	req.Header.Set("Accept", "application/json")
	response, err := b.metricsHTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return helmrelease.ErrInspectionUnavailable
	}
	defer response.Body.Close()
	// No error-body consumption, raw text, redirects, fallback or automatic retry.
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden, http.StatusUnauthorized:
		return helmrelease.ErrInspectionForbidden
	case http.StatusNotFound, http.StatusGone, http.StatusConflict:
		return helmrelease.ErrInspectionStale
	default:
		return helmrelease.ErrInspectionUnavailable
	}
	if response.ContentLength > helmrelease.MaxSecretBytes {
		return helmrelease.ErrInspectionBudget
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, helmrelease.MaxSecretBytes+1))
	defer clear(data)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(data) > helmrelease.MaxSecretBytes {
		return helmrelease.ErrInspectionBudget
	}
	if err != nil {
		return helmrelease.ErrInspectionUnavailable
	}
	return inspect(ctx, data)
}
