package helmrelease

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	strictjson "sigs.k8s.io/json"
)

// InstallFieldManager is fixed for the separately authorized create preflight.
const InstallFieldManager = "aster-helm-install"
const MaxInstallCreateBytes = 3 * MaxSecretBytes

type installCreate struct{ resource, name, body string }

// InstallCreatePlan freezes sensitive request bytes, not permission to send them.
// All accessors return copies. No server-generated value is an execution identity.
type InstallCreatePlan struct {
	candidateID, namespace string
	requests               []installCreate
}

func (p InstallCreatePlan) Format(f fmt.State, _ rune) {
	_, _ = io.WriteString(f, "Inert sensitive Helm create plan")
}
func (p InstallCreatePlan) MarshalJSON() ([]byte, error) {
	return []byte(`{"kind":"inert-helm-create-plan"}`), nil
}
func (p *InstallCreatePlan) Len() int {
	if p == nil {
		return 0
	}
	return len(p.requests)
}
func (p *InstallCreatePlan) CandidateID() string {
	if p == nil {
		return ""
	}
	return p.candidateID
}
func (p *InstallCreatePlan) Identity(i int) (resource, name string) {
	if p == nil || i < 0 || i >= len(p.requests) {
		return "", ""
	}
	r := p.requests[i]
	return r.resource, r.name
}

// SensitiveBody returns owned sensitive bytes; the caller must clear them and
// must not put them in diagnostics, a model, UI reports or persistent evidence.
func (p *InstallCreatePlan) SensitiveBody(i int) []byte {
	if p == nil || i < 0 || i >= len(p.requests) {
		return nil
	}
	return []byte(p.requests[i].body)
}

// BuildInstallCreatePlan uses only the original capture, never source files or
// a second rendering. Secret comes first; ConfigMaps retain manifest order.
func BuildInstallCreatePlan(ctx context.Context, c *InstallCandidate, now time.Time) (*InstallCreatePlan, error) {
	if ctx == nil || !c.Valid(now) {
		return nil, ErrInstallCodec
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	frozen := *c
	secret, err := BuildInstallSecret(ctx, &frozen, now)
	if err != nil {
		return nil, err
	}
	defer clear(secret)
	p := &InstallCreatePlan{candidateID: frozen.id, namespace: frozen.namespace,
		requests: []installCreate{{"secrets", frozen.StorageName(), string(secret)}}}
	var capture struct {
		Manifest string `json:"manifest"`
	}
	if json.Unmarshal([]byte(frozen.payload), &capture) != nil {
		return nil, ErrInstallCodec
	}
	total := len(secret)
	_, err = previewItemsChecked(ctx, capture.Manifest, frozen.namespace, func(obj *unstructured.Unstructured) error {
		if err := checkProfileConfigMap(obj); err != nil {
			return err
		}
		raw, err := obj.MarshalJSON()
		if err != nil {
			return ErrInstallCodec
		}
		defer clear(raw)
		var cm corev1.ConfigMap
		issues, err := strictjson.UnmarshalStrict(raw, &cm)
		if err != nil || len(issues) != 0 {
			return ErrInstallCodec
		}
		if cm.Namespace != "" && cm.Namespace != frozen.namespace {
			return ErrInstallCodec
		}
		cm.Namespace = frozen.namespace
		if cm.Labels == nil {
			cm.Labels = map[string]string{}
		}
		if v, ok := cm.Labels["app.kubernetes.io/managed-by"]; ok && v != "Helm" {
			return ErrInstallCodec
		}
		cm.Labels["app.kubernetes.io/managed-by"] = "Helm"
		if cm.Annotations == nil {
			cm.Annotations = map[string]string{}
		}
		for key, want := range map[string]string{"meta.helm.sh/release-name": frozen.release, "meta.helm.sh/release-namespace": frozen.namespace} {
			if v, ok := cm.Annotations[key]; ok && v != want {
				return ErrInstallCodec
			}
			cm.Annotations[key] = want
		}
		body, err := json.Marshal(cm)
		if err != nil {
			return ErrInstallCodec
		}
		defer clear(body)
		total += len(body)
		if len(body) > MaxSecretBytes || total > MaxInstallCreateBytes {
			return ErrInstallCodec
		}
		p.requests = append(p.requests, installCreate{"configmaps", cm.Name, string(body)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(p.requests) != len(frozen.targets)+1 {
		return nil, ErrInstallCodec
	}
	for i, t := range frozen.targets {
		if p.requests[i+1].name != t.Name || t.Namespace != frozen.namespace {
			return nil, ErrInstallCodec
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p, nil
}

// ValidateInstallCreateResponse validates complete typed content, allowing only
// enumerated generated metadata. It returns no Secret, payload, UID or RV.
func ValidateInstallCreateResponse(ctx context.Context, p *InstallCreatePlan, index int, response []byte) error {
	if ctx == nil || p == nil || index < 0 || index >= p.Len() || len(response) == 0 || len(response) > MaxSecretBytes {
		return ErrInstallCodec
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkJSON(ctx, response); err != nil {
		return ErrInstallCodec
	}
	request := p.SensitiveBody(index)
	defer clear(request)
	switch p.requests[index].resource {
	case "secrets":
		var want, got corev1.Secret
		defer func() {
			for _, s := range []*corev1.Secret{&want, &got} {
				for _, data := range s.Data {
					clear(data)
				}
			}
		}()
		issues, err := strictjson.UnmarshalStrict(response, &got)
		if err != nil || len(issues) != 0 || json.Unmarshal(request, &want) != nil {
			return ErrInstallCodec
		}
		if !installGeneratedMetadata(&got.ObjectMeta) {
			return ErrInstallCodec
		}
		want.CreationTimestamp = metav1.Time{}
		if !reflect.DeepEqual(want, got) {
			return ErrInstallCodec
		}
	case "configmaps":
		var want, got corev1.ConfigMap
		defer func() {
			for _, cm := range []*corev1.ConfigMap{&want, &got} {
				for _, data := range cm.BinaryData {
					clear(data)
				}
			}
		}()
		issues, err := strictjson.UnmarshalStrict(response, &got)
		if err != nil || len(issues) != 0 || json.Unmarshal(request, &want) != nil {
			return ErrInstallCodec
		}
		if !installGeneratedMetadata(&got.ObjectMeta) {
			return ErrInstallCodec
		}
		want.CreationTimestamp = metav1.Time{}
		if !reflect.DeepEqual(want, got) {
			return ErrInstallCodec
		}
	default:
		return ErrInstallCodec
	}
	return ctx.Err()
}
func installGeneratedMetadata(m *metav1.ObjectMeta) bool {
	if !safeMetadataID(string(m.UID), 128) || m.ResourceVersion != "" && !safeMetadataID(m.ResourceVersion, 256) ||
		m.CreationTimestamp.IsZero() || m.CreationTimestamp.Year() < 1970 || m.CreationTimestamp.Year() > 9999 || len(m.ManagedFields) > 1 {
		return false
	}
	for _, f := range m.ManagedFields {
		if f.Manager != InstallFieldManager || f.Operation != metav1.ManagedFieldsOperationUpdate || f.APIVersion != "v1" ||
			f.Subresource != "" || f.FieldsType != "FieldsV1" || f.FieldsV1 == nil {
			return false
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(f.FieldsV1.Raw, &fields) != nil || fields == nil {
			return false
		}
	}
	m.UID = ""
	m.ResourceVersion = ""
	m.CreationTimestamp = metav1.Time{}
	m.ManagedFields = nil
	return true
}
