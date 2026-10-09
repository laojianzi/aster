package kube

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/laojianzi/aster/internal/schemaassist"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
)

// SchemaDocument only requests a known API-server path. The advertised URL is
// validated, never used as a destination. It shares this identity's credential
// transport, cancellation and limiter; there is no cross-session schema cache.
func (b *Backend) SchemaDocument(ctx context.Context, gvk schema.GroupVersionKind) (*schemaassist.Document, error) {
	if gvk.Version == "" || len(validation.IsDNS1035Label(gvk.Version)) > 0 || len(gvk.Kind) == 0 || len(gvk.Kind) > 128 || len(validation.IsCIdentifier(gvk.Kind)) > 0 || (gvk.Group != "" && len(validation.IsDNS1123Subdomain(gvk.Group)) > 0) {
		return nil, schemaassist.ErrInvalid
	}
	key := "api/" + gvk.Version
	if gvk.Group != "" {
		key = "apis/" + gvk.Group + "/" + gvk.Version
	}
	data, err := b.schemaJSON(ctx, "/openapi/v3", "", schemaassist.MaxIndexBytes)
	if err != nil {
		return nil, err
	}
	value, err := schemaassist.ParseJSON(ctx, data, schemaassist.MaxIndexBytes)
	if err != nil {
		return nil, err
	}
	index, ok := value.(map[string]any)
	if !ok {
		return nil, schemaassist.ErrInvalid
	}
	paths, _ := index["paths"].(map[string]any)
	entry, _ := paths[key].(map[string]any)
	advertised, _ := entry["serverRelativeURL"].(string)
	if advertised == "" {
		return nil, schemaassist.ErrNotFound
	}
	path := "/openapi/v3/" + key
	query, err := schemaQuery(advertised, path)
	if err != nil {
		return nil, err
	}
	data, err = b.schemaJSON(ctx, path, query, schemaassist.MaxDocumentBytes)
	if err != nil {
		return nil, err
	}
	return schemaassist.Decode(ctx, data, gvk)
}
func schemaQuery(advertised, path string) (string, error) {
	if len(advertised) > len(path)+256 {
		return "", schemaassist.ErrInvalid
	}
	u, err := url.Parse(advertised)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.Fragment != "" || u.RawPath != "" || u.Path != path || u.Opaque != "" || u.ForceQuery {
		return "", schemaassist.ErrInvalid
	}
	if u.RawQuery == "" {
		return "", nil
	}
	if !strings.HasPrefix(u.RawQuery, "hash=") {
		return "", schemaassist.ErrInvalid
	}
	hash := strings.TrimPrefix(u.RawQuery, "hash=")
	if len(hash) == 0 || len(hash) > 128 || strings.Trim(hash, "0123456789abcdefABCDEF") != "" {
		return "", schemaassist.ErrInvalid
	}
	return "hash=" + hash, nil
}
func (b *Backend) schemaJSON(ctx context.Context, path, query string, limit int) ([]byte, error) {
	if err := b.config.RateLimiter.Wait(ctx); err != nil {
		return nil, err
	}
	address := b.writes.Get().AbsPath(path).URL()
	address.RawQuery = query
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address.String(), nil)
	if err != nil {
		return nil, schemaassist.ErrInvalid
	}
	req.Header.Set("Accept", "application/json")
	response, err := b.metricsHTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, schemaassist.ErrUnavailable
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusForbidden, http.StatusUnauthorized:
		return nil, schemaassist.ErrForbidden
	case http.StatusNotFound:
		return nil, schemaassist.ErrNotFound
	case http.StatusOK:
	default:
		return nil, schemaassist.ErrUnavailable
	}
	if response.ContentLength > int64(limit) {
		return nil, schemaassist.ErrLimit
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, schemaassist.ErrUnavailable
	}
	if len(data) > limit {
		return nil, schemaassist.ErrLimit
	}
	return data, nil
}
