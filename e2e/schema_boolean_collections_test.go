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

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/schemaassist"
	"github.com/laojianzi/aster/internal/testcluster"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

type booleanCollectionFixture struct {
	backend   *kube.Backend
	namespace string
	gvk       schema.GroupVersionKind
	gvr       schema.GroupVersionResource
	live      *unstructured.Unstructured
	draft     func(string) *unstructured.Unstructured
}

func newBooleanCollectionFixture(t *testing.T, isMap, legacy bool) booleanCollectionFixture {
	t.Helper()
	if isMap {
		var f testcluster.SchemaListMapsFixture
		if legacy {
			f = testcluster.NewSchemaBooleanListMapsWithLegacyDuplicates(t)
		} else {
			f = testcluster.NewSchemaBooleanListMaps(t)
		}
		return booleanCollectionFixture{f.Backend, f.Namespace, f.GVK, f.GVR, f.Live, f.Draft}
	}
	var f testcluster.SchemaListSetsFixture
	if legacy {
		f = testcluster.NewSchemaBooleanSetsWithLegacyDuplicates(t)
	} else {
		f = testcluster.NewSchemaBooleanSets(t)
	}
	return booleanCollectionFixture{f.Backend, f.Namespace, f.GVK, f.GVR, f.Live, f.Draft}
}

type booleanHint struct{ pointer, code string }
type booleanCause struct {
	kind  metav1.CauseType
	field string
}
type booleanCollectionCase struct {
	name, field string
	value       any
	hints       []booleanHint
	causes      []booleanCause
	partial     bool
	transformed bool
	expected    any
}

func TestRealSchemaBooleanCollectionsAgreeWithDryRun(t *testing.T) {
	for _, isMap := range []bool{false, true} {
		name := "set"
		if isMap {
			name = "map"
		}
		t.Run(name, func(t *testing.T) {
			f := newBooleanCollectionFixture(t, isMap, false)
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			doc, err := f.backend.SchemaDocument(ctx, f.gvk)
			if err != nil {
				t.Fatal(err)
			}
			code, message := "list-set", "List entries repeat the advertised boolean value."
			if isMap {
				code, message = "list-map-keys", "List entries repeat the advertised key identity."
			}
			hint := func(field string) []booleanHint { return []booleanHint{{"/spec/" + field, code}} }
			duplicate := func(field string) []booleanCause {
				return []booleanCause{{metav1.CauseTypeFieldValueDuplicate, "spec." + field + "[1]"}}
			}
			cases := []booleanCollectionCase{
				{name: "empty", field: "values", value: []any{}},
				{name: "explicit-false", field: "values", value: []any{false}},
				{name: "distinct", field: "values", value: []any{false, true}},
				{name: "reverse-order", field: "values", value: []any{true, false}},
				{name: "false-duplicate", field: "values", value: []any{false, false}, hints: hint("values"), causes: duplicate("values")},
				{name: "true-duplicate", field: "values", value: []any{true, true}, hints: hint("values"), causes: duplicate("values")},
				{name: "triplicate", field: "values", value: []any{false, false, false}, hints: hint("values"), causes: duplicate("values")},
				{name: "two-duplicate-groups", field: "values", value: []any{false, false, true, true}, hints: hint("values"), causes: []booleanCause{{metav1.CauseTypeFieldValueDuplicate, "spec.values[1]"}, {metav1.CauseTypeFieldValueDuplicate, "spec.values[3]"}}},
				{name: "late-quoted", field: "values", value: []any{false, false, "PRIVATE_TRUE"}, hints: []booleanHint{{"/spec/values/2", "type"}}, causes: []booleanCause{{metav1.CauseTypeTypeInvalid, "spec.values[2]"}, {metav1.CauseTypeFieldValueDuplicate, "spec.values[1]"}}, partial: true},
				{name: "number-not-boolean", field: "values", value: []any{int64(0)}, hints: []booleanHint{{"/spec/values/0", "type"}}, causes: []booleanCause{{metav1.CauseTypeTypeInvalid, "spec.values[0]"}}, partial: true},
				{name: "null-item", field: "values", value: []any{nil}, hints: []booleanHint{{"/spec/values/0", "type"}}, causes: []booleanCause{{metav1.CauseTypeTypeInvalid, "spec.values[0]"}}, partial: true},
				{name: "defaulted-null", field: "defaulted", value: []any{nil}, hints: []booleanHint{{"/spec/defaulted/0", "type"}}, partial: true, transformed: true, expected: []any{true}},
				{name: "defaulted-collision", field: "defaulted", value: []any{nil, true}, hints: []booleanHint{{"/spec/defaulted/0", "type"}}, partial: true, causes: duplicate("defaulted")},
				{name: "default-preserves-false", field: "defaulted", value: []any{false, true}},
				{name: "nullable-parent", field: "nullable", value: nil},
				{name: "nullable-parent-duplicate", field: "nullable", value: []any{false, false}, hints: hint("nullable"), causes: duplicate("nullable")},
				{name: "nonnullable-parent-pruned", field: "values", value: nil, hints: []booleanHint{{"/spec/values", "type"}}, causes: []booleanCause{{metav1.CauseTypeFieldValueRequired, "spec.values"}}},
			}
			named := func(key any, payload string) map[string]any { return map[string]any{"name": key, "value": payload} }
			scoped := func(key bool, port int64, zone string) map[string]any {
				return map[string]any{"name": key, "port": port, "zone": zone, "value": "PRIVATE_PAYLOAD"}
			}
			if isMap {
				cases = []booleanCollectionCase{
					{name: "empty", field: "named", value: []any{}},
					{name: "explicit-false", field: "named", value: []any{named(false, "PRIVATE_A")}},
					{name: "distinct", field: "named", value: []any{named(false, "PRIVATE_A"), named(true, "PRIVATE_B")}},
					{name: "reverse-order", field: "named", value: []any{named(true, "PRIVATE_A"), named(false, "PRIVATE_B")}},
					{name: "false-duplicate-payload-independent", field: "named", value: []any{named(false, "PRIVATE_A"), named(false, "PRIVATE_B")}, hints: hint("named"), causes: duplicate("named")},
					{name: "true-duplicate", field: "named", value: []any{named(true, "PRIVATE_A"), named(true, "PRIVATE_B")}, hints: hint("named"), causes: duplicate("named")},
					{name: "quoted-not-boolean", field: "named", value: []any{named("PRIVATE_TRUE", "one")}, hints: []booleanHint{{"/spec/named/0/name", "type"}}, partial: true, causes: []booleanCause{{metav1.CauseTypeTypeInvalid, "spec.named[0].name"}}},
					{name: "number-not-boolean", field: "named", value: []any{named(int64(0), "one")}, hints: []booleanHint{{"/spec/named/0/name", "type"}}, partial: true, causes: []booleanCause{{metav1.CauseTypeTypeInvalid, "spec.named[0].name"}}},
					{name: "null-key-pruned", field: "named", value: []any{named(nil, "one")}, hints: []booleanHint{{"/spec/named/0/name", "type"}}, partial: true, causes: []booleanCause{{metav1.CauseTypeFieldValueRequired, "spec.named[0].name"}}},
					{name: "missing-key", field: "named", value: []any{map[string]any{"value": "one"}}, hints: []booleanHint{{"/spec/named/0/name", "required"}}, partial: true, causes: []booleanCause{{metav1.CauseTypeFieldValueRequired, "spec.named[0].name"}}},
					{name: "mixed-boolean-distinguishes", field: "scoped", value: []any{scoped(false, 9007199254740993, "PRIVATE_ZONE"), scoped(true, 9007199254740993, "PRIVATE_ZONE")}},
					{name: "mixed-int64-distinguishes", field: "scoped", value: []any{scoped(false, 9007199254740992, "PRIVATE_ZONE"), scoped(false, 9007199254740993, "PRIVATE_ZONE")}},
					{name: "mixed-string-distinguishes", field: "scoped", value: []any{scoped(false, 0, "é"), scoped(false, 0, "e\u0301")}},
					{name: "mixed-tuple-duplicate", field: "scoped", value: []any{scoped(false, 9007199254740993, "PRIVATE_ZONE"), scoped(false, 9007199254740993, "PRIVATE_ZONE")}, hints: hint("scoped"), causes: duplicate("scoped")},
					{name: "default-only-missing", field: "defaulted", value: []any{map[string]any{"value": "one"}}, partial: true, transformed: true, expected: []any{named(true, "one")}},
					{name: "default-only-collision", field: "defaulted", value: []any{map[string]any{"value": "one"}, named(true, "two")}, partial: true, causes: duplicate("defaulted")},
					{name: "default-only-preserves-false", field: "defaulted", value: []any{named(false, "one"), map[string]any{"value": "two"}}, partial: true, transformed: true, expected: []any{named(false, "one"), named(true, "two")}},
				}
			}
			for _, live := range []bool{false, true} {
				for index, tc := range cases {
					mode := "create"
					if live {
						mode = "live-update"
					}
					t.Run(mode+"/"+tc.name, func(t *testing.T) {
						draft := f.draft(fmt.Sprintf("boolean-%s-%02d", name, index))
						if live {
							draft = f.live.DeepCopy()
						}
						draft.Object["spec"].(map[string]any)[tc.field] = tc.value
						before := draft.DeepCopy()
						checkBooleanCollectionDraft(t, ctx, doc, draft, tc.hints, tc.partial, code, message)
						var result *unstructured.Unstructured
						if live {
							body, marshalErr := json.Marshal([]map[string]any{{"op": "test", "path": "/metadata/uid", "value": f.live.GetUID()}, {"op": "test", "path": "/metadata/resourceVersion", "value": f.live.GetResourceVersion()}, {"op": "add", "path": "/spec/" + tc.field, "value": tc.value}})
							if marshalErr != nil {
								t.Fatal(marshalErr)
							}
							result, err = f.backend.PatchObject(ctx, f.gvr, f.namespace, f.live.GetName(), types.JSONPatchType, body, metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "boolean-collections-test", FieldValidation: "Strict"})
						} else {
							result, err = f.backend.CreateObject(ctx, f.gvr, f.namespace, draft, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "boolean-collections-test", FieldValidation: "Strict"})
						}
						if len(tc.causes) > 0 {
							rejectBooleanCollection(t, result, err, tc.causes)
						} else {
							expected := before.DeepCopy()
							if tc.transformed {
								expected.Object["spec"].(map[string]any)[tc.field] = tc.expected
							}
							if err != nil || result == nil || result.GetName() != draft.GetName() || result.GetNamespace() != f.namespace || result.GroupVersionKind() != f.gvk || !reflect.DeepEqual(result.Object["spec"], expected.Object["spec"]) || !reflect.DeepEqual(result.GetLabels(), expected.GetLabels()) {
								t.Fatalf("boolean dry-run did not preserve exact expected object; error type %T", err)
							}
							if live && (result.GetUID() != f.live.GetUID() || result.GetResourceVersion() == "") {
								t.Fatal("boolean update preview lost live identity")
							}
						}
						if !reflect.DeepEqual(draft.Object, before.Object) {
							t.Fatal("boolean dry-run mutated its input")
						}
						if !live {
							if _, err := f.backend.GetObject(ctx, f.gvr, f.namespace, draft.GetName()); !apierrors.IsNotFound(err) {
								t.Fatalf("dry-run absence not established; error type %T", err)
							}
						}
						assertBooleanLiveUnchanged(t, ctx, f)
					})
				}
			}
			t.Run("legacy-invalid-update-boundary", func(t *testing.T) {
				legacy := newBooleanCollectionFixture(t, isMap, true)
				lctx, lcancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer lcancel()
				ld, err := legacy.backend.SchemaDocument(lctx, legacy.gvk)
				if err != nil {
					t.Fatal(err)
				}
				field := "values"
				if isMap {
					field = "named"
				}
				draft := legacy.live.DeepCopy()
				draft.SetLabels(map[string]string{"boolean-review": "dry-run"})
				checkBooleanCollectionDraft(t, lctx, ld, draft, hint(field), false, code, message)
				body, err := json.Marshal([]map[string]any{{"op": "test", "path": "/metadata/uid", "value": legacy.live.GetUID()}, {"op": "test", "path": "/metadata/resourceVersion", "value": legacy.live.GetResourceVersion()}, {"op": "add", "path": "/metadata/labels", "value": draft.GetLabels()}})
				if err != nil {
					t.Fatal(err)
				}
				result, err := legacy.backend.PatchObject(lctx, legacy.gvr, legacy.namespace, legacy.live.GetName(), types.JSONPatchType, body, metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "boolean-collections-test", FieldValidation: "Strict"})
				if err != nil || result == nil || result.GetUID() != legacy.live.GetUID() || result.GetResourceVersion() == "" || result.GetName() != draft.GetName() || result.GetNamespace() != legacy.namespace || result.GroupVersionKind() != legacy.gvk || !reflect.DeepEqual(result.Object["spec"], draft.Object["spec"]) || !reflect.DeepEqual(result.GetLabels(), draft.GetLabels()) {
					t.Fatalf("legacy boolean dry-run boundary changed; error type %T", err)
				}
				fresh := legacy.draft("boolean-legacy-fresh")
				fresh.Object["spec"] = draft.DeepCopy().Object["spec"]
				checkBooleanCollectionDraft(t, lctx, ld, fresh, hint(field), false, code, message)
				before := fresh.DeepCopy()
				result, err = legacy.backend.CreateObject(lctx, legacy.gvr, legacy.namespace, fresh, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "boolean-collections-test", FieldValidation: "Strict"})
				rejectBooleanCollection(t, result, err, duplicate(field))
				if !reflect.DeepEqual(fresh.Object, before.Object) {
					t.Fatal("legacy boundary check changed its input")
				}
				if _, err := legacy.backend.GetObject(lctx, legacy.gvr, legacy.namespace, fresh.GetName()); !apierrors.IsNotFound(err) {
					t.Fatalf("legacy dry-run absence not established; error type %T", err)
				}
				assertBooleanLiveUnchanged(t, lctx, legacy)
			})
		})
	}
}

func checkBooleanCollectionDraft(t *testing.T, ctx context.Context, doc *schemaassist.Document, draft *unstructured.Unstructured, want []booleanHint, partial bool, code, message string) {
	t.Helper()
	before := draft.DeepCopy()
	report, err := doc.Check(ctx, draft.Object)
	if err != nil {
		t.Fatal(err)
	}
	got, wants := map[booleanHint]int{}, map[booleanHint]int{}
	for _, d := range report.Diagnostics {
		got[booleanHint{d.Pointer, d.Code}]++
		if d.Code == code && d.Message != message {
			t.Fatal("non-private boolean identity message")
		}
	}
	for _, h := range want {
		wants[h]++
	}
	if !reflect.DeepEqual(got, wants) || partial && !report.Partial {
		t.Fatalf("unexpected boolean local paths/codes: got %v want %v, incomplete %t", got, wants, report.Partial)
	}
	if !reflect.DeepEqual(draft.Object, before.Object) || strings.Contains(report.Text(), "PRIVATE_") || strings.Contains(report.Text(), "9007199254740993") || !strings.Contains(report.Text(), "Server dry-run remains required.") {
		t.Fatal("boolean advisory check mutated/disclosed source or omitted admission boundary")
	}
}
func rejectBooleanCollection(t *testing.T, result *unstructured.Unstructured, err error, want []booleanCause) {
	t.Helper()
	var status apierrors.APIStatus
	if result != nil || !apierrors.IsInvalid(err) || !errors.As(err, &status) || status.Status().Details == nil {
		t.Fatalf("expected boolean server validation rejection; error type %T", err)
	}
	got, wants := map[booleanCause]int{}, map[booleanCause]int{}
	for _, c := range status.Status().Details.Causes {
		got[booleanCause{c.Type, c.Field}]++
	}
	for _, c := range want {
		wants[c]++
	}
	if !reflect.DeepEqual(got, wants) {
		t.Fatalf("unexpected server cause types/fields: got %v want %v", got, wants)
	}
}
func assertBooleanLiveUnchanged(t *testing.T, ctx context.Context, f booleanCollectionFixture) {
	t.Helper()
	after, err := f.backend.GetObject(ctx, f.gvr, f.namespace, f.live.GetName())
	if err != nil || after == nil || after.GetUID() != f.live.GetUID() || after.GetResourceVersion() != f.live.GetResourceVersion() || after.GetName() != f.live.GetName() || after.GetNamespace() != f.namespace || after.GroupVersionKind() != f.gvk || !reflect.DeepEqual(after.Object["spec"], f.live.Object["spec"]) || !reflect.DeepEqual(after.GetLabels(), f.live.GetLabels()) {
		t.Fatalf("boolean inspection/dry-run changed live object; error type %T", err)
	}
}
