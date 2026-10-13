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

func TestRealSchemaListSetAgreeWithDryRun(t *testing.T) {
	f := testcluster.NewSchemaListSets(t)
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
			t.Fatal("read-only string set inspection changed its draft")
		}
		got := make(map[hint]int)
		for _, d := range report.Diagnostics {
			got[hint{d.Pointer, d.Code}]++
			if d.Code == "list-set" && d.Message != "List entries repeat the advertised string value." {
				t.Fatal("string set diagnostic changed its fixed private message")
			}
		}
		wants := make(map[hint]int)
		for _, h := range want {
			wants[h]++
		}
		if !reflect.DeepEqual(got, wants) {
			t.Fatalf("published string set/type constraints produced different hint paths/codes: got %v, want %v", got, wants)
		}
		if incomplete && !report.Partial {
			t.Fatal("an unsupported or incomplete string set was reported as completely inspected")
		}
		for _, private := range []string{"PRIVATE_LIST_SET", "中文船🚢", "fallback", "e\u0301", "界界界"} {
			if strings.Contains(report.Text(), private) {
				t.Fatal("string set report echoed an element or default value")
			}
		}
		if !strings.Contains(report.Text(), "Server dry-run remains required") {
			t.Fatal("string set report omitted its server admission boundary")
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
			t.Fatalf("string set inspection or dry-run changed live identity, labels, or spec; error type %T", err)
		}
	}
	spec := func(d *unstructured.Unstructured) map[string]any { return d.Object["spec"].(map[string]any) }
	set := func(field string, value any) func(*unstructured.Unstructured) {
		return func(d *unstructured.Unstructured) { spec(d)[field] = value }
	}
	bounded := strings.Repeat("界", schemaassist.MaxListSetValueBytes/3) + strings.Repeat("x", schemaassist.MaxListSetValueBytes%3)
	// The primary comparisons use fresh creates to avoid old-object validation
	// exemptions. Named default/pruning and budget cases deliberately distinguish
	// exact decoded draft assistance from the server's admission transformations.
	for index, tc := range []struct {
		name       string
		mutate     func(*unstructured.Unstructured)
		local      []hint
		server     []cause
		incomplete bool
		serverSpec func(*unstructured.Unstructured)
	}{
		{name: "valid-required-string-sets"},
		{name: "values-empty-list", mutate: set("values", []any{})},
		{name: "values-empty-string-singleton", mutate: set("values", []any{""})},
		{name: "values-empty-string-duplicate", mutate: set("values", []any{"", ""}), local: duplicate("values"), server: serverDuplicate("values", 1)},
		{name: "values-exact-private-string-duplicate", mutate: set("values", []any{"PRIVATE_LIST_SET_ID", "PRIVATE_LIST_SET_ID"}), local: duplicate("values"), server: serverDuplicate("values", 1)},
		{name: "values-triplicate-has-one-server-duplicate-location", mutate: set("values", []any{"same", "same", "same"}), local: duplicate("values"), server: serverDuplicate("values", 1)},
		{name: "values-interleaved-duplicate", mutate: set("values", []any{"a", "b", "a"}), local: duplicate("values"), server: serverDuplicate("values", 2)},
		{name: "values-two-duplicate-groups-one-local-parent-hint", mutate: set("values", []any{"a", "a", "b", "b", "b"}), local: duplicate("values"), server: []cause{{metav1.CauseTypeFieldValueDuplicate, "spec.values[1]"}, {metav1.CauseTypeFieldValueDuplicate, "spec.values[3]"}}},
		{name: "values-case-is-not-folded", mutate: set("values", []any{"api", "API"})},
		{name: "values-whitespace-is-not-trimmed", mutate: set("values", []any{"api", " api", "api ", "api\t"})},
		{name: "values-unicode-is-not-normalized", mutate: set("values", []any{"é", "e\u0301"})},
		{name: "values-unicode-exact-duplicate", mutate: set("values", []any{"中文船🚢", "中文船🚢"}), local: duplicate("values"), server: serverDuplicate("values", 1)},
		{name: "values-embedded-nul-remains-distinct", mutate: set("values", []any{"a\x00b", "ab"})},
		{name: "values-embedded-nul-exact-duplicate", mutate: set("values", []any{"a\x00b", "a\x00b"}), local: duplicate("values"), server: serverDuplicate("values", 1)},
		{name: "values-newline-and-backslash-remain-distinct", mutate: set("values", []any{"a\nb", "a\\nb"})},
		{name: "values-newline-exact-duplicate", mutate: set("values", []any{"a\nb", "a\nb"}), local: duplicate("values"), server: serverDuplicate("values", 1)},
		{name: "values-numeric-looking-strings-are-not-numbers", mutate: set("values", []any{"1", "1.0", "01", "1e0"})},
		{name: "values-256-unique-strings", mutate: func(d *unstructured.Unstructured) {
			items := make([]any, schemaassist.MaxListSetItems)
			for i := range items {
				items[i] = fmt.Sprintf("entry-%03d", i)
			}
			spec(d)["values"] = items
		}},
		{name: "values-256-last-string-duplicates", mutate: func(d *unstructured.Unstructured) {
			items := make([]any, schemaassist.MaxListSetItems)
			for i := range items {
				items[i] = fmt.Sprintf("entry-%03d", i)
			}
			items[len(items)-1] = "entry-000"
			spec(d)["values"] = items
		}, local: duplicate("values"), server: serverDuplicate("values", 255)},
		{name: "values-16kib-string-duplicate", mutate: set("values", []any{bounded, bounded}), local: duplicate("values"), server: serverDuplicate("values", 1)},
		{name: "values-oversized-string-server-accepts-local-incomplete", mutate: set("values", []any{bounded + "x"}), incomplete: true},
		{name: "values-late-oversized-string-suppresses-duplicate-hint", mutate: set("values", []any{"same", "same", bounded + "x"}), server: serverDuplicate("values", 1), incomplete: true},
		{name: "values-object-instead-of-array", mutate: set("values", map[string]any{}), local: []hint{{"/spec/values", "type"}}, server: []cause{{metav1.CauseTypeTypeInvalid, "spec.values"}}},
		{name: "values-parent-null-server-pruning-boundary", mutate: set("values", nil), local: []hint{{"/spec/values", "type"}}, server: []cause{{metav1.CauseTypeFieldValueRequired, "spec.values"}}},
		{name: "values-number-item-stays-incomplete", mutate: set("values", []any{int64(7)}), local: []hint{{"/spec/values/0", "type"}}, server: []cause{{metav1.CauseTypeTypeInvalid, "spec.values[0]"}}, incomplete: true},
		{name: "values-boolean-item-stays-incomplete", mutate: set("values", []any{false}), local: []hint{{"/spec/values/0", "type"}}, server: []cause{{metav1.CauseTypeTypeInvalid, "spec.values[0]"}}, incomplete: true},
		{name: "values-object-item-stays-incomplete", mutate: set("values", []any{map[string]any{}}), local: []hint{{"/spec/values/0", "type"}}, server: []cause{{metav1.CauseTypeTypeInvalid, "spec.values[0]"}}, incomplete: true},
		{name: "values-array-item-stays-incomplete", mutate: set("values", []any{[]any{}}), local: []hint{{"/spec/values/0", "type"}}, server: []cause{{metav1.CauseTypeTypeInvalid, "spec.values[0]"}}, incomplete: true},
		{name: "values-null-item-is-not-pruned", mutate: set("values", []any{nil}), local: []hint{{"/spec/values/0", "type"}}, server: []cause{{metav1.CauseTypeTypeInvalid, "spec.values[0]"}}, incomplete: true},
		{name: "values-null-pair-type-and-server-duplicate", mutate: set("values", []any{nil, nil}), local: []hint{{"/spec/values/0", "type"}, {"/spec/values/1", "type"}}, server: []cause{{metav1.CauseTypeTypeInvalid, "spec.values[0]"}, {metav1.CauseTypeTypeInvalid, "spec.values[1]"}, {metav1.CauseTypeFieldValueDuplicate, "spec.values[1]"}}, incomplete: true},
		{name: "values-late-invalid-item-suppresses-duplicate-hint", mutate: set("values", []any{"same", "same", false}), local: []hint{{"/spec/values/2", "type"}}, server: []cause{{metav1.CauseTypeTypeInvalid, "spec.values[2]"}, {metav1.CauseTypeFieldValueDuplicate, "spec.values[1]"}}, incomplete: true},
		{name: "defaulted-null-item-server-default-boundary", mutate: set("defaulted", []any{nil}), local: []hint{{"/spec/defaulted/0", "type"}}, incomplete: true, serverSpec: set("defaulted", []any{"fallback"})},
		{name: "defaulted-null-and-explicit-server-collision", mutate: set("defaulted", []any{nil, "fallback"}), local: []hint{{"/spec/defaulted/0", "type"}}, server: serverDuplicate("defaulted", 1), incomplete: true},
		{name: "defaulted-explicit-unique-strings", mutate: set("defaulted", []any{"left", "right"})},
		{name: "defaulted-explicit-duplicate-strings", mutate: set("defaulted", []any{"same", "same"}), local: duplicate("defaulted"), server: serverDuplicate("defaulted", 1)},
		{name: "defaulted-null-pair-server-collision", mutate: set("defaulted", []any{nil, nil}), local: []hint{{"/spec/defaulted/0", "type"}, {"/spec/defaulted/1", "type"}}, server: serverDuplicate("defaulted", 1), incomplete: true},
		{name: "nullable-parent-array-unique-strings", mutate: set("nullable", []any{"one", "two"})},
		{name: "nullable-parent-array-duplicate-strings", mutate: set("nullable", []any{"same", "same"}), local: duplicate("nullable"), server: serverDuplicate("nullable", 1)},
		{name: "nullable-parent-does-not-make-items-nullable", mutate: set("nullable", []any{nil}), local: []hint{{"/spec/nullable/0", "type"}}, server: []cause{{metav1.CauseTypeTypeInvalid, "spec.nullable[0]"}}, incomplete: true},
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
					t.Fatalf("server did not return the complete expected string set draft; error type %T", err)
				}
			}
			if _, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, draft.GetName()); !apierrors.IsNotFound(err) {
				t.Fatalf("checked/dry-run string set draft persisted or absence was not proven; error type %T", err)
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
		{name: "live-values-valid-dry-run", field: "values", value: []any{"api", "gateway", ""}},
		{name: "live-values-duplicate-dry-run", field: "values", value: []any{"api", "api"}, local: duplicate("values"), server: serverDuplicate("values", 1)},
		{name: "live-defaulted-explicit-valid-dry-run", field: "defaulted", value: []any{"changed"}},
		{name: "live-defaulted-null-server-default-boundary", field: "defaulted", value: []any{nil}, local: []hint{{"/spec/defaulted/0", "type"}}, incomplete: true, serverSpec: set("defaulted", []any{"fallback"})},
		{name: "live-nullable-parent-duplicate-dry-run", field: "nullable", value: []any{"same", "same"}, local: duplicate("nullable"), server: serverDuplicate("nullable", 1)},
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
				t.Fatal("live string set dry-run changed its input draft")
			}
			if len(tc.server) > 0 {
				rejected(t, result, err, tc.server)
			} else {
				expected := before.DeepCopy()
				if tc.serverSpec != nil {
					tc.serverSpec(expected)
				}
				if err != nil || result == nil || result.GetUID() != f.Live.GetUID() || result.GetResourceVersion() == "" || result.GetName() != f.Live.GetName() || result.GetNamespace() != f.Namespace || result.GroupVersionKind() != f.GVK || !reflect.DeepEqual(result.Object["spec"], expected.Object["spec"]) || !reflect.DeepEqual(result.GetLabels(), expected.GetLabels()) {
					t.Fatalf("live string set dry-run did not preview the same UID and complete expected spec; error type %T", err)
				}
			}
			assertLiveUnchanged(t, ctx, f)
		})
	}
	// This separate fixture proves that both the published schema and actual
	// fresh admission observe the atomic-to-set change before testing old data.
	legacy := testcluster.NewSchemaListSetsWithLegacyDuplicates(t)
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
		{name: "legacy-changed-duplicate-values-accepted", duplicateField: "values", mutate: set("values", []any{"new", "new", "new"})},
		{name: "legacy-new-duplicate-other-set-accepted", duplicateField: "defaulted", mutate: func(d *unstructured.Unstructured) {
			spec(d)["values"] = []any{"fixed", "different"}
			spec(d)["defaulted"] = []any{"cross", "cross"}
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
