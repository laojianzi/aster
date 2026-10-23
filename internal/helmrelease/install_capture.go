package helmrelease

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
)

// InstallCapabilityAssumption is the default of the pinned v4.3.0 client.
// helm install has no --kube-version flag. This is not live cluster discovery.
const InstallCapabilityAssumption = "v1.37.0"

var (
	ErrInstallCapture  = errors.New("Helm install capture is invalid, unsupported or outside the restricted profile.")
	ErrInstallConsent  = errors.New("Helm install preparation requires a separate explicit read acknowledgement.")
	ErrInstallStale    = errors.New("Helm install candidate or preparation expired or changed; start a new explicit preparation.")
	ErrInstallConflict = errors.New("The release name or a target is already present; adoption and replacement are not supported.")
	ErrInstallCodec    = errors.New("Helm install storage encoding or identity is invalid; no bytes were produced.")
)

// InstallTarget contains only a bounded identity, not content or permission.
type InstallTarget struct{ Namespace, Name string }

// InstallCandidate is immutable inert content, not operation.Prepared or a
// server-approved installation. Sensitive captured bytes have no accessor and
// never enter JSON or formatting. Constructing it is not proof of CLI provenance.
type InstallCandidate struct {
	id, namespace, release, payload string
	created, expires                time.Time
	summary                         Inspection
	targets                         []InstallTarget
}

func (c *InstallCandidate) ID() string {
	if c == nil {
		return ""
	}
	return c.id
}
func (c *InstallCandidate) Namespace() string {
	if c == nil {
		return ""
	}
	return c.namespace
}
func (c *InstallCandidate) Release() string {
	if c == nil {
		return ""
	}
	return c.release
}
func (c *InstallCandidate) StorageName() string {
	if c == nil {
		return ""
	}
	return "sh.helm.release.v1." + c.release + ".v1"
}
func (c *InstallCandidate) Summary() Inspection {
	if c == nil {
		return Inspection{}
	}
	return c.summary
}
func (c *InstallCandidate) Targets() []InstallTarget {
	if c == nil {
		return nil
	}
	return append([]InstallTarget(nil), c.targets...)
}
func (c *InstallCandidate) ExpiresAt() time.Time {
	if c == nil {
		return time.Time{}
	}
	return c.expires
}
func (c *InstallCandidate) Valid(now time.Time) bool {
	return c != nil && c.id != "" && !now.Before(c.created) && now.Before(c.expires)
}
func (c InstallCandidate) Format(f fmt.State, _ rune) {
	_, _ = io.WriteString(f, "Helm install candidate (sensitive content withheld)")
}
func (c InstallCandidate) MarshalJSON() ([]byte, error) {
	return []byte(`{"kind":"inert-helm-install-candidate"}`), nil
}

// DecodeInstallCapture validates one COMPLETE client-only release. It does not
// convert a Render result, read files, rerender, acquire consent or perform I/O.
func DecodeInstallCapture(ctx context.Context, data []byte, ns, release string, now time.Time) (*InstallCandidate, error) {
	if ctx == nil {
		return nil, ErrInstallCapture
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !ValidNamespace(ns) || len(release) == 0 || len(release) > 53 || len(validation.IsDNS1123Label(release)) != 0 ||
		now.IsZero() || now.Year() < 1970 || now.Year() > 9999 || len(data) > MaxReleaseBytes {
		return nil, ErrInstallCapture
	}
	if err := checkJSON(ctx, data); err != nil {
		return nil, err
	}
	root, ok := profileObject(data, "name", "namespace", "version", "info", "chart", "manifest", "config", "hooks", "apply_method")
	if !ok || !emptyProfileArray(root["hooks"]) {
		return nil, ErrInstallCapture
	}
	var v struct {
		Name      string                     `json:"name"`
		Namespace string                     `json:"namespace"`
		Version   int                        `json:"version"`
		Manifest  string                     `json:"manifest"`
		Method    string                     `json:"apply_method"`
		Config    map[string]json.RawMessage `json:"config"`
	}
	if json.Unmarshal(data, &v) != nil || v.Name != release || v.Namespace != ns || v.Version != 1 || v.Method != "ssa" ||
		len(v.Manifest) > MaxManifestBytes || len(v.Config) > MaxValuesKeys {
		return nil, ErrInstallCapture
	}
	info, ok := profileObject(root["info"], "first_deployed", "last_deployed", "deleted", "description", "status", "notes")
	if !ok {
		return nil, ErrInstallCapture
	}
	var status, description string
	if json.Unmarshal(info["status"], &status) != nil || status != "pending-install" ||
		json.Unmarshal(info["description"], &description) != nil || description != "Dry run complete" {
		return nil, ErrInstallCapture
	}
	var first, last time.Time
	if json.Unmarshal(info["first_deployed"], &first) != nil || json.Unmarshal(info["last_deployed"], &last) != nil ||
		first.IsZero() || !first.Equal(last) || first.Year() < 1970 || first.After(now) {
		return nil, ErrInstallCapture
	}
	if raw, exists := info["deleted"]; exists {
		var deleted time.Time
		if !validProfileTimestamp(info, "deleted") || json.Unmarshal(raw, &deleted) != nil || !deleted.IsZero() {
			return nil, ErrInstallCapture
		}
	}
	if raw, exists := info["notes"]; exists {
		var s string
		if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &s) != nil {
			return nil, ErrInstallCapture
		}
	}
	if err := checkProfileChart(ctx, root["chart"]); err != nil {
		return nil, err
	}
	chartFields, ok := canonicalObject(root["chart"], "metadata")
	if !ok {
		return nil, ErrInstallCapture
	}
	if _, ok := canonicalObject(chartFields["metadata"], "name", "version", "apiVersion"); !ok {
		return nil, ErrInstallCapture
	}
	var chart struct {
		Metadata struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			API     string `json:"apiVersion"`
		} `json:"metadata"`
	}
	if json.Unmarshal(root["chart"], &chart) != nil || chart.Metadata.Name == "" || len(chart.Metadata.Name) > 63 ||
		len(validation.IsDNS1123Subdomain(chart.Metadata.Name)) != 0 || !safeVersion(chart.Metadata.Version) || chart.Metadata.API != "v2" {
		return nil, ErrInstallCapture
	}
	// Reuse the full bounded YAML/profile validator; reject conflicting Helm
	// ownership markers rather than silently adopting or rewriting them.
	items, err := previewItemsChecked(ctx, v.Manifest, ns, func(obj *unstructured.Unstructured) error {
		if err := checkProfileConfigMap(obj); err != nil {
			return err
		}
		if obj.GetNamespace() != "" && obj.GetNamespace() != ns {
			return ErrInstallCapture
		}
		if value, exists := obj.GetLabels()["app.kubernetes.io/managed-by"]; exists && value != "Helm" {
			return ErrInstallCapture
		}
		for key, want := range map[string]string{"meta.helm.sh/release-name": release, "meta.helm.sh/release-namespace": ns} {
			if value, exists := obj.GetAnnotations()[key]; exists && value != want {
				return ErrInstallCapture
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 || len(items) > MaxPreviewObjects {
		return nil, ErrInstallCapture
	}
	targets := make([]InstallTarget, 0, len(items))
	for _, item := range items {
		if item.APIVersion != "v1" || item.Kind != "ConfigMap" || item.Hook || item.Keep || item.CRD || item.ForeignNamespace {
			return nil, ErrInstallCapture
		}
		targets = append(targets, InstallTarget{Namespace: ns, Name: item.Name})
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, ErrInstallCapture
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &InstallCandidate{id: hex.EncodeToString(id[:]), namespace: ns, release: release, payload: string(data),
		created: now, expires: now.Add(PreviewLifetime), targets: targets,
		summary: Inspection{ChartName: chart.Metadata.Name, ChartVersion: chart.Metadata.Version, ManifestBytes: len(v.Manifest), SuppliedValues: len(v.Config)}}, nil
}
