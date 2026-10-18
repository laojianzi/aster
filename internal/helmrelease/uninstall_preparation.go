package helmrelease

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
	strictjson "sigs.k8s.io/json"
)

const PreparationLifetime = 30 * time.Second

var (
	ErrPreparationConsent = errors.New("Restricted Helm preparation requires separate full-content read acknowledgement.")
	ErrPreparationProfile = errors.New("This release is outside the restricted retained-history ConfigMap profile; no preparation was produced.")
	ErrPreparationStale   = errors.New("Restricted Helm preparation changed or expired; perform a new explicit preparation.")
)

// UninstallProfile is a validated stored-content projection, NOT eligibility of
// the current live release or a write capability. No payload/digest is exposed.
// Its preview exists only to reuse the bounded exact-version metadata reader.
type UninstallProfile struct{ preview *UninstallPreview }

func (p *UninstallProfile) Preview() *UninstallPreview {
	if p == nil {
		return nil
	}
	return p.preview
}

// UninstallTarget is a current sampled identity, not the original Helm-installed
// identity. Source order is deterministic, not Helm's general deletion order.
type UninstallTarget struct {
	APIVersion, Kind, Resource, Namespace, Name string
	UID, ResourceVersion                        string
}

// UninstallPreparation is inert and immutable. It never retains release bytes,
// a status patch, a client or operation.Prepared. Matching does NOT authorize a
// write or imply a transaction lock, authentic ownership or no dependents.
type UninstallPreparation struct {
	profile  *UninstallProfile
	targets  []UninstallTarget
	finished time.Time
}

func (p *UninstallPreparation) ID() string {
	if p == nil || p.profile == nil {
		return ""
	}
	return p.profile.preview.ID()
}
func (p *UninstallPreparation) Namespace() string {
	if p == nil || p.profile == nil {
		return ""
	}
	return p.profile.preview.Namespace()
}
func (p *UninstallPreparation) Revision() Revision {
	if p == nil || p.profile == nil {
		return Revision{}
	}
	return p.profile.preview.Revision()
}
func (p *UninstallPreparation) Summary() Inspection {
	if p == nil || p.profile == nil {
		return Inspection{}
	}
	return p.profile.preview.Summary()
}
func (p *UninstallPreparation) ExpiresAt() time.Time {
	if p == nil || p.profile == nil {
		return time.Time{}
	}
	return p.profile.preview.ExpiresAt()
}
func (p *UninstallPreparation) Targets() []UninstallTarget {
	if p == nil {
		return nil
	}
	return append([]UninstallTarget(nil), p.targets...)
}
func (p *UninstallPreparation) Matches(session, ns string, row Revision, now time.Time) bool {
	return p != nil && p.profile != nil && !now.Before(p.finished) &&
		p.profile.preview.Matches(session, ns, row, now)
}

// InspectUninstallProfile inspects the COMPLETE stored wire, rather than treating
// absence of preview warnings as eligibility. Callers must obtain a new content
// read acknowledgement and supply freshly read exact Secret bytes. This pure
// function performs no I/O and does not prove sole revision or live identity.
func InspectUninstallProfile(ctx context.Context, data []byte, ns string, row Revision, session string, started time.Time) (*UninstallProfile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !ValidPreviewSession(session) || started.IsZero() || started.Year() < 1970 || started.Year() > 9999 {
		return nil, ErrPreviewSession
	}
	if !ValidSelection(ns, row) || row.Status != "deployed" || !safeMetadataID(row.UID, 128) || !safeMetadataID(row.ResourceVersion, 256) {
		return nil, ErrPreparationProfile
	}
	var items []UninstallItem
	summary, err := inspectSecretPayload(ctx, data, ns, row, func(_ Inspection, manifest string, payload []byte) error {
		var s struct {
			Metadata  metav1.ObjectMeta `json:"metadata"`
			Immutable *bool             `json:"immutable"`
		}
		if json.Unmarshal(data, &s) != nil || s.Immutable != nil && *s.Immutable || len(s.Metadata.Finalizers) != 0 || len(s.Metadata.OwnerReferences) != 0 || s.Metadata.DeletionGracePeriodSeconds != nil || restrictedAnnotations(s.Metadata.Annotations) {
			return ErrPreparationProfile
		}
		root, ok := profileObject(payload, "name", "namespace", "version", "info", "chart", "manifest", "config", "hooks", "apply_method")
		if !ok || !emptyProfileArray(root["hooks"]) {
			return ErrPreparationProfile
		}
		if raw, exists := root["apply_method"]; exists {
			var method string
			if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &method) != nil || method != "" && method != "csa" && method != "ssa" {
				return ErrPreparationProfile
			}
		}
		info, ok := canonicalObject(root["info"], "status", "description", "deleted")
		if !ok {
			return ErrPreparationProfile
		}
		if v, exists := info["description"]; exists {
			var text string
			if bytes.Equal(v, []byte("null")) || json.Unmarshal(v, &text) != nil {
				return ErrPreparationProfile
			}
		}
		if v, exists := info["deleted"]; exists {
			var deleted time.Time
			if len(v) > 128 || json.Unmarshal(v, &deleted) != nil || !deleted.IsZero() {
				return ErrPreparationProfile
			}
		}
		if err := checkProfileChart(ctx, root["chart"]); err != nil {
			return err
		}
		var err error
		items, err = previewItemsChecked(ctx, manifest, ns, checkProfileConfigMap)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return ErrPreparationProfile
		}
		for _, item := range items {
			if item.APIVersion != "v1" || item.Kind != "ConfigMap" || item.Hook || item.Keep || item.CRD || item.ForeignNamespace {
				return ErrPreparationProfile
			}
		}
		return ctx.Err()
	})
	if err != nil {
		return nil, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, ErrPreviewUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &UninstallProfile{preview: &UninstallPreview{id: hex.EncodeToString(id[:]), session: session, namespace: ns,
		revision: row, summary: summary, items: items, created: started, expires: started.Add(PreparationLifetime)}}, nil
}

// SoleDeployedRevision validates the complete namespace projection, not merely
// the first/highest row. Other releases may coexist; this one must have ONE row.
func SoleDeployedRevision(s Snapshot, ns string, row Revision) error {
	rows, err := LatestRevisionSet(s, ns, row)
	if err != nil {
		return err
	}
	if row.Status != "deployed" || len(rows) != 1 || rows[0] != row {
		return ErrPreparationProfile
	}
	return nil
}

// FinishUninstallPreparation validates the entire batch and chronology. Any
// unsupported/missing/ambiguous object invalidates the whole preparation; no
// filtering, partial capability, payload retention, or write is possible.
func FinishUninstallPreparation(ctx context.Context, profile *UninstallProfile, session string, before, after Snapshot, items []LiveItem, finished time.Time) (*UninstallPreparation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if profile == nil || profile.preview == nil {
		return nil, ErrPreparationStale
	}
	p := profile.preview
	if err := SoleDeployedRevision(before, p.namespace, p.revision); err != nil {
		return nil, err
	}
	if err := SoleDeployedRevision(after, p.namespace, p.revision); err != nil {
		return nil, err
	}
	if _, err := FinishLiveObservation(p, session, before, after, items, p.created, finished); err != nil {
		return nil, err
	}
	targets := make([]UninstallTarget, 0, len(items))
	seenUID := make(map[string]bool)
	for _, item := range items {
		if item.ObservedAt.Before(before.ObservedAt) || item.ObservedAt.After(after.ObservedAt) ||
			item.Stored.APIVersion != "v1" || item.Stored.Kind != "ConfigMap" || item.Resource != "configmaps" || item.Namespace != p.namespace ||
			item.State != "present" || item.Markers != "match" || item.Terminating || item.Finalizers || item.Owners || item.Keep || item.Hook || seenUID[item.UID] {
			return nil, ErrPreparationProfile
		}
		seenUID[item.UID] = true
		targets = append(targets, UninstallTarget{APIVersion: "v1", Kind: "ConfigMap", Resource: "configmaps", Namespace: p.namespace,
			Name: item.Stored.Name, UID: item.UID, ResourceVersion: item.ResourceVersion})
	}
	if len(targets) == 0 || len(targets) > MaxPreviewObjects {
		return nil, ErrPreparationProfile
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &UninstallPreparation{profile: profile, targets: targets, finished: finished}, nil
}

// Unknown structural chart/root fields are intentionally not accepted by this
// first profile. Opaque config/default values remain fully budget/JSON checked.
func profileObject(data []byte, allowed ...string) (map[string]json.RawMessage, bool) {
	fields, ok := canonicalObject(data, allowed...)
	if !ok {
		return nil, false
	}
	for key := range fields {
		known := false
		for _, name := range allowed {
			known = known || key == name
		}
		if !known {
			return nil, false
		}
	}
	return fields, true
}
func emptyProfileArray(raw []byte) bool {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return true
	}
	var list []json.RawMessage
	return json.Unmarshal(raw, &list) == nil && len(list) == 0
}
func checkProfileChart(ctx context.Context, data []byte) error {
	chart, ok := profileObject(data, "metadata", "lock", "templates", "values", "schema", "schemamodtime", "files", "modtime")
	if !ok || !validProfileTimestamp(chart, "modtime") || !validProfileTimestamp(chart, "schemamodtime") {
		return ErrPreparationProfile
	}
	metadata, ok := canonicalObject(chart["metadata"], "dependencies", "type", "annotations", "apiVersion")
	if !ok || !emptyProfileArray(metadata["dependencies"]) {
		return ErrPreparationProfile
	}
	// Helm's stored Chart has no serialized dependencies tree. Declared or
	// locked dependencies cannot establish absence of hidden subchart CRDs.
	if v := chart["lock"]; len(v) != 0 && !bytes.Equal(v, []byte("null")) {
		return ErrPreparationProfile
	}
	if v, exists := metadata["type"]; exists {
		var typ string
		if bytes.Equal(v, []byte("null")) || json.Unmarshal(v, &typ) != nil || typ != "" && typ != "application" {
			return ErrPreparationProfile
		}
	}
	if v, exists := metadata["apiVersion"]; exists {
		var api string
		if json.Unmarshal(v, &api) != nil || api != "v1" && api != "v2" {
			return ErrPreparationProfile
		}
	}
	if v, exists := metadata["annotations"]; exists {
		var annotations map[string]string
		if json.Unmarshal(v, &annotations) != nil || restrictedAnnotations(annotations) {
			return ErrPreparationProfile
		}
	}
	if v, exists := chart["values"]; exists {
		var values map[string]json.RawMessage
		if json.Unmarshal(v, &values) != nil {
			return ErrPreparationProfile
		}
	}
	if v, exists := chart["schema"]; exists && !bytes.Equal(v, []byte("null")) {
		var encoded string
		if json.Unmarshal(v, &encoded) != nil {
			return ErrPreparationProfile
		}
		decoded, err := decodeBase64(encoded, MaxReleaseBytes)
		clear(decoded)
		if err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	count := 0
	for _, key := range []string{"files", "templates"} {
		var files []json.RawMessage
		if v, exists := chart[key]; exists && json.Unmarshal(v, &files) != nil {
			return ErrPreparationProfile
		}
		count += len(files)
		if count > MaxPreviewSegments {
			return ErrPreviewBudget
		}
		for _, v := range files {
			if err := ctx.Err(); err != nil {
				return err
			}
			f, ok := profileObject(v, "name", "data", "modtime")
			if !ok || !validProfileTimestamp(f, "modtime") {
				return ErrPreparationProfile
			}
			var name, encoded string
			if json.Unmarshal(f["name"], &name) != nil || json.Unmarshal(f["data"], &encoded) != nil || !safeProfilePath(name) || seen[name] {
				return ErrPreparationProfile
			}
			seen[name] = true
			// Conservatively reject *all* crds/charts segments, even a non-YAML
			// file; never normalize away a path or decode a nested archive.
			for _, part := range strings.Split(name, "/") {
				if strings.EqualFold(part, "crds") || strings.EqualFold(part, "charts") {
					return ErrPreparationProfile
				}
			}
			if (key == "templates") != strings.HasPrefix(name, "templates/") {
				return ErrPreparationProfile
			}
			b, err := decodeBase64(encoded, MaxReleaseBytes)
			clear(b)
			if err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

// Helm v4.3.0 serializes chart/schema times and optional common.File.modtime.
// These values are inert metadata, never the preparation clock or authority.
// Omission/zero time is valid; explicit null, aliases and malformed values are
// not made acceptable by adding the upstream field to the structural allowlist.
func validProfileTimestamp(fields map[string]json.RawMessage, key string) bool {
	raw, exists := fields[key]
	if !exists {
		return true
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) > 128 || len(raw) < 2 || raw[0] != '"' {
		return false
	}
	var stamp time.Time
	return json.Unmarshal(raw, &stamp) == nil
}

func safeProfilePath(s string) bool {
	if s == "" || len(s) > 512 || path.IsAbs(s) || path.Clean(s) != s || strings.ContainsAny(s, "\\:") {
		return false
	}
	for _, r := range s {
		if r < 33 || r > 126 {
			return false
		}
	}
	for _, part := range strings.Split(s, "/") {
		if part == "." || part == ".." || part == "" {
			return false
		}
	}
	return true
}
func restrictedAnnotations(m map[string]string) bool {
	for key := range m {
		if strings.EqualFold(key, "helm.sh/resource-policy") || strings.HasPrefix(strings.ToLower(key), "helm.sh/hook") {
			return true
		}
	}
	return false
}
func checkProfileConfigMap(obj *unstructured.Unstructured) error {
	if obj.GetAPIVersion() != "v1" || obj.GetKind() != "ConfigMap" {
		return ErrPreparationProfile
	}
	for key := range obj.Object {
		if key != "apiVersion" && key != "kind" && key != "metadata" && key != "data" && key != "binaryData" && key != "immutable" {
			return ErrPreparationProfile
		}
	}
	meta, ok := obj.Object["metadata"].(map[string]interface{})
	if !ok {
		return ErrPreparationProfile
	}
	for key := range meta {
		if key != "name" && key != "namespace" && key != "labels" && key != "annotations" {
			return ErrPreparationProfile
		}
	}
	data, err := obj.MarshalJSON()
	if err != nil {
		return ErrPreparationProfile
	}
	defer clear(data)
	var cm corev1.ConfigMap
	issues, err := strictjson.UnmarshalStrict(data, &cm)
	if err != nil || len(issues) != 0 || restrictedAnnotations(cm.Annotations) {
		return ErrPreparationProfile
	}
	for k, v := range cm.Labels {
		if len(validation.IsQualifiedName(k)) != 0 || len(validation.IsValidLabelValue(v)) != 0 {
			return ErrPreparationProfile
		}
	}
	for k := range cm.Annotations {
		if len(validation.IsQualifiedName(k)) != 0 {
			return ErrPreparationProfile
		}
	}
	for k := range cm.Data {
		if len(validation.IsConfigMapKey(k)) != 0 {
			return ErrPreparationProfile
		}
	}
	for k := range cm.BinaryData {
		if _, exists := cm.Data[k]; exists || len(validation.IsConfigMapKey(k)) != 0 {
			return ErrPreparationProfile
		}
	}
	return nil
}
