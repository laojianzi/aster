package kube

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/laojianzi/aster/internal/resourcemetrics"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
)

// MetricsObject negotiates only known metrics versions advertised by this API
// server. The same credentialed transport and rate limiter as other reads apply.
func (b *Backend) MetricsObject(ctx context.Context, gvr schema.GroupVersionResource, ns, name string) (*unstructured.Unstructured, error) {
	if !resourcemetrics.Supported(gvr) || len(validation.IsDNS1123Subdomain(name)) > 0 || (gvr.Resource == "pods" && len(validation.IsDNS1123Label(ns)) > 0) || (gvr.Resource == "nodes" && ns != "") {
		return nil, resourcemetrics.ErrInvalid
	}
	data, status, err := b.metricsJSON(ctx, []string{"/apis", "metrics.k8s.io"})
	if status == http.StatusNotFound {
		return nil, resourcemetrics.ErrNotInstalled
	}
	if err != nil {
		return nil, err
	}
	var group metav1.APIGroup
	if json.Unmarshal(data, &group) != nil || group.Name != "metrics.k8s.io" {
		return nil, resourcemetrics.ErrInvalid
	}
	version := ""
	for _, known := range []string{"v1", "v1beta1"} {
		for _, v := range group.Versions {
			if v.Version == known && v.GroupVersion == "metrics.k8s.io/"+known {
				version = known
				break
			}
		}
		if version != "" {
			break
		}
	}
	if version == "" {
		return nil, resourcemetrics.ErrUnsupported
	}
	path := []string{"/apis", "metrics.k8s.io", version}
	if ns != "" {
		path = append(path, "namespaces", ns)
	}
	path = append(path, gvr.Resource, name)
	data, status, err = b.metricsJSON(ctx, path)
	if status == http.StatusNotFound {
		return nil, resourcemetrics.ErrNoSample
	}
	if err != nil {
		return nil, err
	}
	obj := &unstructured.Unstructured{}
	if obj.UnmarshalJSON(data) != nil || obj.GetAPIVersion() != "metrics.k8s.io/"+version {
		return nil, resourcemetrics.ErrInvalid
	}
	return obj, nil
}

// Limit decoded response bytes (including error bodies), never echo remote error
// content or URLs, and never follow redirects or replay a failed request here.
func (b *Backend) metricsJSON(ctx context.Context, path []string) ([]byte, int, error) {
	if err := b.config.RateLimiter.Wait(ctx); err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.writes.Get().AbsPath(path...).URL().String(), nil)
	if err != nil {
		return nil, 0, resourcemetrics.ErrInvalid
	}
	req.Header.Set("Accept", "application/json")
	response, err := b.metricsHTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, resourcemetrics.ErrUnavailable
	}
	defer response.Body.Close()
	status := response.StatusCode
	if status == http.StatusForbidden || status == http.StatusUnauthorized {
		return nil, status, resourcemetrics.ErrForbidden
	}
	if status != http.StatusOK {
		return nil, status, resourcemetrics.ErrUnavailable
	}
	const maxBytes = 256 << 10
	if response.ContentLength > maxBytes {
		return nil, status, resourcemetrics.ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, status, ctx.Err()
		}
		return nil, status, resourcemetrics.ErrUnavailable
	}
	if len(data) > maxBytes {
		return nil, status, resourcemetrics.ErrInvalid
	}
	return data, status, nil
}
