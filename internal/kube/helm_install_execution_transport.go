package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	strictjson "sigs.k8s.io/json"
)

// Only exact create POST and the final codec-produced Secret PATCH exist. GETs
// never retry a write. No GetBody, idempotency header, redirect, force or SSA.
func (r *installExecutionRun) mutate(index int, body []byte) ([]byte, bool, error) {
	if index < 0 || index >= len(r.out.effects) || len(body) == 0 || len(body) > helmrelease.MaxSecretBytes || r.remaining <= 0 {
		return nil, false, ErrHelmInstallExecutionResponse
	}
	if err := r.active(); err != nil {
		return nil, false, err
	}
	if err := r.b.config.RateLimiter.Wait(r.ctx); err != nil {
		return nil, false, r.readError(err)
	}
	effect := &r.out.effects[index]
	resource, name := r.state.plan.Identity(index)
	method, media, status := http.MethodPost, "application/json", http.StatusCreated
	path := []string{"/api", "v1", "namespaces", r.state.candidate.Namespace(), resource}
	if index == len(r.out.effects)-1 {
		method, media, status = http.MethodPatch, "application/json-patch+json", http.StatusOK
		resource, name = "secrets", r.state.candidate.StorageName()
		path = []string{"/api", "v1", "namespaces", r.state.candidate.Namespace(), resource, name}
	}
	if name != effect.Name || effect.Namespace != r.state.candidate.Namespace() {
		return nil, false, ErrHelmExecutionJournal
	}
	u := r.b.writes.Get().AbsPath(path...).URL()
	if u.RawQuery != "" {
		return nil, false, ErrHelmInstallExecutionResponse
	}
	q := u.Query()
	q.Set("fieldValidation", "Strict")
	q.Set("fieldManager", helmrelease.InstallFieldManager)
	u.RawQuery = q.Encode()
	owned := &helmDryRunBody{data: bytes.Clone(body)}
	defer owned.Close()
	req, err := http.NewRequestWithContext(r.ctx, method, u.String(), owned)
	if err != nil {
		return nil, false, ErrHelmInstallExecutionResponse
	}
	req.GetBody = nil
	req.ContentLength = int64(len(body))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", media)
	req.Header.Set("Accept-Encoding", "identity")
	digest := installRequestDigest(body)
	if index == len(r.out.effects)-1 {
		effect.ContentDigest = digest
		effect.UID = r.row.UID
		effect.ResourceVersion = r.row.ResourceVersion
	}
	if err = r.journal.Intent(index, digest); err != nil {
		return nil, false, errors.Join(ErrHelmExecutionJournal, err)
	}
	effect.Outcome = helmjournal.Unknown
	// Lock-based authority check is the dispatch linearization point. A later
	// discard cancels the in-flight call, but cannot retract a possible effect.
	if err = r.dispatch(); err != nil {
		return nil, false, err
	}
	resp, err := r.b.metricsHTTP.Do(req)
	if err != nil {
		return nil, false, r.unknownError()
	}
	if !validHelmExecutionHeaders(resp) || resp.Uncompressed {
		resp.Body.Close()
		return nil, false, r.unknownError()
	}
	limit := helmrelease.MaxSecretBytes
	if resp.StatusCode != status {
		limit = 4096
	}
	data, err := r.readBody(resp, limit)
	if err != nil {
		return nil, false, r.unknownError()
	}
	if resp.StatusCode != status {
		rejected := exactInstallRejection(data, resp.StatusCode, resource, name, method == http.MethodPost)
		clear(data)
		if rejected {
			return nil, true, ErrHelmInstallExecutionRejected
		}
		return nil, false, r.unknownError()
	}
	// Preserve a complete valid acknowledgement even if cancellation arrived
	// after its last byte. No further GET or mutation may follow canceled authority.
	return data, false, nil
}
func (r *installExecutionRun) unknownError() error {
	if err := r.ctx.Err(); err != nil {
		return errors.Join(ErrHelmInstallExecutionUnknown, err)
	}
	return ErrHelmInstallExecutionUnknown
}
func (r *installExecutionRun) readError(err error) error {
	if e := r.ctx.Err(); e != nil {
		return e
	}
	// Never return untrusted URLs, body text or credential diagnostics.
	if errors.Is(err, helmrelease.ErrInstallConflict) {
		return ErrHelmInstallExecutionChanged
	}
	return ErrHelmInstallExecutionResponse
}
func (r *installExecutionRun) readBody(resp *http.Response, limit int) ([]byte, error) {
	limit = min(limit, r.remaining)
	if limit <= 0 || resp.ContentLength > int64(limit) {
		resp.Body.Close()
		return nil, ErrHelmInstallExecutionResponse
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	closeErr := resp.Body.Close()
	if err != nil || closeErr != nil || len(data) > limit || resp.ContentLength >= 0 && int64(len(data)) != resp.ContentLength || len(resp.Trailer.Values("Warning")) != 0 {
		clear(data)
		return nil, ErrHelmInstallExecutionResponse
	}
	r.remaining -= len(data)
	return data, nil
}
func (r *installExecutionRun) readObject(resource, name string) ([]byte, error) {
	if err := r.active(); err != nil {
		return nil, err
	}
	if resource != "secrets" && resource != "configmaps" {
		return nil, ErrHelmInstallExecutionResponse
	}
	if err := r.b.config.RateLimiter.Wait(r.ctx); err != nil {
		return nil, r.readError(err)
	}
	if err := r.active(); err != nil {
		return nil, err
	}
	u := r.b.writes.Get().AbsPath("/api", "v1", "namespaces", r.state.candidate.Namespace(), resource, name).URL()
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, ErrHelmInstallExecutionResponse
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := r.b.metricsHTTP.Do(req)
	if err != nil {
		return nil, r.readError(err)
	}
	if resp.StatusCode != http.StatusOK || !validHelmExecutionHeaders(resp) || resp.Uncompressed {
		resp.Body.Close()
		return nil, ErrHelmInstallExecutionChanged
	}
	data, err := r.readBody(resp, helmrelease.MaxSecretBytes)
	if err != nil {
		return nil, err
	}
	if err = r.active(); err != nil {
		clear(data)
		return nil, err
	}
	return data, nil
}
func exactInstallRejection(data []byte, code int, resource, name string, create bool) bool {
	if !nonNullHelmExecutionEnvelope(data) {
		return false
	}
	var s metav1.Status
	issues, err := strictjson.UnmarshalStrict(data, &s)
	if err != nil || len(issues) != 0 {
		return false
	}
	// A create collision is AlreadyExists, not the update Conflict reason.
	if create && code == http.StatusConflict && s.Reason == metav1.StatusReasonAlreadyExists {
		if s.Details == nil || s.Details.UID != "" {
			return false
		}
		s.Reason = metav1.StatusReasonConflict
		normalized, err := json.Marshal(s)
		if err != nil {
			return false
		}
		defer clear(normalized)
		return exactHelmExecutionRejection(normalized, code, resource, name, "")
	}
	return exactHelmExecutionRejection(data, code, resource, name, "")
}
func validateInstallPersistentCreate(plan *helmrelease.InstallCreatePlan, index int, data []byte) (string, string, error) {
	if err := helmrelease.ValidateInstallCreateResponse(context.Background(), plan, index, data); err != nil {
		return "", "", ErrHelmInstallExecutionResponse
	}
	var meta struct {
		Metadata metav1.ObjectMeta `json:"metadata"`
	}
	if json.Unmarshal(data, &meta) != nil || !safeHelmExecutionID(string(meta.Metadata.UID), 128) || !safeHelmExecutionID(meta.Metadata.ResourceVersion, 256) {
		return "", "", ErrHelmInstallExecutionResponse
	}
	return string(meta.Metadata.UID), meta.Metadata.ResourceVersion, nil
}
func sameInstallObject(resource string, a, b []byte) bool {
	if resource == "secrets" {
		return sameHelmExecutionStorage(a, b)
	}
	var x, y corev1.ConfigMap
	defer func() {
		for _, m := range []*corev1.ConfigMap{&x, &y} {
			for _, v := range m.BinaryData {
				clear(v)
			}
		}
	}()
	for _, p := range []struct {
		raw []byte
		v   *corev1.ConfigMap
	}{{a, &x}, {b, &y}} {
		issues, err := strictjson.UnmarshalStrict(p.raw, p.v)
		if err != nil || len(issues) != 0 {
			return false
		}
	}
	return resource == "configmaps" && reflect.DeepEqual(x, y)
}

// Compare the complete actual response against exactly the local codec's JSON
// patch applied to the observed pending Secret. Do not reuse the uninstall-only
// eight-operation validator; install has its own stricter test envelope.
func validateInstallDeployed(original, patch, response []byte, row helmrelease.Revision) (helmrelease.Revision, error) {
	p, err := jsonpatch.DecodePatch(patch)
	if err != nil {
		return helmrelease.Revision{}, ErrHelmInstallExecutionResponse
	}
	expected, err := p.Apply(original)
	if err != nil {
		return helmrelease.Revision{}, ErrHelmInstallExecutionResponse
	}
	defer clear(expected)
	var want, got corev1.Secret
	defer clearHelmExecutionSecret(&want)
	defer clearHelmExecutionSecret(&got)
	for _, v := range []struct {
		raw []byte
		out *corev1.Secret
	}{{expected, &want}, {response, &got}} {
		issues, err := strictjson.UnmarshalStrict(v.raw, v.out)
		if err != nil || len(issues) != 0 {
			return helmrelease.Revision{}, ErrHelmInstallExecutionResponse
		}
	}
	if !safeHelmExecutionID(got.ResourceVersion, 256) || got.ResourceVersion == row.ResourceVersion || string(got.UID) != row.UID || got.Labels["status"] != "deployed" || len(got.ManagedFields) > 1 {
		return helmrelease.Revision{}, ErrHelmInstallExecutionResponse
	}
	for _, f := range got.ManagedFields {
		if f.Manager != helmrelease.InstallFieldManager || f.Operation != metav1.ManagedFieldsOperationUpdate || f.APIVersion != "v1" || f.Subresource != "" || f.FieldsType != "FieldsV1" || f.FieldsV1 == nil {
			return helmrelease.Revision{}, ErrHelmInstallExecutionResponse
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(f.FieldsV1.Raw, &fields) != nil || fields == nil {
			return helmrelease.Revision{}, ErrHelmInstallExecutionResponse
		}
	}
	next := row
	next.ResourceVersion = got.ResourceVersion
	next.Status = "deployed"
	want.ResourceVersion, got.ResourceVersion = "", ""
	want.ManagedFields, got.ManagedFields = nil, nil
	if !reflect.DeepEqual(want, got) {
		return helmrelease.Revision{}, ErrHelmInstallExecutionResponse
	}
	return next, nil
}
