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

// SchemaMultiplesFixture publishes an isolated structural CRD. The primary
// fields have factors exactly representable by the API server's schema type.
// The optional decimal field exposes the server's floating-point boundary;
// they do not imply that a local exact check reproduces admission validation.
type SchemaMultiplesFixture struct {
	Config    *rest.Config
	Backend   *kube.Backend
	Namespace string
	GVK       schema.GroupVersionKind
	GVR       schema.GroupVersionResource
	Live      *unstructured.Unstructured
}

func (f SchemaMultiplesFixture) Draft(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": f.GVK.GroupVersion().String(), "kind": f.GVK.Kind,
		"metadata": map[string]any{"name": name, "namespace": f.Namespace},
		"spec": map[string]any{
			"even": int64(2), "quarter": 0.75, "half": 1.5,
		},
	}}
}

func NewSchemaMultiples(t *testing.T) SchemaMultiplesFixture {
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
	ns, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "aster-multiples-"}}, metav1.CreateOptions{FieldValidation: "Strict"})
	if err != nil {
		t.Fatal(err)
	}
	if ns.UID == "" {
		t.Fatal("multipleOf fixture namespace has no cleanup identity")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		uid := ns.UID
		if err := client.CoreV1().Namespaces().Delete(ctx, ns.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("delete isolated multipleOf namespace: %v", err)
		}
	})
	group := ns.Name + ".aster.test"
	f := SchemaMultiplesFixture{Config: cfg, Backend: b, Namespace: ns.Name,
		GVK: schema.GroupVersionKind{Group: group, Version: "v1", Kind: "MultipleThing"},
		GVR: schema.GroupVersionResource{Group: group, Version: "v1", Resource: "multiplethings"}}
	raw := fmt.Sprintf(`{
 "apiVersion":"apiextensions.k8s.io/v1","kind":"CustomResourceDefinition",
 "metadata":{"name":"multiplethings.%s"},
 "spec":{"group":"%s","scope":"Namespaced","names":{"plural":"multiplethings","singular":"multiplething","kind":"MultipleThing"},
  "versions":[{"name":"v1","served":true,"storage":true,"schema":{"openAPIV3Schema":{
   "type":"object","required":["spec"],"properties":{"spec":{"type":"object","required":["even","quarter","half"],"properties":{
    "even":{"type":"integer","multipleOf":2},
    "quarter":{"type":"number","multipleOf":0.25},
    "half":{"type":"number","multipleOf":0.5},
    "tenth":{"type":"number","multipleOf":0.1}
   }}}
  }}}]}}
 `, group, group)
	crd := &unstructured.Unstructured{}
	if err := crd.UnmarshalJSON([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	crds := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	created, err := b.CreateObject(ctx, crds, "", crd, metav1.CreateOptions{FieldManager: "schema-multiple-fixture", FieldValidation: "Strict"})
	if err != nil {
		t.Fatal(err)
	}
	if created.GetUID() == "" {
		t.Fatal("multipleOf fixture CRD has no cleanup identity")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		uid := created.GetUID()
		if err := b.DeleteObject(ctx, crds, "", created.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("delete isolated multipleOf CRD: %v", err)
		}
	})
	err = wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 20*time.Second, true, func(ctx context.Context) (bool, error) {
		obj, err := b.GetObject(ctx, crds, "", created.GetName())
		if err != nil {
			return false, err
		}
		if obj.GetUID() != created.GetUID() {
			return false, errors.New("multipleOf fixture CRD identity changed")
		}
		conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
		if err != nil || !found {
			return false, err
		}
		established := false
		for _, item := range conditions {
			condition, ok := item.(map[string]any)
			if !ok {
				return false, errors.New("malformed multipleOf CRD establishment condition")
			}
			switch {
			case condition["type"] == "NonStructuralSchema" && condition["status"] == "True":
				return false, errors.New("multipleOf fixture schema is not structural")
			case condition["type"] == "NamesAccepted" && condition["status"] == "False":
				return false, errors.New("multipleOf fixture resource name was rejected")
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
	f.Live, err = b.CreateObject(ctx, f.GVR, f.Namespace, f.Draft("schema-multiples-live"), metav1.CreateOptions{FieldManager: "schema-multiple-fixture", FieldValidation: "Strict"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Live.GetUID() == "" || f.Live.GetResourceVersion() == "" || f.Live.GroupVersionKind() != f.GVK || f.Live.GetNamespace() != f.Namespace {
		t.Fatal("multipleOf fixture live object has an incomplete or unexpected identity")
	}
	return f
}
