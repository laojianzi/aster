//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/testcluster"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func TestRealSchemaEnumAgreeWithDryRun(t *testing.T) {
	f := testcluster.NewSchemaEnums(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	kinds, _, err := f.Backend.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, kind := range kinds {
		if kind.GVR == f.GVR && kind.Kind == f.GVK.Kind && kind.Namespaced {
			found = true
		}
	}
	if !found {
		t.Fatal("same-identity fresh discovery did not advertise the enum CRD")
	}
	document, err := f.Backend.SchemaDocument(ctx, f.GVK)
	if err != nil {
		t.Fatal(err)
	}
	spec := func(draft *unstructured.Unstructured) map[string]any { return draft.Object["spec"].(map[string]any) }
	object := func(draft *unstructured.Unstructured) map[string]any {
		return spec(draft)["objectChoice"].(map[string]any)
	}
	array := func(draft *unstructured.Unstructured) []any { return spec(draft)["arrayChoice"].([]any) }
	check := func(t *testing.T, draft *unstructured.Unstructured, field, code string) {
		t.Helper()
		before := draft.DeepCopy()
		report, err := document.Check(ctx, draft.Object)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(draft.Object, before.Object) {
			t.Fatal("read-only structured enum inspection changed its draft")
		}
		// Unrelated published metadata can remain incomplete. Each mismatch
		// below must nevertheless inspect the selected actual enum, and these
		// comparisons do not assert that local hints replace server admission.
		if code == "" {
			if len(report.Diagnostics) != 0 {
				t.Fatalf("matching decoded enum produced a local hint: %s", report.Text())
			}
		} else if len(report.Diagnostics) != 1 || report.Diagnostics[0].Pointer != "/spec/"+field || report.Diagnostics[0].Code != code {
			t.Fatalf("published structured enum/type was not inspected at its actual field: %s", report.Text())
		}
		for _, private := range []string{"PRIVATE_ENUM_DRAFT", "9007199254740993", "9007199254740992", "中文船🚢", "结束船", "alpha", "beta"} {
			if strings.Contains(report.Text(), private) {
				t.Fatal("structured enum report echoed a draft value or advertised choice")
			}
		}
		if !strings.Contains(report.Text(), "Server dry-run remains required") {
			t.Fatal("structured enum report omitted its server admission boundary")
		}
	}
	rejected := func(t *testing.T, result *unstructured.Unstructured, err error, field, code string) {
		t.Helper()
		var status apierrors.APIStatus
		if !apierrors.IsInvalid(err) || !errors.As(err, &status) || status.Status().Details == nil || result != nil {
			t.Fatalf("expected server field validation rejection, got %v", err)
		}
		want := metav1.CauseTypeFieldValueNotSupported
		if code == "type" {
			want = metav1.CauseTypeTypeInvalid
		}
		causes := make([]string, 0, len(status.Status().Details.Causes))
		for _, cause := range status.Status().Details.Causes {
			causes = append(causes, string(cause.Type)+" at "+cause.Field)
			if cause.Field == "spec."+field && cause.Type == want {
				return
			}
		}
		t.Fatalf("server rejection did not identify %s at spec.%s; received cause types and fields: %v", want, field, causes)
	}
	assertLiveUnchanged := func(t *testing.T) {
		t.Helper()
		after, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, f.Live.GetName())
		if err != nil || after == nil || after.GetUID() != f.Live.GetUID() || after.GetResourceVersion() != f.Live.GetResourceVersion() || after.GroupVersionKind() != f.GVK || after.GetNamespace() != f.Namespace || after.GetName() != f.Live.GetName() || !reflect.DeepEqual(after.Object["spec"], f.Live.Object["spec"]) {
			t.Fatalf("structured enum inspection or dry-run changed live identity or spec: %v", err)
		}
	}
	// Primary rejection cases are fresh creates; an unchanged invalid old
	// value cannot qualify through server validation ratcheting. All fields
	// are declared, with no defaults or unknown-field preservation.
	nextName := 0
	for _, tc := range []struct {
		name, field, localCode, serverCode string
		mutate                             func(*unstructured.Unstructured)
	}{
		{"valid-defaults", "", "", "", nil},
		{"object-key-order-independent", "objectChoice", "", "", func(d *unstructured.Unstructured) {
			before := object(d)
			rebuilt := map[string]any{}
			for _, key := range []string{"nested", "note", "ratio", "count", "enabled", "label"} {
				rebuilt[key] = before[key]
			}
			spec(d)["objectChoice"] = rebuilt
		}},
		{"object-valid-second-choice", "objectChoice", "", "", func(d *unstructured.Unstructured) { object(d)["label"] = "beta" }},
		{"object-nested-scalar-mismatch", "objectChoice", "enum", "enum", func(d *unstructured.Unstructured) { object(d)["label"] = "PRIVATE_ENUM_DRAFT_LABEL" }},
		{"object-nested-array-order", "objectChoice", "enum", "enum", func(d *unstructured.Unstructured) {
			object(d)["nested"].(map[string]any)["steps"] = []any{"second", "first", "first"}
		}},
		{"object-missing-nullable-member", "objectChoice", "enum", "enum", func(d *unstructured.Unstructured) {
			delete(object(d)["nested"].(map[string]any), "optional")
		}},
		{"object-nullable-member-mismatch", "objectChoice", "enum", "enum", func(d *unstructured.Unstructured) {
			object(d)["nested"].(map[string]any)["optional"] = "PRIVATE_ENUM_DRAFT_OPTIONAL"
		}},
		{"object-extra-declared-member", "objectChoice", "enum", "enum", func(d *unstructured.Unstructured) { object(d)["extra"] = "PRIVATE_ENUM_DRAFT_EXTRA" }},
		{"object-adjacent-int64-above-2pow53", "objectChoice", "enum", "enum", func(d *unstructured.Unstructured) { object(d)["count"] = int64(9007199254740992) }},
		{"object-half-fraction-mismatch", "objectChoice", "enum", "enum", func(d *unstructured.Unstructured) { object(d)["ratio"] = float64(1.5) }},
		{"object-boolean-mismatch", "objectChoice", "enum", "enum", func(d *unstructured.Unstructured) { object(d)["enabled"] = false }},
		{"object-unicode-mismatch", "objectChoice", "enum", "enum", func(d *unstructured.Unstructured) { object(d)["note"] = "中文船" }},
		{"object-empty-mismatch", "objectChoice", "enum", "enum", func(d *unstructured.Unstructured) { spec(d)["objectChoice"] = map[string]any{} }},
		{"array-valid-second-choice", "arrayChoice", "", "", func(d *unstructured.Unstructured) {
			a := array(d)
			spec(d)["arrayChoice"] = []any{a[1], a[0]}
		}},
		{"array-order", "arrayChoice", "enum", "enum", func(d *unstructured.Unstructured) {
			a := array(d)
			spec(d)["arrayChoice"] = []any{a[1], a[0], a[2]}
		}},
		{"array-duplicate-cardinality", "arrayChoice", "enum", "enum", func(d *unstructured.Unstructured) {
			a := array(d)
			spec(d)["arrayChoice"] = []any{a[0], a[1]}
		}},
		{"array-length-extra", "arrayChoice", "enum", "enum", func(d *unstructured.Unstructured) {
			a := array(d)
			spec(d)["arrayChoice"] = []any{a[0], a[1], a[2], a[1]}
		}},
		{"array-nested-integer-mismatch", "arrayChoice", "enum", "enum", func(d *unstructured.Unstructured) {
			array(d)[0].(map[string]any)["count"] = int64(9007199254740992)
		}},
		{"array-missing-nullable-member", "arrayChoice", "enum", "enum", func(d *unstructured.Unstructured) { delete(array(d)[0].(map[string]any), "optional") }},
		{"array-member-key-order-independent", "arrayChoice", "", "", func(d *unstructured.Unstructured) {
			a := array(d)
			before := a[0].(map[string]any)
			rebuilt := map[string]any{}
			for _, key := range []string{"optional", "ratio", "count", "enabled", "code"} {
				rebuilt[key] = before[key]
			}
			a[0] = rebuilt
		}},
		{"array-empty-mismatch", "arrayChoice", "enum", "enum", func(d *unstructured.Unstructured) { spec(d)["arrayChoice"] = []any{} }},
		{"empty-object-extra-property", "emptyObject", "enum", "enum", func(d *unstructured.Unstructured) {
			spec(d)["emptyObject"] = map[string]any{"token": "PRIVATE_ENUM_DRAFT_TOKEN"}
		}},
		{"empty-array-extra-item", "emptyArray", "enum", "enum", func(d *unstructured.Unstructured) { spec(d)["emptyArray"] = []any{"PRIVATE_ENUM_DRAFT_ITEM"} }},
		{"object-instead-of-array", "emptyArray", "type", "type", func(d *unstructured.Unstructured) { spec(d)["emptyArray"] = map[string]any{} }},
		{"array-instead-of-object", "emptyObject", "type", "type", func(d *unstructured.Unstructured) { spec(d)["emptyObject"] = []any{} }},
		{"nullable-object-mismatch", "nullableObject", "enum", "enum", func(d *unstructured.Unstructured) {
			spec(d)["nullableObject"] = map[string]any{"mode": "PRIVATE_ENUM_DRAFT_MODE"}
		}},
		{"nullable-object-empty-mismatch", "nullableObject", "enum", "enum", func(d *unstructured.Unstructured) { spec(d)["nullableObject"] = map[string]any{} }},
		// The local enum includes null. Kubernetes skips a nil enum candidate
		// in its common enum validator and rejects this root value anyway.
		// Nested nullable children above have no enum of their own and are
		// deliberately kept in the ordinary matching object/array cases.
		{"nullable-root-null-server-boundary", "nullableObject", "", "enum", func(d *unstructured.Unstructured) { spec(d)["nullableObject"] = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nextName++
			draft := f.Draft(fmt.Sprintf("schema-enum-%02d", nextName))
			if tc.mutate != nil {
				tc.mutate(draft)
			}
			check(t, draft, tc.field, tc.localCode)
			before := draft.DeepCopy()
			result, err := f.Backend.CreateObject(ctx, f.GVR, f.Namespace, draft, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "schema-enum-dry-run", FieldValidation: "Strict"})
			if !reflect.DeepEqual(draft.Object, before.Object) {
				t.Fatal("dry-run create changed its input document")
			}
			if tc.serverCode == "" {
				if err != nil || result == nil || result.GetName() != draft.GetName() || result.GetNamespace() != f.Namespace || result.GroupVersionKind() != f.GVK || !reflect.DeepEqual(result.Object["spec"], draft.Object["spec"]) {
					t.Fatalf("server did not preserve the matching structured enum draft: %v", err)
				}
			} else {
				rejected(t, result, err, tc.field, tc.serverCode)
			}
			if _, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, draft.GetName()); !apierrors.IsNotFound(err) {
				t.Fatalf("checked/dry-run enum draft unexpectedly persisted: %v", err)
			}
			assertLiveUnchanged(t)
		})
	}
	for _, tc := range []struct {
		name, field, code string
		mutate            func(*unstructured.Unstructured)
	}{
		{"live-object-valid-dry-run", "objectChoice", "", func(d *unstructured.Unstructured) { object(d)["label"] = "beta" }},
		{"live-object-invalid-dry-run", "objectChoice", "enum", func(d *unstructured.Unstructured) { object(d)["label"] = "PRIVATE_ENUM_DRAFT_LIVE" }},
		{"live-array-valid-dry-run", "arrayChoice", "", func(d *unstructured.Unstructured) {
			a := array(d)
			spec(d)["arrayChoice"] = []any{a[1], a[0]}
		}},
		{"live-array-invalid-dry-run", "arrayChoice", "enum", func(d *unstructured.Unstructured) {
			a := array(d)
			spec(d)["arrayChoice"] = []any{a[0], a[1]}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft := f.Live.DeepCopy()
			tc.mutate(draft)
			check(t, draft, tc.field, tc.code)
			// Keep the object patch at its changed leaf. The server's merge
			// implementation also prunes null members inside replacement arrays;
			// use the editor's JSON Patch approach for an identity-bound array
			// replacement so the intended nullable child remains present.
			patchType := types.MergePatchType
			var patch any
			if tc.field == "objectChoice" {
				patch = map[string]any{"spec": map[string]any{tc.field: map[string]any{"label": object(draft)["label"]}}}
			} else {
				patchType = types.JSONPatchType
				patch = []map[string]any{
					{"op": "test", "path": "/metadata/uid", "value": f.Live.GetUID()},
					{"op": "test", "path": "/metadata/resourceVersion", "value": f.Live.GetResourceVersion()},
					{"op": "replace", "path": "/spec/" + tc.field, "value": spec(draft)[tc.field]},
				}
			}
			body, err := json.Marshal(patch)
			if err != nil {
				t.Fatal(err)
			}
			result, err := f.Backend.PatchObject(ctx, f.GVR, f.Namespace, f.Live.GetName(), patchType, body, metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "schema-enum-dry-run", FieldValidation: "Strict"})
			if tc.code != "" {
				rejected(t, result, err, tc.field, tc.code)
			} else if err != nil || result == nil || result.GetUID() != f.Live.GetUID() || result.GetName() != f.Live.GetName() || result.GetNamespace() != f.Namespace || result.GroupVersionKind() != f.GVK || !reflect.DeepEqual(result.Object["spec"], draft.Object["spec"]) {
				t.Fatalf("live enum dry-run did not preview the same UID and complete intended spec: %v", err)
			}
			assertLiveUnchanged(t)
		})
	}
	assertLiveUnchanged(t)
}
