//go:build e2e

package testcluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/schemaassist"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const schemaListSetDescription = "Non-nullable exact string set entries."

// SchemaListSetsFixture publishes a string set, an item-default boundary, and
// a nullable parent array whose individual string items remain non-nullable.
type SchemaListSetsFixture struct {
	Config    *rest.Config
	Backend   *kube.Backend
	Namespace string
	GVK       schema.GroupVersionKind
	GVR       schema.GroupVersionResource
	Live      *unstructured.Unstructured
	crd       *unstructured.Unstructured
}

func (f SchemaListSetsFixture) Draft(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": f.GVK.GroupVersion().String(), "kind": f.GVK.Kind,
		"metadata": map[string]any{"name": name, "namespace": f.Namespace},
		"spec": map[string]any{
			"values":    []any{"api", "中文船🚢", ""},
			"defaulted": []any{"explicit"},
			"nullable":  nil,
		},
	}}
}

func NewSchemaListSets(t *testing.T) SchemaListSetsFixture {
	t.Helper()
	return newSchemaListSets(t, false)
}

// NewSchemaListSetsWithLegacyDuplicates stores duplicates under an isolated
// atomic list, then changes only that list to set topology. Publication and a
// real fresh-create duplicate rejection must both observe the transition.
func NewSchemaListSetsWithLegacyDuplicates(t *testing.T) SchemaListSetsFixture {
	t.Helper()
	f := newSchemaListSets(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	crds := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	current, err := f.Backend.GetObject(ctx, crds, "", f.crd.GetName())
	if err != nil || current == nil || current.GetUID() != f.crd.GetUID() || current.GetResourceVersion() == "" {
		t.Fatalf("could not refresh the same isolated set CRD before transition: %v", err)
	}
	f.crd = current.DeepCopy()
	path := "/spec/versions/0/schema/openAPIV3Schema/properties/spec/properties/values/"
	body, err := json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": f.crd.GetUID()},
		{"op": "test", "path": "/metadata/resourceVersion", "value": f.crd.GetResourceVersion()},
		{"op": "test", "path": path + "x-kubernetes-list-type", "value": "atomic"},
		{"op": "replace", "path": path + "x-kubernetes-list-type", "value": "set"},
		{"op": "replace", "path": path + "description", "value": schemaListSetDescription},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := f.Backend.PatchObject(ctx, crds, "", f.crd.GetName(), types.JSONPatchType, body, metav1.PatchOptions{FieldManager: "schema-list-set-fixture", FieldValidation: "Strict"})
	if err != nil || updated == nil || updated.GetUID() != f.crd.GetUID() || updated.GetName() != f.crd.GetName() || updated.GetResourceVersion() == "" || updated.GetResourceVersion() == f.crd.GetResourceVersion() {
		t.Fatalf("could not transition the same isolated set CRD identity: %v", err)
	}
	f.crd = updated
	probe := f.Draft("schema-list-set-transition-probe")
	probe.Object["spec"].(map[string]any)["values"] = []any{"api", "api"}
	err = wait.PollUntilContextTimeout(ctx, 150*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		doc, err := f.Backend.SchemaDocument(ctx, f.GVK)
		if errors.Is(err, schemaassist.ErrNotFound) || errors.Is(err, schemaassist.ErrUnavailable) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		// The description marker does not use the new local checker as a
		// readiness oracle for its own test fixture.
		help, err := doc.Help(ctx, "/spec/values")
		if err != nil || help.Description != schemaListSetDescription {
			return false, err
		}
		result, createErr := f.Backend.CreateObject(ctx, f.GVR, f.Namespace, probe, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "schema-list-set-probe", FieldValidation: "Strict"})
		if _, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, probe.GetName()); !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("set transition dry-run persisted or could not prove absence: %v", err)
		}
		if createErr == nil {
			if result == nil || result.GroupVersionKind() != f.GVK || result.GetNamespace() != f.Namespace || result.GetName() != probe.GetName() || !reflect.DeepEqual(result.Object["spec"], probe.Object["spec"]) {
				return false, errors.New("set transition probe returned a different object")
			}
			return false, nil
		}
		var status apierrors.APIStatus
		if result != nil || !apierrors.IsInvalid(createErr) || !errors.As(createErr, &status) || status.Status().Details == nil {
			return false, createErr
		}
		causes := status.Status().Details.Causes
		if len(causes) == 1 && causes[0].Type == metav1.CauseTypeFieldValueDuplicate && causes[0].Field == "spec.values[1]" {
			return true, nil
		}
		return false, errors.New("set transition rejection did not identify only the duplicate item")
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err = f.Backend.GetObject(ctx, f.GVR, f.Namespace, f.Live.GetName())
	if err != nil || current == nil || current.GetUID() != f.Live.GetUID() || current.GetResourceVersion() != f.Live.GetResourceVersion() || current.GroupVersionKind() != f.GVK || current.GetNamespace() != f.Namespace || current.GetName() != f.Live.GetName() || !reflect.DeepEqual(current.Object["spec"], f.Live.Object["spec"]) || !reflect.DeepEqual(current.GetLabels(), f.Live.GetLabels()) {
		t.Fatalf("set schema transition changed the stored legacy object: %v", err)
	}
	return f
}

func newSchemaListSets(t *testing.T, legacyDuplicates bool) SchemaListSetsFixture {
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
	ns, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "aster-list-sets-"}}, metav1.CreateOptions{FieldValidation: "Strict"})
	if err != nil {
		t.Fatal(err)
	}
	if ns == nil || ns.UID == "" {
		t.Fatal("list-set fixture namespace has no cleanup identity")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		uid := ns.UID
		if err := client.CoreV1().Namespaces().Delete(ctx, ns.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("delete isolated list-set namespace: %v", err)
		}
	})
	group := ns.Name + ".aster.test"
	f := SchemaListSetsFixture{Config: cfg, Backend: b, Namespace: ns.Name,
		GVK: schema.GroupVersionKind{Group: group, Version: "v1", Kind: "ListSetThing"},
		GVR: schema.GroupVersionResource{Group: group, Version: "v1", Resource: "listsetthings"}}
	raw := fmt.Sprintf(`{
 "apiVersion":"apiextensions.k8s.io/v1","kind":"CustomResourceDefinition",
 "metadata":{"name":"listsetthings.%s"},
 "spec":{"group":"%s","scope":"Namespaced","names":{"plural":"listsetthings","singular":"listsetthing","kind":"ListSetThing"},
  "versions":[{"name":"v1","served":true,"storage":true,"schema":{"openAPIV3Schema":{
   "type":"object","required":["spec"],"properties":{"spec":{"type":"object","required":["values","defaulted","nullable"],"properties":{
    "values":{"type":"array","description":%q,"maxItems":256,"x-kubernetes-list-type":"set","items":{"type":"string"}},
    "defaulted":{"type":"array","maxItems":256,"x-kubernetes-list-type":"set","items":{"type":"string","default":"fallback"}},
    "nullable":{"type":"array","nullable":true,"maxItems":256,"x-kubernetes-list-type":"set","items":{"type":"string"}}
   }}}
  }}}]}}
 `, group, group, schemaListSetDescription)
	crd := &unstructured.Unstructured{}
	if err := crd.UnmarshalJSON([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	if legacyDuplicates {
		versions := crd.Object["spec"].(map[string]any)["versions"].([]any)
		root := versions[0].(map[string]any)["schema"].(map[string]any)["openAPIV3Schema"].(map[string]any)
		values := root["properties"].(map[string]any)["spec"].(map[string]any)["properties"].(map[string]any)["values"].(map[string]any)
		values["x-kubernetes-list-type"] = "atomic"
		values["description"] = "Initial atomic list for an isolated set update-validation boundary."
	}
	crds := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	created, err := b.CreateObject(ctx, crds, "", crd, metav1.CreateOptions{FieldManager: "schema-list-set-fixture", FieldValidation: "Strict"})
	if err != nil {
		t.Fatal(err)
	}
	if created == nil || created.GetUID() == "" || created.GetResourceVersion() == "" || created.GetName() != crd.GetName() {
		t.Fatal("list-set fixture CRD has an incomplete cleanup identity")
	}
	f.crd = created
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		uid := created.GetUID()
		if err := b.DeleteObject(ctx, crds, "", created.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("delete isolated list-set CRD: %v", err)
		}
	})
	err = wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 20*time.Second, true, func(ctx context.Context) (bool, error) {
		obj, err := b.GetObject(ctx, crds, "", created.GetName())
		if err != nil {
			return false, err
		}
		if obj == nil || obj.GetUID() != created.GetUID() || obj.GetResourceVersion() == "" {
			return false, errors.New("list-set fixture CRD identity changed")
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
				return false, errors.New("malformed list-set CRD establishment condition")
			}
			switch {
			case condition["type"] == "NonStructuralSchema" && condition["status"] == "True":
				return false, errors.New("list-set fixture schema is not structural")
			case condition["type"] == "NamesAccepted" && condition["status"] == "False":
				return false, errors.New("list-set fixture resource name was rejected")
			case condition["type"] == "Established" && condition["status"] == "True":
				established = true
			}
		}
		if established {
			f.crd = obj.DeepCopy()
		}
		return established, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = wait.PollUntilContextTimeout(ctx, 150*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		doc, err := b.SchemaDocument(ctx, f.GVK)
		if errors.Is(err, schemaassist.ErrNotFound) || errors.Is(err, schemaassist.ErrUnavailable) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		help, err := doc.Help(ctx, "/spec/values")
		if err != nil {
			return false, err
		}
		if legacyDuplicates {
			return help.Description == "Initial atomic list for an isolated set update-validation boundary.", nil
		}
		return help.Description == schemaListSetDescription, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	draft := f.Draft("schema-list-set-live")
	if legacyDuplicates {
		draft.Object["spec"].(map[string]any)["values"] = []any{"api", "api"}
	}
	f.Live, err = b.CreateObject(ctx, f.GVR, f.Namespace, draft, metav1.CreateOptions{FieldManager: "schema-list-set-fixture", FieldValidation: "Strict"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Live == nil || f.Live.GetUID() == "" || f.Live.GetResourceVersion() == "" || f.Live.GroupVersionKind() != f.GVK || f.Live.GetNamespace() != f.Namespace || f.Live.GetName() != draft.GetName() || !reflect.DeepEqual(f.Live.Object["spec"], draft.Object["spec"]) {
		t.Fatal("list-set fixture live object has an incomplete identity or changed spec")
	}
	return f
}
