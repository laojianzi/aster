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

const schemaListMapDescription = "Required non-nullable string list identities."

// SchemaListMapsFixture publishes one-key and mixed-key structural map lists.
// The optional defaulted list is a separate server transformation boundary:
// Aster does not infer its optional key from the advertised default.
type SchemaListMapsFixture struct {
	Config    *rest.Config
	Backend   *kube.Backend
	Namespace string
	GVK       schema.GroupVersionKind
	GVR       schema.GroupVersionResource
	Live      *unstructured.Unstructured
	crd       *unstructured.Unstructured
	keyKind   string
}

func (f SchemaListMapsFixture) Draft(name string) *unstructured.Unstructured {
	var first, second any = "api", "worker"
	if f.keyKind == "integer" {
		first, second = int64(8080), int64(9090)
	}
	if f.keyKind == "boolean" {
		first, second = false, true
	}
	draft := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": f.GVK.GroupVersion().String(), "kind": f.GVK.Kind,
		"metadata": map[string]any{"name": name, "namespace": f.Namespace},
		"spec": map[string]any{
			"named": []any{
				map[string]any{"name": first, "value": "one"},
				map[string]any{"name": second, "value": "two"},
			},
			"scoped": []any{
				map[string]any{"zone": "east", "name": first, "value": "one"},
				map[string]any{"zone": "west", "name": first, "value": "two"},
			},
		},
	}}
	if f.keyKind == "boolean" {
		for _, raw := range draft.Object["spec"].(map[string]any)["scoped"].([]any) {
			raw.(map[string]any)["port"] = int64(9007199254740993)
		}
	}
	return draft
}

func NewSchemaListMaps(t *testing.T) SchemaListMapsFixture {
	t.Helper()
	return newSchemaListMaps(t, false)
}

// NewSchemaListMapsWithLegacyDuplicates isolates the update-validation boundary.
// It stores duplicates while named is atomic, then advertises map topology and
// proves that new duplicate creates are rejected. Only that isolated old object
// has invalid identities; the ordinary fixture always starts from a valid object.
func NewSchemaListMapsWithLegacyDuplicates(t *testing.T) SchemaListMapsFixture {
	t.Helper()
	return newSchemaListMapLegacy(t, "string")
}

func NewSchemaIntegerListMapsWithLegacyDuplicates(t *testing.T) SchemaListMapsFixture {
	t.Helper()
	return newSchemaListMapLegacy(t, "integer")
}

func newSchemaListMapLegacy(t *testing.T, keyKind string) SchemaListMapsFixture {
	t.Helper()
	f := newSchemaListMapsTyped(t, true, keyKind)
	description := schemaListMapDescription
	if keyKind != "string" {
		description = scalarListMapDescription(keyKind)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	crds := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	currentCRD, err := f.Backend.GetObject(ctx, crds, "", f.crd.GetName())
	if err != nil || currentCRD == nil || currentCRD.GetUID() != f.crd.GetUID() || currentCRD.GetResourceVersion() == "" {
		t.Fatalf("could not refresh the same isolated CRD before its topology transition: %v", err)
	}
	f.crd = currentCRD.DeepCopy()
	path := "/spec/versions/0/schema/openAPIV3Schema/properties/spec/properties/named/"
	body, err := json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": f.crd.GetUID()},
		{"op": "test", "path": "/metadata/resourceVersion", "value": f.crd.GetResourceVersion()},
		{"op": "test", "path": path + "x-kubernetes-list-type", "value": "atomic"},
		{"op": "replace", "path": path + "x-kubernetes-list-type", "value": "map"},
		{"op": "add", "path": path + "x-kubernetes-list-map-keys", "value": []any{"name"}},
		{"op": "replace", "path": path + "description", "value": description},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := f.Backend.PatchObject(ctx, crds, "", f.crd.GetName(), types.JSONPatchType, body, metav1.PatchOptions{FieldManager: "schema-list-map-fixture", FieldValidation: "Strict"})
	if err != nil || updated == nil || updated.GetUID() != f.crd.GetUID() || updated.GetName() != f.crd.GetName() || updated.GetResourceVersion() == "" || updated.GetResourceVersion() == f.crd.GetResourceVersion() {
		t.Fatalf("could not update the same isolated list-map CRD identity: %v", err)
	}
	f.crd = updated
	// Both the published document and actual admission must have observed the
	// transition. A description marker checks publication without using the
	// new local duplicate checker as a readiness oracle.
	probe := f.Draft("schema-list-map-transition-probe")
	probe.Object["spec"].(map[string]any)["named"].([]any)[1].(map[string]any)["name"] = probe.Object["spec"].(map[string]any)["named"].([]any)[0].(map[string]any)["name"]
	err = wait.PollUntilContextTimeout(ctx, 150*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		doc, err := f.Backend.SchemaDocument(ctx, f.GVK)
		if errors.Is(err, schemaassist.ErrNotFound) || errors.Is(err, schemaassist.ErrUnavailable) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		help, err := doc.Help(ctx, "/spec/named")
		if err != nil || help.Description != description {
			return false, err
		}
		result, createErr := f.Backend.CreateObject(ctx, f.GVR, f.Namespace, probe, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "schema-list-map-probe", FieldValidation: "Strict"})
		if _, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, probe.GetName()); !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("list-map transition dry-run persisted or could not prove absence: %v", err)
		}
		if createErr == nil {
			if result == nil || result.GroupVersionKind() != f.GVK || result.GetNamespace() != f.Namespace || result.GetName() != probe.GetName() || !reflect.DeepEqual(result.Object["spec"], probe.Object["spec"]) {
				return false, errors.New("list-map transition probe returned a different object")
			}
			return false, nil
		}
		var status apierrors.APIStatus
		if result != nil || !apierrors.IsInvalid(createErr) || !errors.As(createErr, &status) || status.Status().Details == nil {
			return false, createErr
		}
		for _, cause := range status.Status().Details.Causes {
			if cause.Type == metav1.CauseTypeFieldValueDuplicate && cause.Field == "spec.named[1]" {
				return true, nil
			}
		}
		return false, errors.New("list-map transition rejection did not identify the duplicate item")
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, f.Live.GetName())
	if err != nil || current == nil || current.GetUID() != f.Live.GetUID() || current.GetResourceVersion() != f.Live.GetResourceVersion() || current.GroupVersionKind() != f.GVK || current.GetNamespace() != f.Namespace || current.GetName() != f.Live.GetName() || !reflect.DeepEqual(current.Object["spec"], f.Live.Object["spec"]) {
		t.Fatalf("schema transition changed the stored legacy list object: %v", err)
	}
	return f
}

// NewSchemaIntegerListMaps uses the same isolated fixture lifecycle and
// published topology, with signed integer IDs and a mixed string/integer tuple.
func NewSchemaIntegerListMaps(t *testing.T) SchemaListMapsFixture {
	t.Helper()
	return newSchemaListMapsTyped(t, false, "integer")
}

func newSchemaListMaps(t *testing.T, legacyDuplicates bool) SchemaListMapsFixture {
	t.Helper()
	return newSchemaListMapsTyped(t, legacyDuplicates, "string")
}

func newSchemaListMapsTyped(t *testing.T, legacyDuplicates bool, keyKind string) SchemaListMapsFixture {
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
	ns, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "aster-list-maps-"}}, metav1.CreateOptions{FieldValidation: "Strict"})
	if err != nil {
		t.Fatal(err)
	}
	if ns.UID == "" {
		t.Fatal("list-map fixture namespace has no cleanup identity")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		uid := ns.UID
		if err := client.CoreV1().Namespaces().Delete(ctx, ns.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("delete isolated list-map namespace: %v", err)
		}
	})
	group := ns.Name + ".aster.test"
	f := SchemaListMapsFixture{Config: cfg, Backend: b, Namespace: ns.Name, keyKind: keyKind,
		GVK: schema.GroupVersionKind{Group: group, Version: "v1", Kind: "ListMapThing"},
		GVR: schema.GroupVersionResource{Group: group, Version: "v1", Resource: "listmapthings"}}
	raw := fmt.Sprintf(`{
 "apiVersion":"apiextensions.k8s.io/v1","kind":"CustomResourceDefinition",
 "metadata":{"name":"listmapthings.%s"},
 "spec":{"group":"%s","scope":"Namespaced","names":{"plural":"listmapthings","singular":"listmapthing","kind":"ListMapThing"},
  "versions":[{"name":"v1","served":true,"storage":true,"schema":{"openAPIV3Schema":{
   "type":"object","required":["spec"],"properties":{"spec":{"type":"object","required":["named","scoped"],"properties":{
    "named":{"type":"array","description":%q,"maxItems":256,"x-kubernetes-list-type":"map","x-kubernetes-list-map-keys":["name"],
     "items":{"type":"object","required":["name"],"properties":{"name":{"type":"string"},"value":{"type":"string"}}}},
    "scoped":{"type":"array","maxItems":256,"x-kubernetes-list-type":"map","x-kubernetes-list-map-keys":["zone","name"],
     "items":{"type":"object","required":["zone","name"],"properties":{"zone":{"type":"string"},"name":{"type":"string"},"value":{"type":"string"}}}},
    "defaulted":{"type":"array","maxItems":256,"x-kubernetes-list-type":"map","x-kubernetes-list-map-keys":["name"],
     "items":{"type":"object","properties":{"name":{"type":"string","default":"fallback"},"value":{"type":"string"}}}}
   }}}
  }}}]}}
 `, group, group, schemaListMapDescription)
	crd := &unstructured.Unstructured{}
	if err := crd.UnmarshalJSON([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	if keyKind != "string" {
		versions := crd.Object["spec"].(map[string]any)["versions"].([]any)
		root := versions[0].(map[string]any)["schema"].(map[string]any)["openAPIV3Schema"].(map[string]any)
		fields := root["properties"].(map[string]any)["spec"].(map[string]any)["properties"].(map[string]any)
		for _, field := range []string{"named", "scoped", "defaulted"} {
			list := fields[field].(map[string]any)
			list["description"] = scalarListMapDescription(keyKind)
			properties := list["items"].(map[string]any)["properties"].(map[string]any)
			key := map[string]any{"type": keyKind}
			if field == "defaulted" {
				key["default"] = int64(8080)
				if keyKind == "boolean" {
					key["default"] = true
				}
			}
			properties["name"] = key
			if keyKind == "boolean" && field == "scoped" {
				list["x-kubernetes-list-map-keys"] = []any{"zone", "name", "port"}
				list["items"].(map[string]any)["required"] = []any{"zone", "name", "port"}
				properties["port"] = map[string]any{"type": "integer"}
			}
		}
	}
	if legacyDuplicates {
		versions := crd.Object["spec"].(map[string]any)["versions"].([]any)
		root := versions[0].(map[string]any)["schema"].(map[string]any)["openAPIV3Schema"].(map[string]any)
		named := root["properties"].(map[string]any)["spec"].(map[string]any)["properties"].(map[string]any)["named"].(map[string]any)
		named["x-kubernetes-list-type"] = "atomic"
		named["description"] = "Initial atomic list for an isolated update-validation boundary."
		delete(named, "x-kubernetes-list-map-keys")
	}
	crds := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	created, err := b.CreateObject(ctx, crds, "", crd, metav1.CreateOptions{FieldManager: "schema-list-map-fixture", FieldValidation: "Strict"})
	if err != nil {
		t.Fatal(err)
	}
	if created.GetUID() == "" || created.GetResourceVersion() == "" || created.GetName() != crd.GetName() {
		t.Fatal("list-map fixture CRD has an incomplete cleanup identity")
	}
	f.crd = created
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		uid := created.GetUID()
		if err := b.DeleteObject(ctx, crds, "", created.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("delete isolated list-map CRD: %v", err)
		}
	})
	err = wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 20*time.Second, true, func(ctx context.Context) (bool, error) {
		obj, err := b.GetObject(ctx, crds, "", created.GetName())
		if err != nil {
			return false, err
		}
		if obj.GetUID() != created.GetUID() {
			return false, errors.New("list-map fixture CRD identity changed")
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
				return false, errors.New("malformed list-map CRD establishment condition")
			}
			switch {
			case condition["type"] == "NonStructuralSchema" && condition["status"] == "True":
				return false, errors.New("list-map fixture schema is not structural")
			case condition["type"] == "NamesAccepted" && condition["status"] == "False":
				return false, errors.New("list-map fixture resource name was rejected")
			case condition["type"] == "Established" && condition["status"] == "True":
				established = true
			}
		}
		if established {
			// Status updates after Create advance resourceVersion. The later
			// topology transition must test the current CRD version, not the
			// initial Create response's now-stale version.
			f.crd = obj.DeepCopy()
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
	draft := f.Draft("schema-list-map-live")
	if legacyDuplicates {
		draft.Object["spec"].(map[string]any)["named"].([]any)[1].(map[string]any)["name"] = draft.Object["spec"].(map[string]any)["named"].([]any)[0].(map[string]any)["name"]
	}
	f.Live, err = b.CreateObject(ctx, f.GVR, f.Namespace, draft, metav1.CreateOptions{FieldManager: "schema-list-map-fixture", FieldValidation: "Strict"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Live.GetUID() == "" || f.Live.GetResourceVersion() == "" || f.Live.GroupVersionKind() != f.GVK || f.Live.GetNamespace() != f.Namespace || f.Live.GetName() != draft.GetName() || !reflect.DeepEqual(f.Live.Object["spec"], draft.Object["spec"]) {
		t.Fatal("list-map fixture live object has an incomplete identity or changed spec")
	}
	return f
}

func scalarListMapDescription(kind string) string {
	if kind == "integer" {
		return "Exact decoded int64 list identities; strings distinguish mixed tuples."
	}
	return "Exact decoded boolean list identities; strings distinguish mixed tuples."
}
func NewSchemaBooleanListMaps(t *testing.T) SchemaListMapsFixture {
	t.Helper()
	return newSchemaListMapsTyped(t, false, "boolean")
}
func NewSchemaBooleanListMapsWithLegacyDuplicates(t *testing.T) SchemaListMapsFixture {
	t.Helper()
	return newSchemaListMapLegacy(t, "boolean")
}
