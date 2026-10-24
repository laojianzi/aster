package kube

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
)

// HelmReleaseMetadata reads only one namespace's Secret-driver metadata with the
// already resolved current identity. It does not use Helm, discovery, dynamic
// full-object reads, payload decoding, retry, other drivers or any write verb.
func (b *Backend) HelmReleaseMetadata(ctx context.Context, namespace string) (helmrelease.Snapshot, error) {
	return b.helmReleaseMetadata(ctx, namespace, helmrelease.DecodePage)
}

// Both readers share one bounded transport, pagination and complete-snapshot contract.
func (b *Backend) helmReleaseMetadata(ctx context.Context, namespace string, decode func([]byte, string) (helmrelease.Page, error)) (helmrelease.Snapshot, error) {
	empty := helmrelease.Snapshot{}
	if !helmrelease.ValidNamespace(namespace) {
		return empty, helmrelease.ErrNamespace
	}
	ctx, cancel := context.WithTimeout(ctx, helmrelease.ReadTimeout)
	defer cancel()
	result := helmrelease.Snapshot{Namespace: namespace}
	next := ""
	tokens, names, uids := map[string]bool{}, map[string]bool{}, map[string]bool{}
	totalBytes := 0
	for count := 0; count < helmrelease.MaxPages; count++ {
		if err := ctx.Err(); err != nil {
			return empty, err
		}
		data, err := b.helmMetadataPage(ctx, namespace, next, helmrelease.MaxTotalBytes-totalBytes)
		if err != nil {
			return empty, err
		}
		totalBytes += len(data)
		page, err := decode(data, namespace)
		if err != nil {
			return empty, err
		}
		if result.ResourceVersion == "" {
			result.ResourceVersion = page.ResourceVersion
		}
		if result.ResourceVersion != page.ResourceVersion {
			return empty, helmrelease.ErrInvalid
		}
		if len(result.Revisions)+len(page.Revisions) > helmrelease.MaxRevisions {
			return empty, helmrelease.ErrBudget
		}
		for i, row := range page.Revisions {
			if names[row.StorageName] || uids[page.UIDs[i]] {
				return empty, helmrelease.ErrInvalid
			}
			names[row.StorageName], uids[page.UIDs[i]] = true, true
			result.Revisions = append(result.Revisions, row)
		}
		if err := ctx.Err(); err != nil {
			return empty, err
		}
		if page.Continue == "" {
			helmrelease.Sort(result.Revisions)
			result.ObservedAt = time.Now().UTC()
			return result, nil
		}
		if tokens[page.Continue] {
			return empty, helmrelease.ErrInvalid
		}
		tokens[page.Continue] = true
		next = page.Continue
	}
	return empty, helmrelease.ErrBudget
}

func (b *Backend) helmMetadataPage(ctx context.Context, namespace, token string, remaining int) ([]byte, error) {
	if remaining <= 0 {
		return nil, helmrelease.ErrBudget
	}
	if err := b.config.RateLimiter.Wait(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, helmrelease.ErrUnavailable
	}
	// URL construction preserves an API-server path prefix and escapes all query
	// values. Namespace validation precedes construction; tokens never form paths.
	u := b.writes.Get().AbsPath("/api", "v1", "namespaces", namespace, "secrets").URL()
	query := u.Query()
	query.Set("labelSelector", "owner=helm")
	query.Set("limit", strconv.Itoa(helmrelease.PageSize))
	if token != "" {
		query.Set("continue", token)
	}
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, helmrelease.ErrUnavailable
	}
	req.Header.Set("Accept", helmrelease.Accept)
	response, err := b.metricsHTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, helmrelease.ErrUnavailable
	}
	defer response.Body.Close()
	// Never consume or echo error bodies, URLs, tokens or credential diagnostics.
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden, http.StatusUnauthorized:
		return nil, helmrelease.ErrForbidden
	case http.StatusNotAcceptable:
		return nil, helmrelease.ErrUnsupported
	case http.StatusGone:
		return nil, helmrelease.ErrExpired
	default:
		return nil, helmrelease.ErrUnavailable
	}
	limit := min(remaining, helmrelease.MaxPageBytes)
	if response.ContentLength > int64(limit) {
		return nil, helmrelease.ErrBudget
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, helmrelease.ErrUnavailable
	}
	if len(data) > limit {
		return nil, helmrelease.ErrBudget
	}
	return data, nil
}
