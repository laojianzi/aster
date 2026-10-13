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

	"github.com/laojianzi/aster/internal/schemaassist"
	"github.com/laojianzi/aster/internal/testcluster"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func TestRealSchemaIntegerSetAgreeWithDryRun(t *testing.T) {
	f := testcluster.NewSchemaIntegerSets(t)
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
		t.Fatal("same-identity fresh discovery did not advertise the list-set CRD")
	}
	document, err := f.Backend.SchemaDocument(ctx, f.GVK)
	if err != nil {
		t.Fatal(err)
	}
	type hint struct{ pointer, code string }
	type cause struct {
		kind  metav1.CauseType
		field string
	}
	duplicate := func(field string) []hint { return []hint{{"/spec/" + field, "list-set"}} }
	serverDuplicate := func(field string, index int) []cause {
		return []cause{{metav1.CauseTypeFieldValueDuplicate, fmt.Sprintf("spec.%s[%d]", field, index)}}
	}
	check := func(t *testing.T, ctx context.Context, doc *schemaassist.Document, draft *unstructured.Unstructured, want []hint, incomplete bool) {
		t.Helper()
		before := draft.DeepCopy()
		report, err := doc.Check(ctx, draft.Object)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(draft.Object, before.Object) {
			t.Fatal("read-only integer set inspection changed its draft")
		}
		got := make(map[hint]int)
		for _, d := range report.Diagnostics {
			got[hint{d.Pointer, d.Code}]++
			if d.Code == "list-set" && d.Message != "List entries repeat the advertised integer value." {
				t.Fatal("integer set diagnostic changed its fixed private message")
			}
		}
		wants := make(map[hint]int)
		for _, h := range want {
			wants[h]++
		}
		if !reflect.DeepEqual(got, wants) {
			t.Fatalf("published integer set/type constraints produced different hint paths/codes: got %v, want %v", got, wants)
		}
		if incomplete && !report.Partial {
			t.Fatal("an unsupported or incomplete integer set was reported as completely inspected")
		}
		for _, private := range []string{"PRIVATE_INTEGER_SET", "9007199254740993", "9223372036854775807", "987654321"} {
			if strings.Contains(report.Text(), private) {
				t.Fatal("integer set report echoed an element or default value")
			}
		}
		if !strings.Contains(report.Text(), "Server dry-run remains required") {
			t.Fatal("integer set report omitted its server admission boundary")
		}
	}
	rejected := func(t *testing.T, result *unstructured.Unstructured, err error, want []cause) {
		t.Helper()
		var status apierrors.APIStatus
		if result != nil || !apierrors.IsInvalid(err) || !errors.As(err, &status) || status.Status().Details == nil {
			t.Fatalf("expected server field validation rejection; error type %T, returned object %t", err, result != nil)
		}
		got := make(map[cause]int)
		for _, c := range status.Status().Details.Causes {
			got[cause{c.Type, c.Field}]++
		}
		wants := make(map[cause]int)
		for _, c := range want {
			wants[c]++
		}
		if !reflect.DeepEqual(got, wants) {
			// API messages can contain values; compare and print only kinds and paths.
			t.Fatalf("server rejection had different cause types/fields: got %v, want %v", got, wants)
		}
	}
	assertLiveUnchanged := func(t *testing.T, ctx context.Context, fixture testcluster.SchemaListSetsFixture) {
		t.Helper()
		after, err := fixture.Backend.GetObject(ctx, fixture.GVR, fixture.Namespace, fixture.Live.GetName())
		if err != nil || after == nil || after.GetUID() != fixture.Live.GetUID() || after.GetResourceVersion() != fixture.Live.GetResourceVersion() || after.GroupVersionKind() != fixture.GVK || after.GetNamespace() != fixture.Namespace || after.GetName() != fixture.Live.GetName() || !reflect.DeepEqual(after.Object["spec"], fixture.Live.Object["spec"]) || !reflect.DeepEqual(after.GetLabels(), fixture.Live.GetLabels()) {
			t.Fatalf("integer set inspection or dry-run changed live identity, labels, or spec; error type %T", err)
		}
	}
	spec := func(d *unstructured.Unstructured) map[string]any { return d.Object["spec"].(map[string]any) }
	set := func(field string, value any) func(*unstructured.Unstructured) {
		return func(d *unstructured.Unstructured) { spec(d)[field] = value }
	}
	// Fresh creates avoid old-object validation exemptions. Integral float64
	// values remain incomplete locally even if transport emits integer tokens.
	many := make([]any, schemaassist.MaxListSetItems)
	for i := range many {
		many[i] = int64(i)
	}
	manyDuplicate := append([]any{}, many...)
	manyDuplicate[len(manyDuplicate)-1] = int64(0)
	for index, tc := range []struct {
		name       string
		mutate     func(*unstructured.Unstructured)
		local      []hint
		server     []cause
		incomplete bool
		serverSpec func(*unstructured.Unstructured)
	}{
		{name: "valid-integer-sets"},
		{name: "empty", mutate: set("values", []any{})},
		{name: "singleton-zero", mutate: set("values", []any{int64(0)})},
		{name: "zero-duplicate", mutate: set("values", []any{int64(0), int64(0)}), local: duplicate("values"), server: serverDuplicate("values", 1)},
		{name: "signed-endpoints", mutate: set("values", []any{int64(-9223372036854775808), int64(9223372036854775807)})},
		{name: "minimum-duplicate", mutate: set("values", []any{int64(-9223372036854775808), int64(-9223372036854775808)}), local: duplicate("values"), server: serverDuplicate("values", 1)},
		{name: "maximum-duplicate", mutate: set("values", []any{int64(9223372036854775807), int64(9223372036854775807)}), local: duplicate("values"), server: serverDuplicate("values", 1)},
		{name: "adjacent-above-2pow53", mutate: set("values", []any{int64(9007199254740992), int64(9007199254740993)})},
		{name: "late-large-duplicate", mutate: set("values", []any{int64(9007199254740992), int64(9007199254740993), int64(9007199254740993)}), local: duplicate("values"), server: serverDuplicate("values", 2)},
		{name: "negative-adjacent", mutate: set("values", []any{int64(-9007199254740992), int64(-9007199254740993)})},
		{name: "triplicate-one-parent-hint", mutate: set("values", []any{int64(7), int64(7), int64(7)}), local: duplicate("values"), server: serverDuplicate("values", 1)},
		{name: "two-duplicate-groups", mutate: set("values", []any{int64(7), int64(7), int64(8), int64(8)}), local: duplicate("values"), server: []cause{{metav1.CauseTypeFieldValueDuplicate, "spec.values[1]"}, {metav1.CauseTypeFieldValueDuplicate, "spec.values[3]"}}},
		{name: "256-unique", mutate: set("values", many)},
		{name: "256-last-duplicate", mutate: set("values", manyDuplicate), local: duplicate("values"), server: serverDuplicate("values", 255)},
		{name: "integral-float-wire-integer", mutate: set("values", []any{float64(8080)}), incomplete: true, serverSpec: set("values", []any{int64(8080)})},
		{name: "integral-float-wire-collision", mutate: set("values", []any{float64(7), int64(7)}), incomplete: true, server: serverDuplicate("values", 1)},
		{name: "fraction-is-not-an-integer", mutate: set("values", []any{float64(7.5)}), local: []hint{{"/spec/values/0", "type"}}, incomplete: true, server: []cause{{metav1.CauseTypeTypeInvalid, "spec.values[0]"}}},
		{name: "quoted-integer", mutate: set("values", []any{"PRIVATE_INTEGER_SET"}), local: []hint{{"/spec/values/0", "type"}}, incomplete: true, server: []cause{{metav1.CauseTypeTypeInvalid, "spec.values[0]"}}},
		{name: "late-invalid-suppresses-hint", mutate: set("values", []any{int64(7), int64(7), false}), local: []hint{{"/spec/values/2", "type"}}, incomplete: true, server: []cause{{metav1.CauseTypeTypeInvalid, "spec.values[2]"}, {metav1.CauseTypeFieldValueDuplicate, "spec.values[1]"}}},
		{name: "parent-null-pruning", mutate: set("values", nil), local: []hint{{"/spec/values", "type"}}, server: []cause{{metav1.CauseTypeFieldValueRequired, "spec.values"}}},
		{name: "null-item-not-pruned", mutate: set("values", []any{nil}), local: []hint{{"/spec/values/0", "type"}}, incomplete: true, server: []cause{{metav1.CauseTypeTypeInvalid, "spec.values[0]"}}},
		{name: "defaulted-null-transformation", mutate: set("defaulted", []any{nil}), local: []hint{{"/spec/defaulted/0", "type"}}, incomplete: true, serverSpec: set("defaulted", []any{int64(42)})},
		{name: "defaulted-null-collision", mutate: set("defaulted", []any{nil, int64(42)}), local: []hint{{"/spec/defaulted/0", "type"}}, incomplete: true, server: serverDuplicate("defaulted", 1)},
		{name: "explicit-default-not-rewritten", mutate: set("defaulted", []any{int64(0), int64(42)})},
		{name: "defaulted-duplicate", mutate: set("defaulted", []any{int64(42), int64(42)}), local: duplicate("defaulted"), server: serverDuplicate("defaulted", 1)},
		{name: "nullable-parent-unique", mutate: set("nullable", []any{int64(1), int64(2)})},
		{name: "nullable-parent-duplicate", mutate: set("nullable", []any{int64(1), int64(1)}), local: duplicate("nullable"), server: serverDuplicate("nullable", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft := f.Draft(fmt.Sprintf("schema-list-set-%02d", index+1))
			if tc.mutate != nil {
				tc.mutate(draft)
			}
			check(t, ctx, document, draft, tc.local, tc.incomplete)
			before := draft.DeepCopy()
			result, err := f.Backend.CreateObject(ctx, f.GVR, f.Namespace, draft, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "schema-list-set-dry-run", FieldValidation: "Strict"})
			if !reflect.DeepEqual(draft.Object, before.Object) {
				t.Fatal("dry-run create changed its input document")
			}
			if len(tc.server) > 0 {
				rejected(t, result, err, tc.server)
			} else {
				expected := before.DeepCopy()
				if tc.serverSpec != nil {
					tc.serverSpec(expected)
				}
				if err != nil || result == nil || result.GetName() != draft.GetName() || result.GetNamespace() != f.Namespace || result.GroupVersionKind() != f.GVK || !reflect.DeepEqual(result.Object["spec"], expected.Object["spec"]) || !reflect.DeepEqual(result.GetLabels(), expected.GetLabels()) {
					t.Fatalf("server did not return the complete expected integer set draft; error type %T", err)
				}
			}
			if _, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, draft.GetName()); !apierrors.IsNotFound(err) {
				t.Fatalf("checked/dry-run integer set draft persisted or absence was not proven; error type %T", err)
			}
			assertLiveUnchanged(t, ctx, f)
		})
	}
	for _, tc := range []struct {
		name, field string
		value       any
		local       []hint
		server      []cause
		incomplete  bool
		serverSpec  func(*unstructured.Unstructured)
	}{
		{name: "live-values-valid-dry-run", field: "values", value: []any{int64(0), int64(9007199254740993), int64(9223372036854775807)}},
		{name: "live-values-duplicate-dry-run", field: "values", value: []any{int64(7), int64(7)}, local: duplicate("values"), server: serverDuplicate("values", 1)},
		{name: "live-defaulted-explicit-valid-dry-run", field: "defaulted", value: []any{int64(8)}},
		{name: "live-defaulted-null-server-default-boundary", field: "defaulted", value: []any{nil}, local: []hint{{"/spec/defaulted/0", "type"}}, incomplete: true, serverSpec: set("defaulted", []any{int64(42)})},
		{name: "live-nullable-parent-duplicate-dry-run", field: "nullable", value: []any{int64(9), int64(9)}, local: duplicate("nullable"), server: serverDuplicate("nullable", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft := f.Live.DeepCopy()
			spec(draft)[tc.field] = tc.value
			check(t, ctx, document, draft, tc.local, tc.incomplete)
			before := draft.DeepCopy()
			body, err := json.Marshal([]map[string]any{
				{"op": "test", "path": "/metadata/uid", "value": f.Live.GetUID()},
				{"op": "test", "path": "/metadata/resourceVersion", "value": f.Live.GetResourceVersion()},
				{"op": "replace", "path": "/spec/" + tc.field, "value": spec(draft)[tc.field]},
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := f.Backend.PatchObject(ctx, f.GVR, f.Namespace, f.Live.GetName(), types.JSONPatchType, body, metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "schema-list-set-dry-run", FieldValidation: "Strict"})
			if !reflect.DeepEqual(draft.Object, before.Object) {
				t.Fatal("live integer set dry-run changed its input draft")
			}
			if len(tc.server) > 0 {
				rejected(t, result, err, tc.server)
			} else {
				expected := before.DeepCopy()
				if tc.serverSpec != nil {
					tc.serverSpec(expected)
				}
				if err != nil || result == nil || result.GetUID() != f.Live.GetUID() || result.GetResourceVersion() == "" || result.GetName() != f.Live.GetName() || result.GetNamespace() != f.Namespace || result.GroupVersionKind() != f.GVK || !reflect.DeepEqual(result.Object["spec"], expected.Object["spec"]) || !reflect.DeepEqual(result.GetLabels(), expected.GetLabels()) {
					t.Fatalf("live integer set dry-run did not preview the same UID and complete expected spec; error type %T", err)
				}
			}
			assertLiveUnchanged(t, ctx, f)
		})
	}
	// This separate fixture proves that both the published schema and actual
	// fresh admission observe the atomic-to-set change before testing old data.
	legacy := testcluster.NewSchemaIntegerSetsWithLegacyDuplicates(t)
	legacyCtx, legacyCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer legacyCancel()
	legacyDoc, err := legacy.Backend.SchemaDocument(legacyCtx, legacy.GVK)
	if err != nil {
		t.Fatal(err)
	}
	for index, tc := range []struct {
		name, duplicateField string
		mutate               func(*unstructured.Unstructured)
	}{
		{name: "legacy-metadata-only-old-duplicate-accepted", duplicateField: "values"},
		{name: "legacy-changed-duplicate-values-accepted", duplicateField: "values", mutate: set("values", []any{int64(8), int64(8), int64(8)})},
		{name: "legacy-new-duplicate-other-set-accepted", duplicateField: "defaulted", mutate: func(d *unstructured.Unstructured) {
			spec(d)["values"] = []any{int64(1), int64(2)}
			spec(d)["defaulted"] = []any{int64(3), int64(3)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft := legacy.Live.DeepCopy()
			draft.SetLabels(map[string]string{"aster-list-set-review": "dry-run"})
			if tc.mutate != nil {
				tc.mutate(draft)
			}
			check(t, legacyCtx, legacyDoc, draft, duplicate(tc.duplicateField), false)
			before := draft.DeepCopy()
			patch := []map[string]any{
				{"op": "test", "path": "/metadata/uid", "value": legacy.Live.GetUID()},
				{"op": "test", "path": "/metadata/resourceVersion", "value": legacy.Live.GetResourceVersion()},
				{"op": "add", "path": "/metadata/labels", "value": draft.GetLabels()},
			}
			if tc.mutate != nil {
				patch = append(patch, map[string]any{"op": "replace", "path": "/spec", "value": spec(draft)})
			}
			body, err := json.Marshal(patch)
			if err != nil {
				t.Fatal(err)
			}
			result, err := legacy.Backend.PatchObject(legacyCtx, legacy.GVR, legacy.Namespace, legacy.Live.GetName(), types.JSONPatchType, body, metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "schema-list-set-dry-run", FieldValidation: "Strict"})
			if !reflect.DeepEqual(draft.Object, before.Object) {
				t.Fatal("legacy dry-run changed its input draft")
			}
			if err != nil || result == nil || result.GetUID() != legacy.Live.GetUID() || result.GetResourceVersion() == "" || result.GroupVersionKind() != legacy.GVK || result.GetNamespace() != legacy.Namespace || result.GetName() != legacy.Live.GetName() || !reflect.DeepEqual(result.Object["spec"], draft.Object["spec"]) || !reflect.DeepEqual(result.GetLabels(), draft.GetLabels()) {
				t.Fatalf("server did not accept the exact identity-bound legacy dry-run; error type %T", err)
			}
			assertLiveUnchanged(t, legacyCtx, legacy)
			fresh := legacy.Draft(fmt.Sprintf("schema-list-set-legacy-create-%d", index+1))
			fresh.Object["spec"] = draft.DeepCopy().Object["spec"]
			fresh.SetLabels(draft.GetLabels())
			check(t, legacyCtx, legacyDoc, fresh, duplicate(tc.duplicateField), false)
			freshBefore := fresh.DeepCopy()
			result, err = legacy.Backend.CreateObject(legacyCtx, legacy.GVR, legacy.Namespace, fresh, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "schema-list-set-dry-run", FieldValidation: "Strict"})
			if !reflect.DeepEqual(fresh.Object, freshBefore.Object) {
				t.Fatal("legacy-boundary fresh dry-run changed its input draft")
			}
			rejected(t, result, err, serverDuplicate(tc.duplicateField, 1))
			if _, err := legacy.Backend.GetObject(legacyCtx, legacy.GVR, legacy.Namespace, fresh.GetName()); !apierrors.IsNotFound(err) {
				t.Fatalf("legacy-boundary fresh dry-run persisted or absence was not proven; error type %T", err)
			}
			assertLiveUnchanged(t, legacyCtx, legacy)
			t.Log("list-set server boundary: old invalid list dry-run update accepted; corresponding fresh duplicate create rejected; live UID/resourceVersion/spec unchanged and fresh absence verified")
		})
	}
	assertLiveUnchanged(t, ctx, f)
}
