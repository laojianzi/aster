//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/operation"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
)

func TestRealApplySharedOwnershipWithoutValueChanges(t *testing.T) {
	f := newApplyFixture(t)
	s := operation.NewService(f.b, f.target.SessionID)
	p, err := s.PrepareApply(f.ctx, f.target, f.intent(t, map[string]string{"foreign": "controller-value"}))
	if err != nil {
		t.Fatal(err)
	}
	before, after := p.Preview()
	changes, err := manifest.Compare(before, after)
	if err != nil || len(changes) != 0 {
		t.Fatalf("expected ownership-only preview: %v %v", changes, err)
	}
	var post unstructured.Unstructured
	if err = post.UnmarshalJSON(after); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(manifest.Ownership(&post), operation.ApplyFieldManager) {
		t.Fatal("ownership-only effect was hidden")
	}
	initial := f.read(t)
	if _, err = s.Execute(f.ctx, p); err != nil {
		t.Fatal(err)
	}
	applied := f.read(t)
	if applied.ResourceVersion == initial.ResourceVersion || applied.Data["foreign"] != "controller-value" {
		t.Fatal("expected ownership change without value mutation")
	}
	managers := map[string]bool{}
	for _, m := range applied.ManagedFields {
		managers[m.Manager] = true
	}
	if !managers["fixture-owner"] || !managers[operation.ApplyFieldManager] {
		t.Fatal("field managers were not retained")
	}
	// Omission relinquishes our shared field, not the other manager's value.
	p, err = s.PrepareApply(f.ctx, f.target, f.intent(t, map[string]string{"managed": "new"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Execute(f.ctx, p); err != nil {
		t.Fatal(err)
	}
	got := f.read(t)
	if got.Data["foreign"] != "controller-value" || got.Data["managed"] != "new" {
		t.Fatal("shared omission deleted foreign value")
	}
}

func TestRealApplyStructuralCRDListOwnershipAndStrictValidation(t *testing.T) {
	f := newApplyFixture(t) // includes mandatory isolated-cluster authorization
	crds := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	group := f.target.Namespace + ".aster.test"
	definition := fmt.Sprintf(`{
  "apiVersion":"apiextensions.k8s.io/v1","kind":"CustomResourceDefinition",
  "metadata":{"name":"gadgets.%s"},
  "spec":{"group":"%s","scope":"Namespaced","names":{"plural":"gadgets","singular":"gadget","kind":"Gadget"},
  "versions":[{"name":"v1","served":true,"storage":true,"schema":{"openAPIV3Schema":{"type":"object","properties":{
    "spec":{"type":"object","properties":{
      "replicas":{"type":"integer","minimum":0,"maximum":10},
      "items":{"type":"array","x-kubernetes-list-type":"map","x-kubernetes-list-map-keys":["name"],
        "items":{"type":"object","required":["name","value"],"properties":{"name":{"type":"string"},"value":{"type":"string"}}}}
    }}
  }}}}]}}
 `, group, group)
	crd := &unstructured.Unstructured{}
	if err := crd.UnmarshalJSON([]byte(definition)); err != nil {
		t.Fatal(err)
	}
	created, err := f.b.CreateObject(f.ctx, crds, "", crd, metav1.CreateOptions{FieldManager: "fixture-owner"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = f.b.DeleteObject(ctx, crds, "", created.GetName(), metav1.DeleteOptions{})
	})
	err = wait.PollUntilContextTimeout(f.ctx, 100*time.Millisecond, 20*time.Second, true, func(ctx context.Context) (bool, error) {
		o, err := f.b.GetObject(ctx, crds, "", created.GetName())
		if err != nil {
			return false, err
		}
		// A newly created CRD can expose null conditions before the naming and
		// establishing controllers publish their first status. This is not
		// Established and must remain in the bounded readiness wait.
		value, found, err := unstructured.NestedFieldNoCopy(o.Object, "status", "conditions")
		if err != nil {
			return false, err
		}
		if !found || value == nil {
			return false, nil
		}
		conditions, ok := value.([]interface{})
		if !ok {
			return false, fmt.Errorf("malformed CRD status.conditions: %T", value)
		}
		for _, item := range conditions {
			m, ok := item.(map[string]interface{})
			if ok && m["type"] == "Established" && m["status"] == "True" {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	gvr := schema.GroupVersionResource{Group: group, Version: "v1", Resource: "gadgets"}
	object := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": gvr.GroupVersion().String(), "kind": "Gadget", "metadata": map[string]interface{}{"name": "list-owned", "namespace": f.target.Namespace}, "spec": map[string]interface{}{"items": []interface{}{map[string]interface{}{"name": "foreign", "value": "keep"}}}}}
	live, err := f.b.CreateObject(f.ctx, gvr, f.target.Namespace, object, metav1.CreateOptions{FieldManager: "fixture-owner"})
	if err != nil {
		t.Fatal(err)
	}
	target := f.target
	target.GVR = gvr
	target.Name = live.GetName()
	target.UID = live.GetUID()
	s := operation.NewService(f.b, target.SessionID)
	intent := func(spec map[string]interface{}) []byte {
		t.Helper()
		body, err := json.Marshal(map[string]interface{}{"apiVersion": gvr.GroupVersion().String(), "kind": "Gadget", "metadata": map[string]interface{}{"name": target.Name, "namespace": target.Namespace}, "spec": spec})
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	p, err := s.PrepareApply(f.ctx, target, intent(map[string]interface{}{"items": []interface{}{map[string]interface{}{"name": "ours", "value": "managed"}}}))
	if err != nil {
		t.Fatal(err)
	}
	before, after := p.Preview()
	review, err := manifest.OwnershipReview(before, after)
	if err != nil || !strings.Contains(review, `k:{"name":"ours"}`) {
		t.Fatalf("list ownership missing: %s %v", review, err)
	}
	result, err := s.Execute(f.ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	items, _, err := unstructured.NestedSlice(result.Object.Object, "spec", "items")
	if err != nil || len(items) != 2 {
		t.Fatalf("list map was replaced: %v %v", items, err)
	}
	values := map[string]string{}
	for _, v := range items {
		m := v.(map[string]interface{})
		values[m["name"].(string)] = m["value"].(string)
	}
	if values["foreign"] != "keep" || values["ours"] != "managed" {
		t.Fatal("foreign list entry changed")
	}
	stableRV := result.Object.GetResourceVersion()
	if _, err = s.PrepareApply(f.ctx, target, intent(map[string]interface{}{"items": []interface{}{map[string]interface{}{"name": "foreign", "value": "steal"}}})); !apierrors.IsConflict(err) {
		t.Fatalf("CRD ownership conflict not surfaced: %v", err)
	}
	for _, spec := range []map[string]interface{}{{"replicas": int64(-1)}, {"unknownField": "do-not-prune"}} {
		if p, err := s.PrepareApply(f.ctx, target, intent(spec)); p != nil || err == nil || (!apierrors.IsInvalid(err) && !apierrors.IsBadRequest(err)) {
			t.Fatalf("CRD schema error ignored: %v", err)
		}
	}
	actual, err := f.b.GetObject(f.ctx, gvr, target.Namespace, target.Name)
	if err != nil || actual.GetResourceVersion() != stableRV {
		t.Fatalf("rejected/dry-run CRD intent persisted: %v", err)
	}
	// The same precondition contract must hold on the CRD create-on-apply path.
	race := &applyRaceClient{Backend: f.b, beforePatch: func() {
		if err := f.b.DeleteObject(f.ctx, gvr, target.Namespace, target.Name, metav1.DeleteOptions{}); err != nil {
			t.Fatal(err)
		}
	}}
	guarded := operation.NewService(race, target.SessionID)
	p, err = guarded.PrepareApply(f.ctx, target, intent(map[string]interface{}{"items": []interface{}{map[string]interface{}{"name": "ours", "value": "stale"}}}))
	if err != nil {
		t.Fatal(err)
	}
	if result, err := guarded.Execute(f.ctx, p); err == nil || result.State == "Succeeded" || race.sent != 1 {
		t.Fatalf("deleted CRD apply did not fail: %+v %v", result, err)
	}
	if _, err = f.b.GetObject(f.ctx, gvr, target.Namespace, target.Name); !apierrors.IsNotFound(err) {
		t.Fatal("CRD was recreated by stale apply", err)
	}
}
