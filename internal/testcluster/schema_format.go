//go:build e2e

package testcluster

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/schemaassist"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// SchemaFormatsFixture publishes the three supported Kubernetes string formats
// and a nullable date. It does not assert support for other format names or
// equate these bounded checks with full OpenAPI or server admission validation.
type SchemaFormatsFixture struct {
	Config    *rest.Config
	Backend   *kube.Backend
	Namespace string
	GVK       schema.GroupVersionKind
	GVR       schema.GroupVersionResource
	Live      *unstructured.Unstructured
}

func (f SchemaFormatsFixture) Draft(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": f.GVK.GroupVersion().String(), "kind": f.GVK.Kind,
		"metadata": map[string]any{"name": name, "namespace": f.Namespace},
		"spec": map[string]any{
			"date": "2024-02-29", "byte": "YWJj",
			"uuid": "123e4567-e89b-12d3-a456-426614174000", "nullable": nil,
		},
	}}
}

func NewSchemaFormats(t *testing.T) SchemaFormatsFixture {
	t.Helper()
	cfg := Config(t)
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := kube.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ns, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "aster-formats-"}}, metav1.CreateOptions{FieldValidation: "Strict"})
	if err != nil {
		t.Fatal(err)
	}
	if ns.UID == "" {
		t.Fatal("format fixture namespace has no cleanup identity")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		uid := ns.UID
		if err := client.CoreV1().Namespaces().Delete(ctx, ns.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("delete isolated format namespace: %v", err)
		}
	})
	group := ns.Name + ".aster.test"
	f := SchemaFormatsFixture{Config: cfg, Backend: b, Namespace: ns.Name,
		GVK: schema.GroupVersionKind{Group: group, Version: "v1", Kind: "FormatThing"},
		GVR: schema.GroupVersionResource{Group: group, Version: "v1", Resource: "formatthings"}}
	// Use actual nonempty format keywords in the published schema. An empty
	// format is omitted by schema serialization and cannot prove inspection.
	raw := fmt.Sprintf(`{
 "apiVersion":"apiextensions.k8s.io/v1","kind":"CustomResourceDefinition",
 "metadata":{"name":"formatthings.%s"},
 "spec":{"group":"%s","scope":"Namespaced","names":{"plural":"formatthings","singular":"formatthing","kind":"FormatThing"},
  "versions":[{"name":"v1","served":true,"storage":true,"schema":{"openAPIV3Schema":{
   "type":"object","required":["spec"],"properties":{"spec":{"type":"object","required":["date","byte","uuid","nullable"],"properties":{
    "date":{"type":"string","format":"date"},
    "byte":{"type":"string","format":"byte"},
    "uuid":{"type":"string","format":"uuid"},
    "nullable":{"type":"string","nullable":true,"format":"date"}
   }}}
  }}}]}}
 `, group, group)
	crd := &unstructured.Unstructured{}
	if err := crd.UnmarshalJSON([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	crds := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	created, err := b.CreateObject(ctx, crds, "", crd, metav1.CreateOptions{FieldManager: "schema-format-fixture", FieldValidation: "Strict"})
	if err != nil {
		t.Fatal(err)
	}
	if created.GetUID() == "" {
		t.Fatal("format fixture CRD has no cleanup identity")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		uid := created.GetUID()
		if err := b.DeleteObject(ctx, crds, "", created.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("delete isolated format CRD: %v", err)
		}
	})
	err = wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 20*time.Second, true, func(ctx context.Context) (bool, error) {
		obj, err := b.GetObject(ctx, crds, "", created.GetName())
		if err != nil {
			return false, err
		}
		if obj.GetUID() != created.GetUID() {
			return false, errors.New("format fixture CRD identity changed")
		}
		value, found, err := unstructured.NestedFieldNoCopy(obj.Object, "status", "conditions")
		if err != nil || !found || value == nil {
			return false, err
		}
		conditions, _, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
		if err != nil {
			return false, err
		}
		established := false
		for _, item := range conditions {
			condition, ok := item.(map[string]any)
			if !ok {
				return false, errors.New("malformed format CRD establishment condition")
			}
			switch {
			case condition["type"] == "NonStructuralSchema" && condition["status"] == "True":
				return false, errors.New("format fixture schema is not structural")
			case condition["type"] == "NamesAccepted" && condition["status"] == "False":
				return false, errors.New("format fixture resource name was rejected")
			case condition["type"] == "Established" && condition["status"] == "True":
				established = true
			}
		}
		return established, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = wait.PollUntilContextTimeout(ctx, 150*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		_, err := b.SchemaDocument(ctx, f.GVK)
		if errors.Is(err, schemaassist.ErrNotFound) || errors.Is(err, schemaassist.ErrUnavailable) {
			return false, nil
		}
		return err == nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	f.Live, err = b.CreateObject(ctx, f.GVR, f.Namespace, f.Draft("schema-format-live"), metav1.CreateOptions{FieldManager: "schema-format-fixture", FieldValidation: "Strict"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Live.GetUID() == "" || f.Live.GetResourceVersion() == "" || f.Live.GroupVersionKind() != f.GVK || f.Live.GetNamespace() != f.Namespace || f.Live.GetName() != "schema-format-live" {
		t.Fatal("format fixture live object has an incomplete or unexpected identity")
	}
	return f
}
