//go:build e2e

package e2e

import (
	"context"
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

func TestRealSchemaPatternAgreeWithDryRun(t *testing.T) {
	f := testcluster.NewSchemaPatterns(t)
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
		t.Fatal("same-identity fresh discovery did not advertise the pattern CRD")
	}
	document, err := f.Backend.SchemaDocument(ctx, f.GVK)
	if err != nil {
		t.Fatal(err)
	}
	check := func(t *testing.T, draft *unstructured.Unstructured, field, code string) {
		t.Helper()
		before := draft.DeepCopy()
		report, err := document.Check(ctx, draft.Object)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(draft.Object, before.Object) {
			t.Fatal("read-only pattern inspection changed its draft")
		}
		// Other published metadata may remain incomplete. These comparisons
		// establish the actual selected constraints, not complete admission.
		if code == "" {
			if len(report.Diagnostics) != 0 {
				t.Fatalf("matching pattern produced a local constraint hint: %s", report.Text())
			}
		} else if len(report.Diagnostics) != 1 || report.Diagnostics[0].Pointer != "/spec/"+field || report.Diagnostics[0].Code != code {
			t.Fatalf("published pattern/type was not inspected at its actual field: %s", report.Text())
		}
		for _, private := range []string{"PRIVATE_PATTERN_DRAFT", "^[a-z][a-z0-9-]*$", "船"} {
			if strings.Contains(report.Text(), private) {
				t.Fatal("pattern report echoed a draft value or schema pattern")
			}
		}
		if !strings.Contains(report.Text(), "Server dry-run remains required") {
			t.Fatal("pattern report omitted the server admission boundary")
		}
	}
	rejected := func(t *testing.T, result *unstructured.Unstructured, err error, field string) {
		t.Helper()
		var status apierrors.APIStatus
		if !apierrors.IsInvalid(err) || !errors.As(err, &status) || status.Status().Details == nil || result != nil {
			t.Fatalf("expected server field validation rejection, got %v", err)
		}
		for _, cause := range status.Status().Details.Causes {
			if cause.Field == "spec."+field {
				return
			}
		}
		t.Fatal("server rejection did not identify the locally diagnosed pattern/type field")
	}
	assertLiveUnchanged := func(t *testing.T) {
		t.Helper()
		after, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, f.Live.GetName())
		if err != nil || after == nil || after.GetUID() != f.Live.GetUID() || after.GetResourceVersion() != f.Live.GetResourceVersion() || after.GroupVersionKind() != f.GVK || after.GetNamespace() != f.Namespace || after.GetName() != f.Live.GetName() || !reflect.DeepEqual(after.Object["spec"], f.Live.Object["spec"]) {
			t.Fatalf("pattern inspection or dry-run changed the live fixture identity or spec: %v", err)
		}
	}
	nextName := 0
	createDryRun := func(t *testing.T, field string, value any, code string) {
		t.Helper()
		nextName++
		draft := f.Draft(fmt.Sprintf("schema-pattern-%02d", nextName))
		if field != "" {
			draft.Object["spec"].(map[string]any)[field] = value
		}
		check(t, draft, field, code)
		result, err := f.Backend.CreateObject(ctx, f.GVR, f.Namespace, draft, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "schema-pattern-dry-run", FieldValidation: "Strict"})
		if code == "" {
			if err != nil || result == nil || result.GetName() != draft.GetName() || result.GetNamespace() != f.Namespace || result.GroupVersionKind() != f.GVK {
				t.Fatalf("server rejected a supported pattern boundary: %v", err)
			}
			if !reflect.DeepEqual(result.Object["spec"], draft.Object["spec"]) {
				t.Fatal("server dry-run did not preserve the tested string/null fields")
			}
		} else {
			rejected(t, result, err, field)
		}
		if _, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, draft.GetName()); !apierrors.IsNotFound(err) {
			t.Fatalf("checked/dry-run pattern draft unexpectedly persisted: %v", err)
		}
		assertLiveUnchanged(t)
	}
	// All patterns use the supported no-flags subset. BMP literals can match
	// within valid UTF-8 strings containing emoji without treating an astral
	// pattern, dot, negated class or character-count equivalence as supported.
	for _, tc := range []struct {
		name, field string
		value       any
		code        string
	}{
		{"valid-defaults", "", nil, ""},
		{"slug-anchored-valid", "slug", "native-fixed", ""},
		{"slug-leading-digit", "slug", "1invalid", "pattern"},
		{"slug-uppercase", "slug", "PRIVATE_PATTERN_DRAFT_SLUG", "pattern"},
		{"slug-invalid-character", "slug", "invalid.slug", "pattern"},
		{"slug-empty", "slug", "", "pattern"},
		{"slug-trailing-line-feed", "slug", "valid-name\n", "pattern"},
		{"contains-unanchored-match", "contains", "ok", ""},
		{"contains-missing-literal", "contains", "PRIVATE_PATTERN_DRAFT_TOKEN", "pattern"},
		{"contains-case-sensitive", "contains", "OK", "pattern"},
		{"contains-empty", "contains", "", "pattern"},
		{"contains-line-boundaries", "contains", "before\nok\nafter", ""},
		{"contains-split-literal", "contains", "o\nk", "pattern"},
		{"unicode-literal", "unicode", "船", ""},
		{"unicode-emoji-neighbors", "unicode", "🚢船🚢", ""},
		{"unicode-missing-literal", "unicode", "PRIVATE_PATTERN_DRAFT_🚢", "pattern"},
		{"empty-string", "empty", "", ""},
		{"empty-nonempty", "empty", "PRIVATE_PATTERN_DRAFT_EMPTY", "pattern"},
		{"empty-line-feed", "empty", "\n", "pattern"},
		{"nullable-null", "nullable", nil, ""},
		{"nullable-matching-string", "nullable", "before-ok-after", ""},
		{"nullable-nonmatching-string", "nullable", "PRIVATE_PATTERN_DRAFT_NULLABLE", "pattern"},
		{"string-type-integer", "slug", int64(7), "type"},
		{"string-type-boolean", "contains", true, "type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			createDryRun(t, tc.field, tc.value, tc.code)
		})
	}
	for _, tc := range []struct {
		name, body, value string
		invalid           bool
	}{
		{"live-valid-dry-run", `{"spec":{"slug":"reviewed-next"}}`, "reviewed-next", false},
		{"live-invalid-dry-run", `{"spec":{"slug":"PRIVATE_PATTERN_DRAFT_LIVE"}}`, "PRIVATE_PATTERN_DRAFT_LIVE", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft := f.Live.DeepCopy()
			draft.Object["spec"].(map[string]any)["slug"] = tc.value
			code := ""
			if tc.invalid {
				code = "pattern"
			}
			check(t, draft, "slug", code)
			result, err := f.Backend.PatchObject(ctx, f.GVR, f.Namespace, f.Live.GetName(), types.MergePatchType, []byte(tc.body), metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "schema-pattern-dry-run", FieldValidation: "Strict"})
			if tc.invalid {
				rejected(t, result, err, "slug")
			} else if err != nil || result == nil || result.GetUID() != f.Live.GetUID() || result.GetName() != f.Live.GetName() || result.GetNamespace() != f.Namespace || result.GroupVersionKind() != f.GVK || !reflect.DeepEqual(result.Object["spec"], draft.Object["spec"]) {
				t.Fatalf("live dry-run did not preview the same UID and intended string draft: %v", err)
			}
			assertLiveUnchanged(t)
		})
	}
	assertLiveUnchanged(t)
}
