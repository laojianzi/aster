package kube

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	strictjson "sigs.k8s.io/json"
)

const (
	liveMetadataAccept    = "application/json;as=PartialObjectMetadata;g=meta.k8s.io;v=v1"
	maxLiveGroups         = 8
	maxLiveReadBytes      = 2 << 20 // discovery and object responses combined; lists separately bounded
	maxLiveDiscoveryBytes = 256 << 10
	maxLiveObjectBytes    = 64 << 10
)

type liveRoute struct {
	resource   string
	namespaced bool
}

// ObserveHelmPreview samples metadata only. It does not reopen sensitive release
// content, acquire a lock, run Helm or create a Prepared/delete capability.
func (b *Backend) ObserveHelmPreview(ctx context.Context, p *helmrelease.UninstallPreview, session string, acknowledged bool) (*helmrelease.LiveObservation, error) {
	if !acknowledged {
		return nil, helmrelease.ErrLiveConsent
	}
	started := time.Now()
	if p == nil || !p.Matches(session, p.Namespace(), p.Revision(), started) {
		return nil, helmrelease.ErrLiveExpired
	}
	ctx, cancel := context.WithTimeout(ctx, helmrelease.ReadTimeout)
	defer cancel()
	ctx, expiryCancel := context.WithDeadline(ctx, p.ExpiresAt())
	defer expiryCancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := p.Items()
	groups := map[string]bool{}
	for _, item := range items {
		if !item.ForeignNamespace {
			groups[item.APIVersion] = true
		}
	}
	if len(groups) > maxLiveGroups {
		return nil, helmrelease.ErrLiveBudget
	}
	before, err := b.HelmReleaseMetadata(ctx, p.Namespace())
	if err != nil {
		return nil, err
	}
	if _, err = helmrelease.LatestRevisionSet(before, p.Namespace(), p.Revision()); err != nil {
		return nil, err
	}
	remaining := maxLiveReadBytes
	discovery := map[string]metav1.APIResourceList{}
	observed := make([]helmrelease.LiveItem, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if item.ForeignNamespace {
			observed = append(observed, helmrelease.LiveItem{Stored: item, State: "outside-namespace", ObservedAt: time.Now()})
			continue
		}
		gv, err := schema.ParseGroupVersion(item.APIVersion)
		if err != nil {
			return nil, helmrelease.ErrLiveRouting
		}
		path := []string{"/api", gv.Version}
		if gv.Group != "" {
			path = []string{"/apis", gv.Group, gv.Version}
		}
		list, ok := discovery[item.APIVersion]
		if !ok {
			data, _, err := b.helmLiveGET(ctx, path, "application/json", maxLiveDiscoveryBytes, &remaining, false)
			if err != nil {
				return nil, err
			}
			list, err = decodeLiveDiscovery(data, item.APIVersion)
			clear(data)
			if err != nil {
				return nil, helmrelease.ErrLiveRouting
			}
			discovery[item.APIVersion] = list
		}
		route, err := resolveLiveRoute(list, item.Kind)
		if err != nil {
			return nil, err
		}
		ns := ""
		if route.namespaced {
			ns = p.Namespace()
			if item.Namespace != "" && item.Namespace != ns {
				return nil, helmrelease.ErrLiveRouting
			}
		} else if item.Namespace != "" {
			return nil, helmrelease.ErrLiveRouting
		}
		// Same group/resource/name via a different API version is still one object.
		key := gv.Group + "\x00" + route.resource + "\x00" + ns + "\x00" + item.Name
		if seen[key] {
			return nil, helmrelease.ErrLiveRouting
		}
		seen[key] = true
		if route.namespaced {
			path = append(path, "namespaces", ns)
		}
		path = append(path, route.resource, item.Name)
		data, status, err := b.helmLiveGET(ctx, path, liveMetadataAccept, maxLiveObjectBytes, &remaining, true)
		if err != nil {
			return nil, err
		}
		row, err := projectLiveMetadata(data, status, item, route.resource, ns, p.Revision().Release, p.Namespace())
		clear(data)
		if err != nil {
			return nil, err
		}
		row.ObservedAt = time.Now()
		observed = append(observed, row)
	}
	after, err := b.HelmReleaseMetadata(ctx, p.Namespace())
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return helmrelease.FinishLiveObservation(p, session, before, after, observed, started, time.Now())
}

// Legacy unaggregated discovery deliberately omits the top-level apiVersion.
// groupVersion is still mandatory and must match the exact requested endpoint.
// Keep an explicitly supplied envelope version strict; null/empty/other versions
// and duplicate or unknown fields do not receive the legacy omission allowance.
func decodeLiveDiscovery(data []byte, expectedGroupVersion string) (metav1.APIResourceList, error) {
	var wire struct {
		APIVersion   json.RawMessage      `json:"apiVersion"`
		Kind         string               `json:"kind"`
		GroupVersion string               `json:"groupVersion"`
		Resources    []metav1.APIResource `json:"resources"`
	}
	var list metav1.APIResourceList
	issues, err := strictjson.UnmarshalStrict(data, &wire)
	if err != nil || len(issues) != 0 || wire.Kind != "APIResourceList" || wire.GroupVersion != expectedGroupVersion || wire.Resources == nil || len(wire.Resources) > 512 {
		return list, helmrelease.ErrLiveRouting
	}
	if len(wire.APIVersion) != 0 {
		var version string
		if json.Unmarshal(wire.APIVersion, &version) != nil || version != "v1" {
			return list, helmrelease.ErrLiveRouting
		}
		list.APIVersion = version
	}
	list.Kind, list.GroupVersion, list.APIResources = wire.Kind, wire.GroupVersion, wire.Resources
	return list, nil
}

// Do not reuse broad preferred-version discovery: it tolerates partial failure,
// selects preferred versions and requires list/watch. Here only exact GVK/get is
// relevant; plural names always come from the exact-version response.
func resolveLiveRoute(list metav1.APIResourceList, kind string) (liveRoute, error) {
	var route liveRoute
	matches := 0
	gv, err := schema.ParseGroupVersion(list.GroupVersion)
	if err != nil {
		return route, helmrelease.ErrLiveRouting
	}
	for _, r := range list.APIResources {
		if r.Kind != kind || strings.Contains(r.Name, "/") {
			continue
		}
		matches++
		if len(validation.IsDNS1123Label(r.Name)) != 0 || !hasVerb(r.Verbs, "get") || (r.Group != "" && r.Group != gv.Group) || (r.Version != "" && r.Version != gv.Version) {
			return route, helmrelease.ErrLiveRouting
		}
		route = liveRoute{resource: r.Name, namespaced: r.Namespaced}
	}
	if matches != 1 {
		return route, helmrelease.ErrLiveRouting
	}
	return route, nil
}

// Only typed Kubernetes NotFound for this exact resource/name counts as absence.
// A proxy's HTML 404, missing API, forbidden read and failed negotiation do not.
func projectLiveMetadata(data []byte, status int, item helmrelease.UninstallItem, resource, ns, release, releaseNS string) (helmrelease.LiveItem, error) {
	empty := helmrelease.LiveItem{}
	out := helmrelease.LiveItem{Stored: item, Resource: resource, Namespace: ns}
	if status == http.StatusNotFound {
		var s metav1.Status
		issues, err := strictjson.UnmarshalStrict(data, &s)
		gv, _ := schema.ParseGroupVersion(item.APIVersion)
		if err != nil || len(issues) != 0 || s.Kind != "Status" || s.APIVersion != "v1" || s.Status != "Failure" || s.Code != 404 || s.Reason != metav1.StatusReasonNotFound || s.Details == nil || s.Details.Name != item.Name || s.Details.Kind != resource || s.Details.Group != gv.Group {
			return empty, helmrelease.ErrLiveInvalid
		}
		out.State = "absent"
		return out, nil
	}
	var m metav1.PartialObjectMetadata
	issues, err := strictjson.UnmarshalStrict(data, &m)
	if err != nil || len(issues) != 0 {
		return empty, helmrelease.ErrLiveInvalid
	}
	if m.Kind != "PartialObjectMetadata" || m.APIVersion != "meta.k8s.io/v1" {
		return empty, helmrelease.ErrLiveUnsupported
	}
	if m.Name != item.Name || m.Namespace != ns || m.UID == "" || len(m.UID) > 128 || m.ResourceVersion == "" || len(m.ResourceVersion) > 256 {
		return empty, helmrelease.ErrLiveInvalid
	}
	out.State, out.UID, out.ResourceVersion = "present", string(m.UID), m.ResourceVersion
	out.Markers = "match"
	for _, v := range []struct {
		actual string
		exists bool
		want   string
	}{
		marker(m.Labels, "app.kubernetes.io/managed-by", "Helm"),
		marker(m.Annotations, "meta.helm.sh/release-name", release),
		marker(m.Annotations, "meta.helm.sh/release-namespace", releaseNS),
	} {
		if !v.exists && out.Markers != "mismatch" {
			out.Markers = "missing"
		}
		if v.exists && v.actual != v.want {
			out.Markers = "mismatch"
		}
	}
	out.Terminating = m.DeletionTimestamp != nil
	out.Finalizers, out.Owners = len(m.Finalizers) > 0, len(m.OwnerReferences) > 0
	// Presence rather than interpreting unknown policy/annotation values.
	_, out.Keep = m.Annotations["helm.sh/resource-policy"]
	for key := range m.Annotations {
		if strings.HasPrefix(key, "helm.sh/hook") {
			out.Hook = true
		}
	}
	return out, nil
}
func marker(m map[string]string, key, want string) struct {
	actual string
	exists bool
	want   string
} {
	v, ok := m[key]
	return struct {
		actual string
		exists bool
		want   string
	}{v, ok, want}
}

func (b *Backend) helmLiveGET(ctx context.Context, path []string, accept string, limit int, remaining *int, allowMissing bool) ([]byte, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if *remaining <= 0 {
		return nil, 0, helmrelease.ErrLiveBudget
	}
	if err := b.config.RateLimiter.Wait(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, helmrelease.ErrLiveUnavailable
	}
	u := b.writes.Get().AbsPath(path...).URL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, helmrelease.ErrLiveUnavailable
	}
	req.Header.Set("Accept", accept)
	resp, err := b.metricsHTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, helmrelease.ErrLiveUnavailable
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		if !allowMissing {
			return nil, 0, helmrelease.ErrLiveRouting
		}
		limit = min(limit, 4096)
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, 0, helmrelease.ErrLiveForbidden
	case http.StatusNotAcceptable:
		return nil, 0, helmrelease.ErrLiveUnsupported
	default:
		return nil, 0, helmrelease.ErrLiveUnavailable
	}
	limit = min(limit, *remaining)
	if resp.ContentLength > int64(limit) {
		return nil, 0, helmrelease.ErrLiveBudget
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil || ctx.Err() != nil || len(data) > limit {
		clear(data)
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		if len(data) > limit {
			return nil, 0, helmrelease.ErrLiveBudget
		}
		return nil, 0, helmrelease.ErrLiveUnavailable
	}
	*remaining -= len(data)
	return data, resp.StatusCode, nil
}
