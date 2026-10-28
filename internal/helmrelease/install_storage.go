package helmrelease

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// installPendingPayload changes only the capture's dry-run description. Chart,
// supplied config, manifest and timestamps are the SINGLE captured original.
func installPendingPayload(ctx context.Context, c *InstallCandidate) ([]byte, error) {
	var root map[string]json.RawMessage
	if c == nil || c.id == "" || json.Unmarshal([]byte(c.payload), &root) != nil {
		return nil, ErrInstallCodec
	}
	info, err := replaceStatusMember(ctx, root["info"], "description", []byte(`"Initial install underway"`))
	if err != nil {
		return nil, err
	}
	defer clear(info)
	return replaceStatusMember(ctx, []byte(c.payload), "info", info)
}

// BuildInstallSecret returns SENSITIVE bytes for a new revision-1 Secret. This
// pure codec grants no authority and does not prove absence or perform dry-run.
// Callers must clear returned bytes and must never put them in UI/logs/exports.
func BuildInstallSecret(ctx context.Context, c *InstallCandidate, now time.Time) ([]byte, error) {
	if ctx == nil || !c.Valid(now) || now.Year() < 1970 || now.Year() > 9999 {
		return nil, ErrInstallCodec
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	payload, err := installPendingPayload(ctx, c)
	if err != nil {
		return nil, err
	}
	defer clear(payload)
	encoded, err := encodeStatusPayload(ctx, payload)
	if err != nil {
		return nil, err
	}
	defer clear(encoded)
	wire := struct {
		APIVersion string            `json:"apiVersion"`
		Kind       string            `json:"kind"`
		Metadata   metav1.ObjectMeta `json:"metadata"`
		Type       string            `json:"type"`
		Data       map[string][]byte `json:"data"`
	}{"v1", "Secret", metav1.ObjectMeta{Name: c.StorageName(), Namespace: c.namespace, Labels: map[string]string{
		"owner": "helm", "name": c.release, "version": "1", "status": "pending-install", "createdAt": strconv.FormatInt(now.Unix(), 10),
	}}, "helm.sh/release.v1", map[string][]byte{"release": encoded}}
	out, err := json.Marshal(wire)
	if err != nil || len(out) > MaxSecretBytes {
		clear(out)
		return nil, ErrInstallCodec
	}
	if err := ctx.Err(); err != nil {
		clear(out)
		return nil, err
	}
	return out, nil
}

// BuildInstallDeployedPatch is a PURE pending-install -> deployed codec, NOT an
// executor. Completion must be independently established by a future executor.
// It binds the complete original pending payload plus exact UID/RV/storage labels
// with atomic server-side tests; it never guesses an RV or uses SSA/adoption.
func BuildInstallDeployedPatch(ctx context.Context, c *InstallCandidate, data []byte, row Revision, now time.Time) ([]byte, error) {
	if ctx == nil || !c.Valid(now) || row.Release != c.Release() || row.StorageName != c.StorageName() || row.Number != 1 ||
		row.Status != "pending-install" || !safeMetadataID(row.UID, 128) || !safeMetadataID(row.ResourceVersion, 256) {
		return nil, ErrInstallCodec
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	expected, err := installPendingPayload(ctx, c)
	if err != nil {
		return nil, err
	}
	defer clear(expected)
	var result []byte
	_, err = inspectSecretPayload(ctx, data, c.namespace, row, func(_ Inspection, _ string, payload []byte) error {
		if !bytes.Equal(payload, expected) {
			return ErrInstallCodec
		}
		var storage struct {
			Metadata  metav1.ObjectMeta `json:"metadata"`
			Immutable *bool             `json:"immutable"`
			Data      map[string]string `json:"data"`
		}
		if json.Unmarshal(data, &storage) != nil || storage.Immutable != nil && *storage.Immutable ||
			len(storage.Metadata.Finalizers) != 0 || len(storage.Metadata.OwnerReferences) != 0 ||
			storage.Metadata.DeletionGracePeriodSeconds != nil || restrictedAnnotations(storage.Metadata.Annotations) {
			return ErrInstallCodec
		}
		encoded, err := encodeInstallDeployedPayload(ctx, payload)
		if err != nil {
			return err
		}
		defer clear(encoded)
		ops := []statusPatchOperation{
			{"test", "/apiVersion", "v1"}, {"test", "/kind", "Secret"}, {"test", "/type", "helm.sh/release.v1"},
			{"test", "/metadata/name", row.StorageName}, {"test", "/metadata/namespace", c.namespace},
			{"test", "/metadata/uid", row.UID}, {"test", "/metadata/resourceVersion", row.ResourceVersion},
			{"test", "/metadata/labels/owner", "helm"}, {"test", "/metadata/labels/name", c.release},
			{"test", "/metadata/labels/version", "1"}, {"test", "/metadata/labels/status", "pending-install"},
			{"test", "/data/release", storage.Data["release"]},
			{"replace", "/metadata/labels/status", "deployed"},
			{"add", "/metadata/labels/modifiedAt", strconv.FormatInt(now.Unix(), 10)},
			{"replace", "/data/release", base64.StdEncoding.EncodeToString(encoded)},
		}
		result, err = json.Marshal(ops)
		if err != nil || len(result) > MaxSecretBytes {
			return ErrInstallCodec
		}
		return ctx.Err()
	})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		clear(result)
		return nil, err
	}
	return result, nil
}

// InstallExecutionSizeBounds returns only lower bounds for the pending Secret's
// release value and the final patch's two release values, in JSON wire bytes.
// These inert local facts can reject an impossible execution before any write.
// They are not a fit guarantee: server metadata, other reads, JSON framing and
// timing cost more. No UID/RV is invented, and no payload or authority escapes.
func InstallExecutionSizeBounds(ctx context.Context, c *InstallCandidate, p *InstallCreatePlan) (pendingReleaseBytes, deployedPatchDataBytes int, err error) {
	if ctx == nil || c == nil || c.id == "" || p == nil || p.candidateID != c.id || p.namespace != c.namespace ||
		p.Len() == 0 || p.requests[0].resource != "secrets" || p.requests[0].name != c.StorageName() {
		return 0, 0, ErrInstallCodec
	}
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	body := p.SensitiveBody(0)
	defer clear(body)
	var pending struct {
		Data map[string][]byte `json:"data"`
	}
	defer func() {
		for _, data := range pending.Data {
			clear(data)
		}
	}()
	if json.Unmarshal(body, &pending) != nil || len(pending.Data) != 1 || len(pending.Data["release"]) == 0 {
		return 0, 0, ErrInstallCodec
	}
	payload, err := installPendingPayload(ctx, c)
	if err != nil {
		return 0, 0, err
	}
	defer clear(payload)
	encoded, err := encodeInstallDeployedPayload(ctx, payload)
	if err != nil {
		return 0, 0, err
	}
	defer clear(encoded)
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	// The exact byte values require at least their canonical Base64 lengths.
	// Alternative JSON escaping can add bytes, never remove required Base64.
	pendingReleaseBytes = base64.StdEncoding.EncodedLen(len(pending.Data["release"]))
	return pendingReleaseBytes, pendingReleaseBytes + base64.StdEncoding.EncodedLen(len(encoded)), nil
}

// Both the actual conditional codec and pre-write size proof use this exact
// payload transformation and bounded encoder. Neither reads mutable sources.
func encodeInstallDeployedPayload(ctx context.Context, payload []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(payload, &root) != nil {
		return nil, ErrInstallCodec
	}
	info, err := replaceStatusMember(ctx, root["info"], "status", []byte(`"deployed"`))
	if err != nil {
		return nil, err
	}
	defer func() { clear(info) }()
	next, err := replaceStatusMember(ctx, info, "description", []byte(`"Install complete"`))
	clear(info)
	if err != nil {
		return nil, err
	}
	info = next
	updated, err := replaceStatusMember(ctx, payload, "info", info)
	if err != nil {
		return nil, err
	}
	defer clear(updated)
	return encodeStatusPayload(ctx, updated)
}
