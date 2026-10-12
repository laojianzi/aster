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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// SchemaConstraintsFixture publishes an isolated, structural CRD. Its numeric
// bounds are representable by Kubernetes' CRD schema fields; the >2^53 lexical
// boundary regression is deliberately exercised by the native JSON fixture.
type SchemaConstraintsFixture struct {
	Config    *rest.Config
	Backend   *kube.Backend
	Namespace string
	GVK       schema.GroupVersionKind
	GVR       schema.GroupVersionResource
	Live      *unstructured.Unstructured
}

func (f SchemaConstraintsFixture) Draft(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": f.GVK.GroupVersion().String(), "kind": f.GVK.Kind,
		"metadata": map[string]any{"name": name, "namespace": f.Namespace},
		"spec": map[string]any{
			"mode": "allowed", "enabled": true, "choice": int64(2),
			"replicas": int64(1), "fraction": 0.1, "exclusive": int64(5),
			"title": "中文🚢", "items": []any{"one"},
			"attributes": map[string]any{"one": "value"},
		},
	}}
}

func NewSchemaConstraints(t *testing.T) SchemaConstraintsFixture {
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
	ns, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "aster-schema-"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = client.CoreV1().Namespaces().Delete(ctx, ns.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &ns.UID}})
	})
	group := ns.Name + ".aster.test"
	f := SchemaConstraintsFixture{Config: cfg, Backend: b, Namespace: ns.Name,
		GVK: schema.GroupVersionKind{Group: group, Version: "v1", Kind: "ConstraintThing"},
		GVR: schema.GroupVersionResource{Group: group, Version: "v1", Resource: "constraintthings"}}
	raw := fmt.Sprintf(`{
 "apiVersion":"apiextensions.k8s.io/v1","kind":"CustomResourceDefinition",
 "metadata":{"name":"constraintthings.%s"},
 "spec":{"group":"%s","scope":"Namespaced","names":{"plural":"constraintthings","singular":"constraintthing","kind":"ConstraintThing"},
  "versions":[{"name":"v1","served":true,"storage":true,"schema":{"openAPIV3Schema":{
   "type":"object","required":["spec"],"properties":{"spec":{"type":"object","properties":{
    "mode":{"type":"string","enum":["allowed","secondary"]},
    "enabled":{"type":"boolean","enum":[true]},
    "choice":{"type":"integer","enum":[2,4]},
    "replicas":{"type":"integer","minimum":1,"maximum":5},
    "fraction":{"type":"number","minimum":0.1,"maximum":0.1},
    "exclusive":{"type":"number","minimum":0,"maximum":10,"exclusiveMinimum":true,"exclusiveMaximum":true},
    "title":{"type":"string","minLength":2,"maxLength":4},
    "items":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":2},
    "attributes":{"type":"object","additionalProperties":{"type":"string"},"minProperties":1,"maxProperties":2}
   }}}
  }}}]}}
 `, group, group)
	crd := &unstructured.Unstructured{}
	if err := crd.UnmarshalJSON([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	crds := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	created, err := b.CreateObject(ctx, crds, "", crd, metav1.CreateOptions{FieldManager: "schema-constraint-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		uid := created.GetUID()
		_ = b.DeleteObject(ctx, crds, "", created.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
	})
	err = wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 20*time.Second, true, func(ctx context.Context) (bool, error) {
		obj, err := b.GetObject(ctx, crds, "", created.GetName())
		if err != nil {
			return false, err
		}
		value, found, err := unstructured.NestedFieldNoCopy(obj.Object, "status", "conditions")
		if err != nil || !found || value == nil {
			return false, err
		}
		conditions, ok := value.([]any)
		if !ok {
			return false, fmt.Errorf("malformed CRD establishment conditions")
		}
		for _, item := range conditions {
			condition, ok := item.(map[string]any)
			if ok && condition["type"] == "Established" && condition["status"] == "True" {
				return true, nil
			}
		}
		return false, nil
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
	f.Live, err = b.CreateObject(ctx, f.GVR, f.Namespace, f.Draft("schema-constraints-live"), metav1.CreateOptions{FieldManager: "schema-constraint-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
