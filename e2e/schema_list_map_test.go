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

func TestRealSchemaListMapAgreeWithDryRun(t *testing.T) {
	f := testcluster.NewSchemaListMaps(t)
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
		t.Fatal("same-identity fresh discovery did not advertise the list-map CRD")
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
	duplicate := func(field string) []hint { return []hint{{"/spec/" + field, "list-map-keys"}} }
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
			t.Fatal("read-only list identity inspection changed its draft")
		}
		got := make(map[hint]int)
		for _, d := range report.Diagnostics {
			got[hint{d.Pointer, d.Code}]++
		}
		wants := make(map[hint]int)
		for _, w := range want {
			wants[w]++
		}
		if !reflect.DeepEqual(got, wants) {
			t.Fatalf("published list identity/type constraints produced different hints: %s", report.Text())
		}
		if incomplete && !report.Partial {
			t.Fatal("an unsupported or incomplete identity was reported as completely inspected")
		}
		for _, private := range []string{"PRIVATE_LIST_MAP", "中文船🚢", "fallback", "a|b", "e\u0301"} {
			if strings.Contains(report.Text(), private) {
				t.Fatal("list-map report echoed an identity or non-key payload")
			}
		}
		if !strings.Contains(report.Text(), "Server dry-run remains required") {
			t.Fatal("list-map report omitted its server admission boundary")
		}
	}
	rejected := func(t *testing.T, result *unstructured.Unstructured, err error, want []cause) {
		t.Helper()
		var status apierrors.APIStatus
		if result != nil || !apierrors.IsInvalid(err) || !errors.As(err, &status) || status.Status().Details == nil {
			t.Fatalf("expected server field validation rejection, got %v", err)
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
			// Only cause kinds and paths are printed. Server messages may embed
			// identity values and must not become accidental test-log payloads.
			t.Fatalf("server rejection had different cause types/fields: got %v, want %v", got, wants)
		}
	}
	assertLiveUnchanged := func(t *testing.T, ctx context.Context, fixture testcluster.SchemaListMapsFixture) {
		t.Helper()
		after, err := fixture.Backend.GetObject(ctx, fixture.GVR, fixture.Namespace, fixture.Live.GetName())
		if err != nil || after == nil || after.GetUID() != fixture.Live.GetUID() || after.GetResourceVersion() != fixture.Live.GetResourceVersion() || after.GroupVersionKind() != fixture.GVK || after.GetNamespace() != fixture.Namespace || after.GetName() != fixture.Live.GetName() || !reflect.DeepEqual(after.Object["spec"], fixture.Live.Object["spec"]) || !reflect.DeepEqual(after.GetLabels(), fixture.Live.GetLabels()) {
			t.Fatalf("list inspection or dry-run changed live identity, labels, or spec: %v", err)
		}
	}
	spec := func(d *unstructured.Unstructured) map[string]any { return d.Object["spec"].(map[string]any) }
	named := func(name, value string) any { return map[string]any{"name": name, "value": value} }
	scoped := func(zone, name, value string) any { return map[string]any{"zone": zone, "name": name, "value": value} }
	set := func(field string, value any) func(*unstructured.Unstructured) {
		return func(d *unstructured.Unstructured) { spec(d)[field] = value }
	}
	// Primary comparisons are fresh creates. The default-only cases and the
	// legacy-object update below are explicitly named server boundaries; they
	// do not assert that advisory identity checks reproduce full admission.
	for index, tc := range []struct {
		name       string
		mutate     func(*unstructured.Unstructured)
		local      []hint
		server     []cause
		incomplete bool
		serverSpec func(*unstructured.Unstructured)
	}{
		{name: "valid-required-string-identities"},
		{name: "named-empty-list", mutate: set("named", []any{})},
		{name: "named-empty-string-is-a-key", mutate: set("named", []any{named("", "PRIVATE_LIST_MAP_EMPTY")})},
		{name: "named-empty-string-duplicate", mutate: set("named", []any{named("", "one"), named("", "two")}), local: duplicate("named"), server: serverDuplicate("named", 1)},
		{name: "named-duplicate-ignores-non-key-payload", mutate: set("named", []any{named("PRIVATE_LIST_MAP_ID", "one"), named("PRIVATE_LIST_MAP_ID", "two")}), local: duplicate("named"), server: serverDuplicate("named", 1)},
		{name: "named-triplicate-has-one-server-duplicate-location", mutate: set("named", []any{named("same", "one"), named("same", "two"), named("same", "three")}), local: duplicate("named"), server: serverDuplicate("named", 1)},
		{name: "named-two-duplicate-identities-one-local-parent-hint", mutate: set("named", []any{named("a", "one"), named("a", "two"), named("b", "three"), named("b", "four")}), local: duplicate("named"), server: []cause{{metav1.CauseTypeFieldValueDuplicate, "spec.named[1]"}, {metav1.CauseTypeFieldValueDuplicate, "spec.named[3]"}}},
		{name: "named-case-is-not-folded", mutate: set("named", []any{named("api", "one"), named("API", "two")})},
		{name: "named-whitespace-is-not-trimmed", mutate: set("named", []any{named("api", "one"), named(" api", "two")})},
		{name: "named-unicode-is-not-normalized", mutate: set("named", []any{named("é", "one"), named("e\u0301", "two")})},
		{name: "named-unicode-exact-duplicate", mutate: set("named", []any{named("中文船🚢", "one"), named("中文船🚢", "two")}), local: duplicate("named"), server: serverDuplicate("named", 1)},
		{name: "named-256-unique-identities", mutate: func(d *unstructured.Unstructured) {
			items := make([]any, 256)
			for i := range items {
				items[i] = named(fmt.Sprintf("entry-%03d", i), "PRIVATE_LIST_MAP_PAYLOAD")
			}
			spec(d)["named"] = items
		}},
		{name: "named-256-last-identity-duplicates", mutate: func(d *unstructured.Unstructured) {
			items := make([]any, 256)
			for i := range items {
				items[i] = named(fmt.Sprintf("entry-%03d", i), "PRIVATE_LIST_MAP_PAYLOAD")
			}
			items[255] = named("entry-000", "different payload")
			spec(d)["named"] = items
		}, local: duplicate("named"), server: serverDuplicate("named", 255)},
		{name: "scoped-shared-name-different-zone", mutate: set("scoped", []any{scoped("east", "api", "one"), scoped("west", "api", "two")})},
		{name: "scoped-shared-zone-different-name", mutate: set("scoped", []any{scoped("east", "api", "one"), scoped("east", "worker", "two")})},
		{name: "scoped-empty-members-remain-distinct", mutate: set("scoped", []any{scoped("", "api", "one"), scoped("api", "", "two")})},
		{name: "scoped-empty-tuple-duplicates", mutate: set("scoped", []any{scoped("", "", "one"), scoped("", "", "two")}), local: duplicate("scoped"), server: serverDuplicate("scoped", 1)},
		{name: "scoped-duplicate-tuple-ignores-non-key-payload", mutate: set("scoped", []any{scoped("east", "api", "PRIVATE_LIST_MAP_FIRST"), scoped("east", "api", "PRIVATE_LIST_MAP_SECOND")}), local: duplicate("scoped"), server: serverDuplicate("scoped", 1)},
		{name: "scoped-separator-concatenation-cannot-collide", mutate: set("scoped", []any{scoped("a|b", "c", "one"), scoped("a", "b|c", "two")})},
		{name: "scoped-nul-separator-cannot-collide", mutate: set("scoped", []any{scoped("a\x00", "b", "one"), scoped("a", "\x00b", "two")})},
		{name: "scoped-unicode-tuple-is-exact", mutate: set("scoped", []any{scoped("中文船🚢", "é", "one"), scoped("中文船🚢", "e\u0301", "two")})},
		{name: "named-missing-key-stays-incomplete", mutate: set("named", []any{map[string]any{"value": "PRIVATE_LIST_MAP_MISSING"}}), local: []hint{{"/spec/named/0/name", "required"}}, server: []cause{{metav1.CauseTypeFieldValueRequired, "spec.named[0].name"}}, incomplete: true},
		// The server removes a nonnullable null object property before required
		// validation. Aster preserves the user's null and reports its type.
		{name: "named-null-key-server-pruning-boundary", mutate: set("named", []any{map[string]any{"name": nil, "value": "one"}}), local: []hint{{"/spec/named/0/name", "type"}}, server: []cause{{metav1.CauseTypeFieldValueRequired, "spec.named[0].name"}}, incomplete: true},
		{name: "named-non-string-key-stays-incomplete", mutate: set("named", []any{map[string]any{"name": int64(7), "value": "one"}}), local: []hint{{"/spec/named/0/name", "type"}}, server: []cause{{metav1.CauseTypeTypeInvalid, "spec.named[0].name"}}, incomplete: true},
		{name: "named-non-object-item-stays-incomplete", mutate: set("named", []any{"PRIVATE_LIST_MAP_NOT_AN_OBJECT"}), local: []hint{{"/spec/named/0", "type"}}, server: []cause{{metav1.CauseTypeTypeInvalid, "spec.named[0]"}, {metav1.CauseTypeFieldValueInvalid, "spec.named[0]"}}, incomplete: true},
		{name: "named-null-item-stays-incomplete", mutate: set("named", []any{nil}), local: []hint{{"/spec/named/0", "type"}}, server: []cause{{metav1.CauseTypeTypeInvalid, "spec.named[0]"}}, incomplete: true},
		{name: "named-object-instead-of-array", mutate: set("named", map[string]any{}), local: []hint{{"/spec/named", "type"}}, server: []cause{{metav1.CauseTypeTypeInvalid, "spec.named"}}},
		{name: "scoped-missing-one-key-stays-incomplete", mutate: set("scoped", []any{map[string]any{"name": "api", "value": "one"}}), local: []hint{{"/spec/scoped/0/zone", "required"}}, server: []cause{{metav1.CauseTypeFieldValueRequired, "spec.scoped[0].zone"}}, incomplete: true},
		{name: "scoped-null-key-server-pruning-boundary", mutate: set("scoped", []any{map[string]any{"zone": nil, "name": "api", "value": "one"}}), local: []hint{{"/spec/scoped/0/zone", "type"}}, server: []cause{{metav1.CauseTypeFieldValueRequired, "spec.scoped[0].zone"}}, incomplete: true},
		{name: "scoped-non-string-key-stays-incomplete", mutate: set("scoped", []any{map[string]any{"zone": false, "name": "api", "value": "one"}}), local: []hint{{"/spec/scoped/0/zone", "type"}}, server: []cause{{metav1.CauseTypeTypeInvalid, "spec.scoped[0].zone"}}, incomplete: true},
		{name: "late-incomplete-identity-suppresses-new-duplicate-hint", mutate: set("named", []any{named("same", "one"), named("same", "two"), map[string]any{"value": "PRIVATE_LIST_MAP_MISSING"}}), local: []hint{{"/spec/named/2/name", "required"}}, server: []cause{{metav1.CauseTypeFieldValueRequired, "spec.named[2].name"}, {metav1.CauseTypeFieldValueDuplicate, "spec.named[1]"}}, incomplete: true},
		{name: "default-only-missing-key-server-default-boundary", mutate: set("defaulted", []any{map[string]any{"value": "PRIVATE_LIST_MAP_DEFAULT"}}), incomplete: true, serverSpec: func(d *unstructured.Unstructured) {
			spec(d)["defaulted"].([]any)[0].(map[string]any)["name"] = "fallback"
		}},
		{name: "default-only-missing-and-explicit-key-server-collision", mutate: set("defaulted", []any{map[string]any{"value": "one"}, named("fallback", "two")}), server: serverDuplicate("defaulted", 1), incomplete: true},
		{name: "default-only-explicit-unique-remains-unsupported", mutate: set("defaulted", []any{named("left", "one"), named("right", "two")}), incomplete: true},
		{name: "default-only-explicit-duplicates-remain-unsupported", mutate: set("defaulted", []any{named("same", "one"), named("same", "two")}), server: serverDuplicate("defaulted", 1), incomplete: true},
		{name: "default-only-null-key-server-default-boundary", mutate: set("defaulted", []any{map[string]any{"name": nil, "value": "one"}}), local: []hint{{"/spec/defaulted/0/name", "type"}}, incomplete: true, serverSpec: func(d *unstructured.Unstructured) {
			spec(d)["defaulted"].([]any)[0].(map[string]any)["name"] = "fallback"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft := f.Draft(fmt.Sprintf("schema-list-map-%02d", index+1))
			if tc.mutate != nil {
				tc.mutate(draft)
			}
			check(t, ctx, document, draft, tc.local, tc.incomplete)
			before := draft.DeepCopy()
			result, err := f.Backend.CreateObject(ctx, f.GVR, f.Namespace, draft, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "schema-list-map-dry-run", FieldValidation: "Strict"})
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
				if err != nil || result == nil || result.GetName() != draft.GetName() || result.GetNamespace() != f.Namespace || result.GroupVersionKind() != f.GVK || !reflect.DeepEqual(result.Object["spec"], expected.Object["spec"]) {
					t.Fatalf("server did not return the complete expected list draft: %v", err)
				}
			}
			if _, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, draft.GetName()); !apierrors.IsNotFound(err) {
				t.Fatalf("checked/dry-run list draft unexpectedly persisted: %v", err)
			}
			assertLiveUnchanged(t, ctx, f)
		})
	}
	for _, tc := range []struct {
		name, field string
		mutate      func(*unstructured.Unstructured)
		invalid     bool
	}{
		{"live-named-valid-dry-run", "named", func(d *unstructured.Unstructured) { spec(d)["named"].([]any)[1].(map[string]any)["name"] = "gateway" }, false},
		{"live-named-duplicate-dry-run", "named", func(d *unstructured.Unstructured) { spec(d)["named"].([]any)[1].(map[string]any)["name"] = "api" }, true},
		{"live-scoped-valid-dry-run", "scoped", func(d *unstructured.Unstructured) { spec(d)["scoped"].([]any)[1].(map[string]any)["zone"] = "north" }, false},
		{"live-scoped-duplicate-dry-run", "scoped", func(d *unstructured.Unstructured) { spec(d)["scoped"].([]any)[1].(map[string]any)["zone"] = "east" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft := f.Live.DeepCopy()
			tc.mutate(draft)
			var local []hint
			if tc.invalid {
				local = duplicate(tc.field)
			}
			check(t, ctx, document, draft, local, false)
			before := draft.DeepCopy()
			body, err := json.Marshal([]map[string]any{
				{"op": "test", "path": "/metadata/uid", "value": f.Live.GetUID()},
				{"op": "test", "path": "/metadata/resourceVersion", "value": f.Live.GetResourceVersion()},
				{"op": "replace", "path": "/spec/" + tc.field, "value": spec(draft)[tc.field]},
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := f.Backend.PatchObject(ctx, f.GVR, f.Namespace, f.Live.GetName(), types.JSONPatchType, body, metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "schema-list-map-dry-run", FieldValidation: "Strict"})
			if !reflect.DeepEqual(draft.Object, before.Object) {
				t.Fatal("live list dry-run changed its input draft")
			}
			if tc.invalid {
				rejected(t, result, err, serverDuplicate(tc.field, 1))
			} else if err != nil || result == nil || result.GetUID() != f.Live.GetUID() || result.GetResourceVersion() == "" || result.GetName() != f.Live.GetName() || result.GetNamespace() != f.Namespace || result.GroupVersionKind() != f.GVK || !reflect.DeepEqual(result.Object["spec"], draft.Object["spec"]) {
				t.Fatalf("live list dry-run did not preview the same UID and complete intended spec: %v", err)
			}
			assertLiveUnchanged(t, ctx, f)
		})
	}
	t.Run("old-invalid-list-ratcheting-server-boundary", func(t *testing.T) {
		legacy := testcluster.NewSchemaListMapsWithLegacyDuplicates(t)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		doc, err := legacy.Backend.SchemaDocument(ctx, legacy.GVK)
		if err != nil {
			t.Fatal(err)
		}
		draft := legacy.Live.DeepCopy()
		draft.SetLabels(map[string]string{"aster-list-map-review": "dry-run"})
		check(t, ctx, doc, draft, duplicate("named"), false)
		body, err := json.Marshal([]map[string]any{
			{"op": "test", "path": "/metadata/uid", "value": legacy.Live.GetUID()},
			{"op": "test", "path": "/metadata/resourceVersion", "value": legacy.Live.GetResourceVersion()},
			{"op": "add", "path": "/metadata/labels", "value": draft.GetLabels()},
		})
		if err != nil {
			t.Fatal(err)
		}
		result, err := legacy.Backend.PatchObject(ctx, legacy.GVR, legacy.Namespace, legacy.Live.GetName(), types.JSONPatchType, body, metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "schema-list-map-dry-run", FieldValidation: "Strict"})
		if err != nil || result == nil || result.GetUID() != legacy.Live.GetUID() || result.GetResourceVersion() == "" || result.GroupVersionKind() != legacy.GVK || result.GetNamespace() != legacy.Namespace || result.GetName() != legacy.Live.GetName() || !reflect.DeepEqual(result.Object["spec"], draft.Object["spec"]) || !reflect.DeepEqual(result.GetLabels(), draft.GetLabels()) {
			t.Fatalf("server did not accept the identity-bound metadata-only legacy dry-run: %v", err)
		}
		assertLiveUnchanged(t, ctx, legacy)
		fresh := legacy.Draft("schema-list-map-ratcheting-create")
		spec(fresh)["named"].([]any)[1].(map[string]any)["name"] = "api"
		check(t, ctx, doc, fresh, duplicate("named"), false)
		result, err = legacy.Backend.CreateObject(ctx, legacy.GVR, legacy.Namespace, fresh, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "schema-list-map-dry-run", FieldValidation: "Strict"})
		rejected(t, result, err, serverDuplicate("named", 1))
		if _, err := legacy.Backend.GetObject(ctx, legacy.GVR, legacy.Namespace, fresh.GetName()); !apierrors.IsNotFound(err) {
			t.Fatalf("legacy-boundary fresh dry-run object unexpectedly persisted: %v", err)
		}
		assertLiveUnchanged(t, ctx, legacy)
		t.Log("list-map server boundary: old invalid list metadata-only dry-run accepted; equivalent fresh duplicate create rejected; both live UID/resourceVersion/spec and fresh absence verified")
	})
	assertLiveUnchanged(t, ctx, f)
}
