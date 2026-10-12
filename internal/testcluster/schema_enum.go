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

// SchemaEnumsFixture advertises structural object and array choices. The
// nullable root enum is a separate local/server boundary, not an assertion
// that local decoded-value equality implements complete server admission.
type SchemaEnumsFixture struct {
	Config    *rest.Config
	Backend   *kube.Backend
	Namespace string
	GVK       schema.GroupVersionKind
	GVR       schema.GroupVersionResource
	Live      *unstructured.Unstructured
}

func (f SchemaEnumsFixture) Draft(name string) *unstructured.Unstructured {
	first := func() any {
		return map[string]any{"code": "start", "enabled": true, "count": int64(9007199254740993), "ratio": float64(0.5), "optional": nil}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": f.GVK.GroupVersion().String(), "kind": f.GVK.Kind,
		"metadata": map[string]any{"name": name, "namespace": f.Namespace},
		"spec": map[string]any{
			"objectChoice": map[string]any{
				"label": "alpha", "enabled": true, "count": int64(9007199254740993), "ratio": float64(0.5), "note": "中文船🚢",
				"nested": map[string]any{"steps": []any{"first", "second", "first"}, "optional": nil, "emptyObject": map[string]any{}, "emptyArray": []any{}},
			},
			"arrayChoice": []any{first(), map[string]any{"code": "结束船", "enabled": false, "count": int64(2), "ratio": float64(1.5), "optional": "ok"}, first()},
			"emptyObject": map[string]any{}, "emptyArray": []any{}, "nullableObject": map[string]any{"mode": "ok"},
		},
	}}
}

func NewSchemaEnums(t *testing.T) SchemaEnumsFixture {
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
	ns, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "aster-enums-"}}, metav1.CreateOptions{FieldValidation: "Strict"})
	if err != nil {
		t.Fatal(err)
	}
	if ns.UID == "" {
		t.Fatal("enum fixture namespace has no cleanup identity")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		uid := ns.UID
		if err := client.CoreV1().Namespaces().Delete(ctx, ns.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("delete isolated enum namespace: %v", err)
		}
	})
	group := ns.Name + ".aster.test"
	f := SchemaEnumsFixture{Config: cfg, Backend: b, Namespace: ns.Name,
		GVK: schema.GroupVersionKind{Group: group, Version: "v1", Kind: "EnumThing"},
		GVR: schema.GroupVersionResource{Group: group, Version: "v1", Resource: "enumthings"}}
	// Every nested value has structural schema. No defaults or preserved
	// unknown fields can silently turn a rejected enum member into a match.
	// The optional extra/token properties are declared so those rejection
	// cases test equality rather than unknown-field pruning or strict decoding.
	raw := fmt.Sprintf(`{
 "apiVersion":"apiextensions.k8s.io/v1","kind":"CustomResourceDefinition",
 "metadata":{"name":"enumthings.%s"},
 "spec":{"group":"%s","scope":"Namespaced","names":{"plural":"enumthings","singular":"enumthing","kind":"EnumThing"},
  "versions":[{"name":"v1","served":true,"storage":true,"schema":{"openAPIV3Schema":{
   "type":"object","required":["spec"],"properties":{"spec":{"type":"object","required":["objectChoice","arrayChoice","emptyObject","emptyArray","nullableObject"],"properties":{
    "objectChoice":{"type":"object","properties":{
     "label":{"type":"string"},"enabled":{"type":"boolean"},"count":{"type":"integer"},"ratio":{"type":"number"},"note":{"type":"string"},"extra":{"type":"string"},
     "nested":{"type":"object","properties":{"steps":{"type":"array","items":{"type":"string"}},"optional":{"type":"string","nullable":true},"emptyObject":{"type":"object","properties":{"token":{"type":"string"}}},"emptyArray":{"type":"array","items":{"type":"string"}}}}
    },"enum":[
     {"label":"alpha","enabled":true,"count":9007199254740993,"ratio":0.5,"note":"中文船🚢","nested":{"steps":["first","second","first"],"optional":null,"emptyObject":{},"emptyArray":[]}},
     {"label":"beta","enabled":true,"count":9007199254740993,"ratio":0.5,"note":"中文船🚢","nested":{"steps":["first","second","first"],"optional":null,"emptyObject":{},"emptyArray":[]}}
    ]},
    "arrayChoice":{"type":"array","items":{"type":"object","properties":{"code":{"type":"string"},"enabled":{"type":"boolean"},"count":{"type":"integer"},"ratio":{"type":"number"},"optional":{"type":"string","nullable":true}}},"enum":[
     [{"code":"start","enabled":true,"count":9007199254740993,"ratio":0.5,"optional":null},{"code":"结束船","enabled":false,"count":2,"ratio":1.5,"optional":"ok"},{"code":"start","enabled":true,"count":9007199254740993,"ratio":0.5,"optional":null}],
     [{"code":"结束船","enabled":false,"count":2,"ratio":1.5,"optional":"ok"},{"code":"start","enabled":true,"count":9007199254740993,"ratio":0.5,"optional":null}]
    ]},
    "emptyObject":{"type":"object","properties":{"token":{"type":"string"}},"enum":[{}]},
    "emptyArray":{"type":"array","items":{"type":"string"},"enum":[[]]},
    "nullableObject":{"type":"object","nullable":true,"properties":{"mode":{"type":"string"}},"enum":[null,{"mode":"ok"}]}
   }}}
  }}}]}}
 `, group, group)
	crd := &unstructured.Unstructured{}
	if err := crd.UnmarshalJSON([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	crds := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	created, err := b.CreateObject(ctx, crds, "", crd, metav1.CreateOptions{FieldManager: "schema-enum-fixture", FieldValidation: "Strict"})
	if err != nil {
		t.Fatal(err)
	}
	if created.GetUID() == "" {
		t.Fatal("enum fixture CRD has no cleanup identity")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		uid := created.GetUID()
		if err := b.DeleteObject(ctx, crds, "", created.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("delete isolated enum CRD: %v", err)
		}
	})
	err = wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 20*time.Second, true, func(ctx context.Context) (bool, error) {
		obj, err := b.GetObject(ctx, crds, "", created.GetName())
		if err != nil {
			return false, err
		}
		if obj.GetUID() != created.GetUID() {
			return false, errors.New("enum fixture CRD identity changed")
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
				return false, errors.New("malformed enum CRD establishment condition")
			}
			switch {
			case condition["type"] == "NonStructuralSchema" && condition["status"] == "True":
				return false, errors.New("enum fixture schema is not structural")
			case condition["type"] == "NamesAccepted" && condition["status"] == "False":
				return false, errors.New("enum fixture resource name was rejected")
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
	f.Live, err = b.CreateObject(ctx, f.GVR, f.Namespace, f.Draft("schema-enum-live"), metav1.CreateOptions{FieldManager: "schema-enum-fixture", FieldValidation: "Strict"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Live.GetUID() == "" || f.Live.GetResourceVersion() == "" || f.Live.GroupVersionKind() != f.GVK || f.Live.GetNamespace() != f.Namespace || f.Live.GetName() != "schema-enum-live" {
		t.Fatal("enum fixture live object has an incomplete or unexpected identity")
	}
	return f
}
