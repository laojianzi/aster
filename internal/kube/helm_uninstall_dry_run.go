package kube

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	strictjson "sigs.k8s.io/json"
)

var (
	ErrHelmDryRunConsent  = errors.New("Helm server dry-run requires fresh, independent, single-use acknowledgement.")
	ErrHelmDryRunResponse = errors.New("Helm server dry-run returned an unsupported or changed response; no preflight qualified.")
	ErrHelmDryRunRejected = errors.New("Helm server dry-run was rejected or unavailable; no preflight qualified.")
)

// A copied handle still refers to the SAME consumption/revocation state. This
// acknowledgement authorizes bounded reads and dry-run requests only, not writes.
type HelmUninstallDryRunConsent struct{ state *helmDryRunConsentState }
type helmDryRunConsentState struct {
	preparation *HelmUninstallPreparation
	mu          sync.Mutex
	used        bool
	// Protected by mu: copies of one successful report may mint only one
	// independently acknowledged execution consent, including failed attempts.
	executionMinted bool
	cancel          context.CancelFunc
	revoked         atomic.Bool
}

// AcknowledgeHelmUninstallDryRun must be called only after a separate explicit
// acknowledgement of PATCH/DELETE dry-run permissions, sensitive storage reads,
// orphan policy and the limits of preflight. It does not extend preparation life.
func (b *Backend) AcknowledgeHelmUninstallDryRun(p *HelmUninstallPreparation, session, ns string, row helmrelease.Revision, acknowledged bool) (*HelmUninstallDryRunConsent, error) {
	if !acknowledged {
		return nil, ErrHelmDryRunConsent
	}
	if p == nil {
		return nil, helmrelease.ErrPreparationStale
	}
	// The caller can replace its public wrapper even though its fields are
	// private. Freeze that wrapper before qualification so a later replacement
	// cannot rebind the dry-run report or renew its original execution expiry.
	private := *p
	if !private.Matches(b, session, ns, row, time.Now()) {
		return nil, helmrelease.ErrPreparationStale
	}
	return &HelmUninstallDryRunConsent{state: &helmDryRunConsentState{preparation: &private}}, nil
}

// Discard revokes both in-flight work and any result from this acknowledgement.
// It is safe to call repeatedly or concurrently with PreflightHelmUninstall.
func (c *HelmUninstallDryRunConsent) Discard() {
	if c == nil || c.state == nil {
		return
	}
	s := c.state
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked.Store(true)
	s.used = true
	if s.cancel != nil {
		s.cancel()
	}
}

// HelmUninstallDryRun is inert sampled admission information. It contains no
// payload, response, status patch, backend/client credentials or write authority.
// A successful preflight does not qualify the future final-history update.
type HelmUninstallDryRun struct {
	state    *helmDryRunConsentState
	finished time.Time
}

func (r *HelmUninstallDryRun) PreparationID() string {
	if r == nil || r.state == nil {
		return ""
	}
	return r.state.preparation.ID()
}
func (r *HelmUninstallDryRun) Targets() []helmrelease.UninstallTarget {
	if r == nil || r.state == nil {
		return nil
	}
	return r.state.preparation.Targets()
}
func (r *HelmUninstallDryRun) ExpiresAt() time.Time {
	if r == nil || r.state == nil {
		return time.Time{}
	}
	return r.state.preparation.ExpiresAt()
}
func (r *HelmUninstallDryRun) Matches(b *Backend, session, ns string, row helmrelease.Revision, now time.Time) bool {
	return r != nil && r.state != nil && !r.state.revoked.Load() && !now.Before(r.finished) &&
		r.state.preparation.Matches(b, session, ns, row, now)
}

// PreflightHelmUninstall consumes acknowledgement BEFORE any network request,
// even when a subsequent check fails. No persistent mutation, retry, fallback,
// CLI, collection DELETE or execution capability exists on this path.
func (b *Backend) PreflightHelmUninstall(ctx context.Context, c *HelmUninstallDryRunConsent, session, ns string, row helmrelease.Revision) (*HelmUninstallDryRun, error) {
	if c == nil || c.state == nil {
		return nil, ErrHelmDryRunConsent
	}
	s := c.state
	s.mu.Lock()
	if s.used || s.revoked.Load() {
		s.mu.Unlock()
		return nil, ErrHelmDryRunConsent
	}
	s.used = true
	p := s.preparation
	if !p.Matches(b, session, ns, row, time.Now()) {
		s.mu.Unlock()
		return nil, helmrelease.ErrPreparationStale
	}
	ctx, timeoutCancel := context.WithTimeout(ctx, helmrelease.ReadTimeout)
	ctx, expiryCancel := context.WithDeadline(ctx, p.ExpiresAt())
	ctx, revokeCancel := context.WithCancel(ctx)
	s.cancel = revokeCancel
	s.mu.Unlock()
	defer func() {
		revokeCancel()
		expiryCancel()
		timeoutCancel()
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
	}()
	active := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.revoked.Load() || !p.Matches(b, session, ns, row, time.Now()) {
			return helmrelease.ErrPreparationStale
		}
		return nil
	}
	if err := active(); err != nil {
		return nil, err
	}
	// Reuse the complete profile and bracketing snapshots, but never use its
	// newly generated expiry to renew the originally acknowledged preparation.
	revalidate := func() error {
		fresh, err := b.PrepareHelmUninstall(ctx, ns, row, session, true)
		if err != nil {
			return err
		}
		if !slices.Equal(p.Targets(), fresh.Targets()) {
			return helmrelease.ErrPreparationStale
		}
		return active()
	}
	if err := revalidate(); err != nil {
		return nil, err
	}
	remaining := helmrelease.MaxSecretBytes + maxLiveReadBytes
	err := b.readHelmRevision(ctx, ns, row, true, func(ctx context.Context, data []byte) error {
		if err := active(); err != nil {
			return err
		}
		patch, err := helmrelease.BuildUninstallStatusPatch(ctx, data, ns, row, helmrelease.BeginUninstallStatus, time.Now())
		if err != nil {
			return err
		}
		defer clear(patch)
		response, err := b.helmDryRunRequest(ctx, http.MethodPatch, ns, "secrets", row.StorageName, patch, helmrelease.MaxSecretBytes, &remaining)
		if err != nil {
			return err
		}
		defer clear(response)
		if err := validateHelmDryRunStorage(ctx, data, patch, response); err != nil {
			return err
		}
		return active()
	})
	if err != nil {
		return nil, err
	}
	for _, target := range p.Targets() {
		if err := active(); err != nil {
			return nil, err
		}
		uid, rv := types.UID(target.UID), target.ResourceVersion
		policy := metav1.DeletePropagationOrphan
		options := metav1.DeleteOptions{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "DeleteOptions"},
			Preconditions:     &metav1.Preconditions{UID: &uid, ResourceVersion: &rv},
			PropagationPolicy: &policy, DryRun: []string{metav1.DryRunAll}}
		body, err := json.Marshal(options)
		if err != nil {
			return nil, ErrHelmDryRunRejected
		}
		response, err := b.helmDryRunRequest(ctx, http.MethodDelete, ns, "configmaps", target.Name, body, maxLiveObjectBytes, &remaining)
		clear(body)
		if err != nil {
			return nil, err
		}
		err = validateHelmDryRunDelete(response, target, row.Release)
		clear(response)
		if err != nil {
			return nil, err
		}
		if err := active(); err != nil {
			return nil, err
		}
	}
	// Sample non-persistence and all current restrictions, not just command
	// success. Server honesty, external webhook effects and future admission
	// cannot be proven by these reads. Any source/target change fails the batch.
	if err := revalidate(); err != nil {
		return nil, err
	}
	if err := active(); err != nil {
		return nil, err
	}
	return &HelmUninstallDryRun{state: s, finished: time.Now()}, nil
}

// The only request builder for this path always emits an explicit All, including
// for DELETE. GetBody is disabled to forbid automatic body replay. Existing
// identity/TLS/rate-limiter/redirect protections are reused without a new client.
func (b *Backend) helmDryRunRequest(ctx context.Context, method, ns, resource, name string, body []byte, limit int, remaining *int) ([]byte, error) {
	if method != http.MethodPatch && method != http.MethodDelete ||
		method == http.MethodPatch && resource != "secrets" || method == http.MethodDelete && resource != "configmaps" ||
		len(body) == 0 || len(body) > helmrelease.MaxSecretBytes {
		return nil, ErrHelmDryRunRejected
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit = min(limit, *remaining)
	if limit <= 0 {
		return nil, helmrelease.ErrLiveBudget
	}
	if err := b.config.RateLimiter.Wait(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrHelmDryRunRejected
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	u := b.writes.Get().AbsPath("/api", "v1", "namespaces", ns, resource, name).URL()
	q := u.Query()
	q.Set("dryRun", metav1.DryRunAll)
	u.RawQuery = q.Encode()
	owned := &helmDryRunBody{data: bytes.Clone(body)}
	defer owned.Close()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), owned)
	if err != nil {
		return nil, ErrHelmDryRunRejected
	}
	req.ContentLength = int64(len(body))
	req.GetBody = nil
	req.Header.Set("Accept", "application/json")
	if method == http.MethodDelete {
		req.Header.Set("Accept", liveMetadataAccept)
	}
	req.Header.Set("Accept-Encoding", "identity")
	contentType := "application/json"
	if method == http.MethodPatch {
		contentType = "application/json-patch+json"
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := b.metricsHTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrHelmDryRunRejected
	}
	defer resp.Body.Close()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Do not consume error bodies, even Kubernetes errors can echo Secret data.
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		return nil, helmrelease.ErrLiveForbidden
	}
	if resp.StatusCode != http.StatusOK {
		return nil, ErrHelmDryRunRejected
	}
	mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" || len(resp.Header.Values("Content-Type")) != 1 || len(resp.Header.Values("Warning")) != 0 ||
		resp.Header.Get("Content-Encoding") != "" && resp.Header.Get("Content-Encoding") != "identity" {
		return nil, ErrHelmDryRunResponse
	}
	if resp.ContentLength > int64(limit) {
		return nil, helmrelease.ErrLiveBudget
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil || ctx.Err() != nil || len(data) > limit {
		clear(data)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if len(data) > limit {
			return nil, helmrelease.ErrLiveBudget
		}
		return nil, ErrHelmDryRunResponse
	}
	*remaining -= len(data)
	return data, nil
}

// net/http may close an outgoing body asynchronously after Do returns. Own a
// separate bounded copy and synchronize Read/Close so wiping the codec output
// cannot race with an early server response or a canceled in-flight upload.
type helmDryRunBody struct {
	mu     sync.Mutex
	data   []byte
	offset int
}

func (b *helmDryRunBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.offset == len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.offset:])
	b.offset += n
	return n, nil
}
func (b *helmDryRunBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	clear(b.data)
	b.data = nil
	b.offset = 0
	return nil
}

func validateHelmDryRunStorage(ctx context.Context, original, patch, response []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var before, after corev1.Secret
	defer func() {
		for _, s := range []*corev1.Secret{&before, &after} {
			for _, v := range s.Data {
				clear(v)
			}
		}
	}()
	for _, pair := range []struct {
		data []byte
		out  *corev1.Secret
	}{{original, &before}, {response, &after}} {
		issues, err := strictjson.UnmarshalStrict(pair.data, pair.out)
		if err != nil || len(issues) != 0 {
			return ErrHelmDryRunResponse
		}
	}
	if before.Labels == nil || before.Data == nil {
		return ErrHelmDryRunResponse
	}
	var operations []struct{ Op, Path, Value string }
	if json.Unmarshal(patch, &operations) != nil || len(operations) != 8 {
		return ErrHelmDryRunResponse
	}
	// Only the three wire changes produced by the already qualified codec.
	for _, op := range operations {
		if op.Op == "test" {
			continue
		}
		switch op.Path {
		case "/metadata/labels/status":
			before.Labels["status"] = op.Value
		case "/metadata/labels/modifiedAt":
			before.Labels["modifiedAt"] = op.Value
		case "/data/release":
			encoded, err := base64.StdEncoding.DecodeString(op.Value)
			if err != nil {
				return ErrHelmDryRunResponse
			}
			clear(before.Data["release"])
			before.Data["release"] = encoded
		default:
			return ErrHelmDryRunResponse
		}
	}
	if after.APIVersion != "v1" || after.Kind != "Secret" || after.ResourceVersion == "" || len(after.ResourceVersion) > 256 {
		return ErrHelmDryRunResponse
	}
	// Dry-run generated RV/managedFields are not later execution preconditions.
	// All payload bytes and every other storage/metadata field must match.
	before.ResourceVersion, after.ResourceVersion = "", ""
	before.ManagedFields, after.ManagedFields = nil, nil
	if !reflect.DeepEqual(before, after) {
		return ErrHelmDryRunResponse
	}
	return ctx.Err()
}

func validateHelmDryRunDelete(data []byte, target helmrelease.UninstallTarget, release string) error {
	// Fixed orphan propagation can yield a simulated terminating object with
	// the GC orphan finalizer. Negotiate metadata ONLY; never fall back to the
	// ConfigMap data. This is admission information, not observed disappearance.
	var critical map[string]json.RawMessage
	if json.Unmarshal(data, &critical) != nil {
		return ErrHelmDryRunResponse
	}
	for _, key := range []string{"apiVersion", "kind", "status", "code", "reason", "details"} {
		if bytes.Equal(bytes.TrimSpace(critical[key]), []byte("null")) {
			return ErrHelmDryRunResponse
		}
	}
	var envelope struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return ErrHelmDryRunResponse
	}
	if envelope.Kind == "PartialObjectMetadata" {
		var m metav1.PartialObjectMetadata
		issues, err := strictjson.UnmarshalStrict(data, &m)
		if err != nil || len(issues) != 0 || m.APIVersion != "meta.k8s.io/v1" ||
			m.Name != target.Name || m.Namespace != target.Namespace || string(m.UID) != target.UID ||
			m.ResourceVersion == "" || len(m.ResourceVersion) > 256 || len(m.OwnerReferences) != 0 ||
			m.Labels["app.kubernetes.io/managed-by"] != "Helm" || m.Annotations["meta.helm.sh/release-name"] != release ||
			m.Annotations["meta.helm.sh/release-namespace"] != target.Namespace ||
			m.DeletionTimestamp == nil || m.DeletionTimestamp.IsZero() ||
			m.DeletionGracePeriodSeconds == nil || *m.DeletionGracePeriodSeconds != 0 ||
			!slices.Equal(m.Finalizers, []string{metav1.FinalizerOrphanDependents}) {
			return ErrHelmDryRunResponse
		}
		if _, exists := m.Annotations["helm.sh/resource-policy"]; exists {
			return ErrHelmDryRunResponse
		}
		for key := range m.Annotations {
			if strings.HasPrefix(key, "helm.sh/hook") {
				return ErrHelmDryRunResponse
			}
		}
		return nil
	}
	var s metav1.Status
	issues, err := strictjson.UnmarshalStrict(data, &s)
	if err != nil || len(issues) != 0 || s.APIVersion != "v1" || s.Kind != "Status" || s.Status != metav1.StatusSuccess ||
		s.Code != 0 && s.Code != 200 || s.Message != "" || s.Reason != "" || s.Details == nil ||
		s.Details.Name != target.Name || s.Details.Kind != "configmaps" || s.Details.Group != "" ||
		string(s.Details.UID) != target.UID || len(s.Details.Causes) != 0 || s.Details.RetryAfterSeconds != 0 ||
		!reflect.DeepEqual(s.ListMeta, metav1.ListMeta{}) {
		return ErrHelmDryRunResponse
	}
	return nil
}
