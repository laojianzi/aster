package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/laojianzi/aster/internal/helmrelease"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	strictjson "sigs.k8s.io/json"
)

func validHelmExecutionHeaders(resp *http.Response) bool {
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	return err == nil && media == "application/json" && len(resp.Header.Values("Content-Type")) == 1 &&
		len(resp.Header.Values("Warning")) == 0 && len(resp.Header.Values("Content-Encoding")) <= 1 &&
		(resp.Header.Get("Content-Encoding") == "" || resp.Header.Get("Content-Encoding") == "identity")
}

// Persistent requests have no replayable GetBody, retry loop, dryRun parameter,
// redirect, collection route, arbitrary policy or privilege-changing transport.
// An error after Do begins is Unknown unless a bounded, exact Kubernetes failure
// envelope proves a definitive rejection. Bare status codes/proxy bodies do not.
func (r *helmExecutionRun) mutate(method, resource, name string, body []byte, limit int) ([]byte, bool, bool, error) {
	if method != http.MethodPatch && method != http.MethodDelete ||
		method == http.MethodPatch && resource != "secrets" || method == http.MethodDelete && resource != "configmaps" ||
		len(body) == 0 || len(body) > helmrelease.MaxSecretBytes || r.remaining <= 0 {
		return nil, false, false, ErrHelmExecutionResponse
	}
	if err := r.active(); err != nil {
		return nil, false, false, err
	}
	if err := r.b.config.RateLimiter.Wait(r.ctx); err != nil {
		if err := r.active(); err != nil {
			return nil, false, false, err
		}
		return nil, false, false, ErrHelmExecutionResponse
	}
	u := r.b.writes.Get().AbsPath("/api", "v1", "namespaces", r.ns, resource, name).URL()
	if u.RawQuery != "" {
		return nil, false, false, ErrHelmExecutionResponse
	}
	owned := &helmDryRunBody{data: bytes.Clone(body)}
	defer owned.Close()
	req, err := http.NewRequestWithContext(r.ctx, method, u.String(), owned)
	if err != nil {
		return nil, false, false, ErrHelmExecutionResponse
	}
	req.ContentLength = int64(len(body))
	req.GetBody = nil
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json-patch+json")
	if method == http.MethodDelete {
		req.Header.Set("Accept", liveMetadataAccept)
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept-Encoding", "identity")
	if err := r.dispatch(); err != nil {
		return nil, false, false, err
	}
	response, err := r.b.metricsHTTP.Do(req)
	if err != nil {
		if err := r.active(); err != nil {
			return nil, true, false, err
		}
		return nil, true, false, ErrHelmExecutionResponse
	}
	defer response.Body.Close()
	if err := r.active(); err != nil {
		return nil, true, false, err
	}
	if !validHelmExecutionHeaders(response) {
		return nil, true, false, ErrHelmExecutionResponse
	}
	if response.StatusCode != http.StatusOK {
		// A fixed small failure envelope is interpreted privately and discarded.
		// In particular it is never included in an error or stored in the ledger.
		if response.StatusCode < 400 || response.StatusCode >= 500 {
			return nil, true, false, ErrHelmExecutionResponse
		}
		data, err := r.responseBody(r.ctx, response, 4096)
		if err != nil {
			return nil, true, false, err
		}
		defer clear(data)
		uid := r.row.UID
		if resource == "configmaps" {
			uid = ""
			for _, target := range r.out.targets {
				if target.Name == name {
					uid = target.UID
					break
				}
			}
		}
		if !exactHelmExecutionRejection(data, response.StatusCode, resource, name, uid) {
			return nil, true, false, ErrHelmExecutionResponse
		}
		if err := r.active(); err != nil {
			return nil, true, false, err
		}
		return nil, true, true, ErrHelmExecutionRejected
	}
	data, err := r.responseBody(r.ctx, response, limit)
	if err != nil {
		return nil, true, false, err
	}
	if err := r.active(); err != nil {
		clear(data)
		return nil, true, false, err
	}
	return data, true, false, nil
}

func (r *helmExecutionRun) responseBody(ctx context.Context, response *http.Response, limit int) ([]byte, error) {
	limit = min(limit, r.remaining)
	if limit <= 0 || response.ContentLength > int64(limit) {
		return nil, ErrHelmExecutionResponse
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if err != nil || ctx.Err() != nil || len(data) > limit {
		clear(data)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrHelmExecutionResponse
	}
	r.remaining -= len(data)
	return data, nil
}

// Additional exact reads share the same current authenticated client and rate
// limiter. Full-object fallback is never available for ConfigMap observations.
func (r *helmExecutionRun) read(ctx context.Context, resource, name string, limit int, allowMissing bool) ([]byte, int, error) {
	if resource != "secrets" && resource != "configmaps" || r.remaining <= 0 {
		return nil, 0, ErrHelmExecutionResponse
	}
	if err := r.active(); err != nil {
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if err := r.b.config.RateLimiter.Wait(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, ErrHelmExecutionResponse
	}
	if err := r.active(); err != nil {
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	u := r.b.writes.Get().AbsPath("/api", "v1", "namespaces", r.ns, resource, name).URL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, ErrHelmExecutionResponse
	}
	req.Header.Set("Accept", "application/json")
	if resource == "configmaps" {
		req.Header.Set("Accept", liveMetadataAccept)
	}
	req.Header.Set("Accept-Encoding", "identity")
	response, err := r.b.metricsHTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, ErrHelmExecutionResponse
	}
	defer response.Body.Close()
	if err := r.active(); err != nil {
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if response.StatusCode != http.StatusOK && (!allowMissing || response.StatusCode != http.StatusNotFound) {
		return nil, 0, ErrHelmExecutionResponse
	}
	if !validHelmExecutionHeaders(response) {
		return nil, 0, ErrHelmExecutionResponse
	}
	if response.StatusCode == http.StatusNotFound {
		limit = min(limit, 4096)
	}
	data, err := r.responseBody(ctx, response, limit)
	if err != nil {
		return nil, 0, err
	}
	if err := r.active(); err != nil {
		clear(data)
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		clear(data)
		return nil, 0, err
	}
	return data, response.StatusCode, nil
}

func exactHelmExecutionRejection(data []byte, code int, resource, name, uid string) bool {
	var s metav1.Status
	issues, err := strictjson.UnmarshalStrict(data, &s)
	if err != nil || len(issues) != 0 || !nonNullHelmExecutionEnvelope(data) ||
		s.APIVersion != "v1" || s.Kind != "Status" || s.Status != metav1.StatusFailure || int(s.Code) != code ||
		s.Details == nil || s.Details.Name != name || s.Details.Group != "" ||
		(s.Details.UID != "" && string(s.Details.UID) != uid) || !reflect.DeepEqual(s.ListMeta, metav1.ListMeta{}) {
		return false
	}
	if s.Details.Kind != resource {
		// Kubernetes BeforeDelete reports a UID/RV precondition conflict using
		// the object's exact GVK kind as StatusDetails.Kind. Admit only that
		// core ConfigMap/409 shape for this restricted DELETE route. Other
		// resources, reasons and anonymous patch failures stay unqualified.
		if code != http.StatusConflict || resource != "configmaps" || s.Details.Kind != "ConfigMap" {
			return false
		}
	}
	want := map[int]metav1.StatusReason{
		400: metav1.StatusReasonBadRequest, 401: metav1.StatusReasonUnauthorized, 403: metav1.StatusReasonForbidden,
		404: metav1.StatusReasonNotFound, 405: metav1.StatusReasonMethodNotAllowed, 409: metav1.StatusReasonConflict,
		415: metav1.StatusReasonUnsupportedMediaType, 422: metav1.StatusReasonInvalid, 429: metav1.StatusReasonTooManyRequests,
	}
	if reason, exists := want[code]; !exists || s.Reason != reason {
		return false
	}
	if len(s.Details.Causes) > 8 || (code != 422 && len(s.Details.Causes) != 0) {
		return false
	}
	for _, cause := range s.Details.Causes {
		switch cause.Type {
		case metav1.CauseTypeFieldValueNotFound, metav1.CauseTypeFieldValueRequired, metav1.CauseTypeFieldValueDuplicate,
			metav1.CauseTypeFieldValueInvalid, metav1.CauseTypeFieldValueNotSupported:
		default:
			return false
		}
	}
	return s.Details.RetryAfterSeconds >= 0 && (code == 429 || s.Details.RetryAfterSeconds == 0)
}

func nonNullHelmExecutionEnvelope(data []byte) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return false
	}
	for _, key := range []string{"apiVersion", "kind", "metadata", "status", "code", "reason", "details"} {
		if bytes.Equal(bytes.TrimSpace(fields[key]), []byte("null")) {
			return false
		}
	}
	return true
}

func safeHelmExecutionID(s string, limit int) bool {
	if s == "" || len(s) > limit {
		return false
	}
	for _, ch := range s {
		if ch < 33 || ch > 126 {
			return false
		}
	}
	return true
}

func clearHelmExecutionSecret(s *corev1.Secret) {
	for _, value := range s.Data {
		clear(value)
	}
}

func sameHelmExecutionStorage(a, b []byte) bool {
	var left, right corev1.Secret
	defer clearHelmExecutionSecret(&left)
	defer clearHelmExecutionSecret(&right)
	for _, pair := range []struct {
		data []byte
		out  *corev1.Secret
	}{{a, &left}, {b, &right}} {
		issues, err := strictjson.UnmarshalStrict(pair.data, pair.out)
		if err != nil || len(issues) != 0 {
			return false
		}
	}
	// Unlike dry-run/first-ack comparison, subsequent exact GETs must keep even
	// RV and managedFields identical. Raw release data is compared byte-for-byte.
	return reflect.DeepEqual(left, right)
}

func validateHelmExecutionStorage(ctx context.Context, original, patch, response []byte, ns string, row helmrelease.Revision, nextStatus string) (helmrelease.Revision, error) {
	if err := validateHelmDryRunStorage(ctx, original, patch, response); err != nil {
		return helmrelease.Revision{}, err
	}
	var s corev1.Secret
	defer clearHelmExecutionSecret(&s)
	issues, err := strictjson.UnmarshalStrict(response, &s)
	if err != nil || len(issues) != 0 || !safeHelmExecutionID(s.ResourceVersion, 256) || s.ResourceVersion == row.ResourceVersion ||
		s.Labels["status"] != nextStatus {
		return helmrelease.Revision{}, ErrHelmExecutionResponse
	}
	next := row
	next.Status, next.ResourceVersion = nextStatus, s.ResourceVersion
	if _, err := helmrelease.InspectSecret(ctx, response, ns, next); err != nil {
		return helmrelease.Revision{}, ErrHelmExecutionResponse
	}
	return next, ctx.Err()
}

func validateHelmExecutionDelete(data []byte, target helmrelease.UninstallTarget, release string) error {
	if !nonNullHelmExecutionEnvelope(data) {
		return ErrHelmExecutionResponse
	}
	if err := validateHelmDryRunDelete(data, target, release); err != nil {
		return ErrHelmExecutionResponse
	}
	var m metav1.PartialObjectMetadata
	if json.Unmarshal(data, &m) != nil {
		return ErrHelmExecutionResponse
	}
	if m.Kind == "PartialObjectMetadata" && (!safeHelmExecutionID(m.ResourceVersion, 256) || m.ResourceVersion == target.ResourceVersion) {
		return ErrHelmExecutionResponse
	}
	return nil
}

// Exact NotFound is a separate observation, not inferred from DELETE admission.
// Success metadata establishes only that the reviewed UID is still visible.
func inspectHelmExecutionAbsence(data []byte, status int, target helmrelease.UninstallTarget, release string) (bool, error) {
	if !nonNullHelmExecutionEnvelope(data) {
		return false, ErrHelmExecutionResponse
	}
	if status == http.StatusNotFound {
		var s metav1.Status
		issues, err := strictjson.UnmarshalStrict(data, &s)
		if err != nil || len(issues) != 0 || s.APIVersion != "v1" || s.Kind != "Status" || s.Status != metav1.StatusFailure ||
			s.Code != 404 || s.Reason != metav1.StatusReasonNotFound || s.Details == nil ||
			s.Details.Name != target.Name || s.Details.Kind != "configmaps" || s.Details.Group != "" || s.Details.UID != "" ||
			len(s.Details.Causes) != 0 || s.Details.RetryAfterSeconds != 0 || !reflect.DeepEqual(s.ListMeta, metav1.ListMeta{}) {
			return false, ErrHelmExecutionResponse
		}
		return true, nil
	}
	if status != http.StatusOK {
		return false, ErrHelmExecutionResponse
	}
	var m metav1.PartialObjectMetadata
	issues, err := strictjson.UnmarshalStrict(data, &m)
	if err != nil || len(issues) != 0 || m.APIVersion != "meta.k8s.io/v1" || m.Kind != "PartialObjectMetadata" ||
		!safeHelmExecutionID(m.ResourceVersion, 256) {
		return false, ErrHelmExecutionResponse
	}
	if m.Name != target.Name || m.Namespace != target.Namespace || string(m.UID) != target.UID || len(m.OwnerReferences) != 0 ||
		m.Labels["app.kubernetes.io/managed-by"] != "Helm" || m.Annotations["meta.helm.sh/release-name"] != release ||
		m.Annotations["meta.helm.sh/release-namespace"] != target.Namespace {
		return false, ErrHelmExecutionChanged
	}
	if len(m.Finalizers) != 0 && !slices.Equal(m.Finalizers, []string{metav1.FinalizerOrphanDependents}) {
		return false, ErrHelmExecutionChanged
	}
	if m.DeletionTimestamp != nil && (m.DeletionTimestamp.IsZero() || m.DeletionGracePeriodSeconds == nil || *m.DeletionGracePeriodSeconds != 0) {
		return false, ErrHelmExecutionChanged
	}
	if m.DeletionTimestamp == nil && (m.DeletionGracePeriodSeconds != nil || len(m.Finalizers) != 0) {
		return false, ErrHelmExecutionChanged
	}
	if _, exists := m.Annotations["helm.sh/resource-policy"]; exists {
		return false, ErrHelmExecutionChanged
	}
	for key := range m.Annotations {
		if strings.HasPrefix(key, "helm.sh/hook") {
			return false, ErrHelmExecutionChanged
		}
	}
	return false, nil
}
