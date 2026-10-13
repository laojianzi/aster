//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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

func TestRealSchemaIntegerMapKeysAgreeWithDryRun(t *testing.T) {
	f := testcluster.NewSchemaIntegerListMaps(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	doc, err := f.Backend.SchemaDocument(ctx, f.GVK)
	if err != nil {
		t.Fatal(err)
	}
	help, err := doc.Help(ctx, "/spec/named/0/name")
	if err != nil || help.Type != "integer" {
		t.Fatalf("published key was not integer: %+v %v", help, err)
	}
	type cause struct {
		kind  metav1.CauseType
		field string
	}
	assertLive := func(t *testing.T, fixture testcluster.SchemaListMapsFixture) {
		t.Helper()
		after, err := fixture.Backend.GetObject(ctx, fixture.GVR, fixture.Namespace, fixture.Live.GetName())
		if err != nil || after == nil || after.GetUID() != fixture.Live.GetUID() || after.GetResourceVersion() != fixture.Live.GetResourceVersion() || after.GroupVersionKind() != fixture.GVK || after.GetNamespace() != fixture.Namespace || after.GetName() != fixture.Live.GetName() || !reflect.DeepEqual(after.Object["spec"], fixture.Live.Object["spec"]) || !reflect.DeepEqual(after.GetLabels(), fixture.Live.GetLabels()) {
			t.Fatalf("integer dry-run changed live identity/spec: %v", err)
		}
	}
	rejected := func(t *testing.T, result *unstructured.Unstructured, err error, wants []cause) {
		t.Helper()
		var status apierrors.APIStatus
		if result != nil || !apierrors.IsInvalid(err) || !errors.As(err, &status) || status.Status().Details == nil {
			t.Fatalf("expected integer key validation rejection: %v", err)
		}
		got, want := map[cause]int{}, map[cause]int{}
		for _, v := range status.Status().Details.Causes {
			got[cause{v.Type, v.Field}]++
		}
		for _, v := range wants {
			want[v]++
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("integer rejection causes differ: %v != %v", got, want)
		}
	}
	type testCase struct {
		name                            string
		a, b                            any
		sameZone, duplicate, incomplete bool
		missing, wireDuplicate          bool
		localCode                       string
		causes                          []cause
	}
	cases := []testCase{
		{name: "distinct-ports", a: int64(8080), b: int64(9090), sameZone: true},
		{name: "duplicate-ports", a: int64(8080), b: int64(8080), sameZone: true, duplicate: true},
		{name: "zero-is-value", a: int64(0), b: int64(0), sameZone: true, duplicate: true},
		{name: "negative-distinct", a: int64(-1), b: int64(1), sameZone: true},
		{name: "above-float-precision-distinct", a: int64(9007199254740992), b: int64(9007199254740993), sameZone: true},
		{name: "above-float-precision-duplicate", a: int64(9007199254740993), b: int64(9007199254740993), sameZone: true, duplicate: true},
		{name: "max-int64-duplicate", a: int64(math.MaxInt64), b: int64(math.MaxInt64), sameZone: true, duplicate: true},
		{name: "min-int64-duplicate", a: int64(math.MinInt64), b: int64(math.MinInt64), sameZone: true, duplicate: true},
		{name: "endpoints-distinct", a: int64(math.MinInt64), b: int64(math.MaxInt64), sameZone: true},
		// Float is not admitted locally; transport may serialize it to an integer.
		{name: "integral-float-wire-boundary", a: int64(8080), b: float64(9090), sameZone: true, incomplete: true},
		{name: "float-wire-collision", a: int64(8080), b: float64(8080), sameZone: true, incomplete: true, wireDuplicate: true},
		{name: "quoted-key", a: int64(8080), b: "PRIVATE_BAD_KEY", sameZone: true, incomplete: true, localCode: "type", causes: []cause{{metav1.CauseTypeTypeInvalid, "[1].name"}}},
		{name: "null-key-pruning", a: int64(8080), b: nil, sameZone: true, incomplete: true, localCode: "type", causes: []cause{{metav1.CauseTypeFieldValueRequired, "[1].name"}}},
		{name: "missing-key", a: int64(8080), missing: true, sameZone: true, incomplete: true, localCode: "required", causes: []cause{{metav1.CauseTypeFieldValueRequired, "[1].name"}}},
		{name: "boolean-key", a: int64(8080), b: true, sameZone: true, incomplete: true, localCode: "type", causes: []cause{{metav1.CauseTypeTypeInvalid, "[1].name"}}},
		{name: "mixed-zone-distinguishes", a: int64(8080), b: int64(8080), sameZone: false},
	}
	count := 0
	for _, field := range []string{"named", "scoped"} {
		for _, tc := range cases {
			if field == "named" && !tc.sameZone {
				continue
			}
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				count++
				draft := f.Draft(fmt.Sprintf("integer-map-%02d", count))
				entries := draft.Object["spec"].(map[string]any)[field].([]any)
				entries[0].(map[string]any)["name"], entries[1].(map[string]any)["name"] = tc.a, tc.b
				if tc.missing {
					delete(entries[1].(map[string]any), "name")
				}
				if field == "scoped" && tc.sameZone {
					entries[1].(map[string]any)["zone"] = "east"
				}
				before := draft.DeepCopy()
				report, err := doc.Check(ctx, draft.Object)
				if err != nil {
					t.Fatal(err)
				}
				hints := 0
				for _, d := range report.Diagnostics {
					if tc.localCode != "" {
						if d.Code != tc.localCode || d.Pointer != "/spec/"+field+"/1/name" {
							t.Fatalf("unexpected structural hint: %+v", d)
						}
					} else if d.Code != "list-map-keys" || d.Pointer != "/spec/"+field || d.Message != "List entries repeat the advertised key identity." {
						t.Fatalf("unexpected identity hint: %+v", d)
					}
					hints++
				}
				want := 0
				if tc.duplicate || tc.localCode != "" {
					want = 1
				}
				if hints != want || tc.incomplete && !report.Partial {
					t.Fatal("integer identity did not match independent expectation")
				}

				if !reflect.DeepEqual(draft.Object, before.Object) || strings.Contains(report.Text(), "8080") || strings.Contains(report.Text(), "9007199254740993") {
					t.Fatal("integer report modified/disclosed identity")
				}
				result, err := f.Backend.CreateObject(ctx, f.GVR, f.Namespace, draft, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "integer-map-test", FieldValidation: "Strict"})
				if len(tc.causes) > 0 {
					causes := append([]cause{}, tc.causes...)
					for i := range causes {
						causes[i].field = "spec." + field + causes[i].field
					}
					rejected(t, result, err, causes)
				} else if tc.duplicate || tc.wireDuplicate {
					rejected(t, result, err, []cause{{metav1.CauseTypeFieldValueDuplicate, "spec." + field + "[1]"}})
				} else {
					expected := before.DeepCopy()
					if _, ok := tc.b.(float64); ok {
						expected.Object["spec"].(map[string]any)[field].([]any)[1].(map[string]any)["name"] = int64(9090)
					}
					if err != nil || result == nil || result.GetName() != draft.GetName() || result.GetNamespace() != f.Namespace || result.GroupVersionKind() != f.GVK || !reflect.DeepEqual(result.Object["spec"], expected.Object["spec"]) {
						t.Fatalf("integer dry-run did not return exact expected values: %v", err)
					}
				}
				if _, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, draft.GetName()); !apierrors.IsNotFound(err) {
					t.Fatalf("integer create dry-run persisted: %v", err)
				}
				if !reflect.DeepEqual(draft.Object, before.Object) {
					t.Fatal("integer create changed its input")
				}
				assertLive(t, f)
			})
		}
	}
	for _, field := range []string{"named", "scoped"} {
		for _, valid := range []bool{true, false} {
			t.Run(fmt.Sprintf("live-%s-%v", field, valid), func(t *testing.T) {
				draft := f.Live.DeepCopy()
				entries := draft.Object["spec"].(map[string]any)[field].([]any)
				if valid {
					entries[1].(map[string]any)["name"] = int64(10000)
				} else {
					entries[1].(map[string]any)["name"] = int64(8080)
					if field == "scoped" {
						entries[1].(map[string]any)["zone"] = "east"
					}
				}
				body, err := json.Marshal([]map[string]any{{"op": "test", "path": "/metadata/uid", "value": f.Live.GetUID()}, {"op": "test", "path": "/metadata/resourceVersion", "value": f.Live.GetResourceVersion()}, {"op": "replace", "path": "/spec/" + field, "value": entries}})
				if err != nil {
					t.Fatal(err)
				}
				result, err := f.Backend.PatchObject(ctx, f.GVR, f.Namespace, f.Live.GetName(), types.JSONPatchType, body, metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "integer-map-test", FieldValidation: "Strict"})
				if !valid {
					rejected(t, result, err, []cause{{metav1.CauseTypeFieldValueDuplicate, "spec." + field + "[1]"}})
				} else if err != nil || result == nil || result.GetUID() != f.Live.GetUID() || result.GetResourceVersion() == "" || !reflect.DeepEqual(result.Object["spec"], draft.Object["spec"]) {
					t.Fatalf("integer live dry-run lost intended values/identity: %v", err)
				}
				assertLive(t, f)
			})
		}
	}
	t.Run("default-only-collision-boundary", func(t *testing.T) {
		draft := f.Draft("integer-default-collision")
		draft.Object["spec"].(map[string]any)["defaulted"] = []any{map[string]any{"value": "one"}, map[string]any{"name": int64(8080), "value": "two"}}
		report, err := doc.Check(ctx, draft.Object)
		if err != nil || !report.Partial || len(report.Diagnostics) != 0 {
			t.Fatal("optional integer default was silently inferred locally")
		}
		result, err := f.Backend.CreateObject(ctx, f.GVR, f.Namespace, draft, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "integer-map-test", FieldValidation: "Strict"})
		rejected(t, result, err, []cause{{metav1.CauseTypeFieldValueDuplicate, "spec.defaulted[1]"}})
		if _, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, draft.GetName()); !apierrors.IsNotFound(err) {
			t.Fatal("default-boundary dry-run persisted")
		}
		assertLive(t, f)
	})
	t.Run("legacy-invalid-update-boundary", func(t *testing.T) {
		legacy := testcluster.NewSchemaIntegerListMapsWithLegacyDuplicates(t)
		ld, err := legacy.Backend.SchemaDocument(ctx, legacy.GVK)
		if err != nil {
			t.Fatal(err)
		}
		report, err := ld.Check(ctx, legacy.Live.Object)
		if err != nil || len(report.Diagnostics) != 1 || report.Diagnostics[0].Code != "list-map-keys" {
			t.Fatal("legacy integer duplicates not locally diagnosed")
		}
		body, err := json.Marshal([]map[string]any{{"op": "test", "path": "/metadata/uid", "value": legacy.Live.GetUID()}, {"op": "test", "path": "/metadata/resourceVersion", "value": legacy.Live.GetResourceVersion()}, {"op": "add", "path": "/metadata/labels", "value": map[string]string{"integer-review": "dry-run"}}})
		if err != nil {
			t.Fatal(err)
		}
		result, err := legacy.Backend.PatchObject(ctx, legacy.GVR, legacy.Namespace, legacy.Live.GetName(), types.JSONPatchType, body, metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "integer-map-test", FieldValidation: "Strict"})
		if err != nil || result == nil || result.GetUID() != legacy.Live.GetUID() || !reflect.DeepEqual(result.Object["spec"], legacy.Live.Object["spec"]) || result.GetLabels()["integer-review"] != "dry-run" {
			t.Fatalf("legacy integer update boundary changed: %v", err)
		}
		assertLive(t, legacy)
	})
}
