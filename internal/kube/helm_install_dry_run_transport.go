package kube

import (
	"bytes"
	"context"
	"io"
	"mime"
	"net/http"

	"github.com/laojianzi/aster/internal/helmrelease"
)

// This path has no method argument: only create POST with all dry-run options
// can be constructed. Reuse current identity, TLS, redirect and rate budgets.
func (b *Backend) helmInstallDryRunPOST(ctx context.Context, ns, resource string, body []byte, remaining *int) (data []byte, attempted bool, err error) {
	if ctx == nil || b == nil || b.config == nil || !helmrelease.ValidNamespace(ns) ||
		(resource != "secrets" && resource != "configmaps") || len(body) == 0 || len(body) > helmrelease.MaxSecretBytes || remaining == nil {
		return nil, false, ErrHelmDryRunRejected
	}
	if err = ctx.Err(); err != nil {
		return nil, false, err
	}
	limit := min(helmrelease.MaxSecretBytes, *remaining)
	if limit <= 0 {
		return nil, false, helmrelease.ErrLiveBudget
	}
	if err = b.config.RateLimiter.Wait(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, false, ErrHelmDryRunRejected
	}
	if err = ctx.Err(); err != nil {
		return nil, false, err
	}
	u := b.writes.Get().AbsPath("/api", "v1", "namespaces", ns, resource).URL()
	q := u.Query()
	q.Set("dryRun", "All")
	q.Set("fieldValidation", "Strict")
	q.Set("fieldManager", helmrelease.InstallFieldManager)
	u.RawQuery = q.Encode()
	owned := &helmDryRunBody{data: bytes.Clone(body)}
	defer owned.Close()
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), owned)
	if e != nil {
		return nil, false, ErrHelmDryRunRejected
	}
	req.ContentLength = int64(len(body))
	req.GetBody = nil
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	response, e := b.metricsHTTP.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, true, ctx.Err()
		}
		return nil, true, ErrHelmDryRunRejected
	}
	defer response.Body.Close()
	if err = ctx.Err(); err != nil {
		return nil, true, err
	}
	// Never read Kubernetes error bodies; they can echo values or Secret bytes.
	switch response.StatusCode {
	case http.StatusForbidden, http.StatusUnauthorized:
		return nil, true, helmrelease.ErrLiveForbidden
	case http.StatusConflict:
		return nil, true, helmrelease.ErrInstallConflict
	case http.StatusCreated:
	default:
		return nil, true, ErrHelmDryRunRejected
	}
	media, _, e := mime.ParseMediaType(response.Header.Get("Content-Type"))
	encoding := response.Header.Values("Content-Encoding")
	if e != nil || media != "application/json" || len(response.Header.Values("Content-Type")) != 1 ||
		len(response.Header.Values("Warning")) != 0 || response.Uncompressed || len(encoding) > 1 ||
		len(encoding) == 1 && encoding[0] != "identity" {
		return nil, true, ErrHelmDryRunResponse
	}
	if response.ContentLength > int64(limit) {
		return nil, true, helmrelease.ErrLiveBudget
	}
	data, e = io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if e != nil || ctx.Err() != nil || len(data) > limit || response.ContentLength >= 0 && int64(len(data)) != response.ContentLength || len(response.Trailer.Values("Warning")) != 0 {
		tooLarge := len(data) > limit
		clear(data)
		if ctx.Err() != nil {
			return nil, true, ctx.Err()
		}
		if tooLarge {
			return nil, true, helmrelease.ErrLiveBudget
		}
		return nil, true, ErrHelmDryRunResponse
	}
	*remaining -= len(data)
	return data, true, nil
}
