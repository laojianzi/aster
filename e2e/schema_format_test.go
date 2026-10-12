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

func TestRealSchemaFormatAgreeWithDryRun(t *testing.T) {
	f := testcluster.NewSchemaFormats(t)
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
		t.Fatal("same-identity fresh discovery did not advertise the format CRD")
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
			t.Fatal("read-only format inspection changed its draft")
		}
		// Other published metadata may remain incomplete. These comparisons
		// establish the selected format constraints, not complete admission.
		if code == "" {
			if len(report.Diagnostics) != 0 {
				t.Fatalf("matching format produced a local constraint hint: %s", report.Text())
			}
		} else if len(report.Diagnostics) != 1 || report.Diagnostics[0].Pointer != "/spec/"+field || report.Diagnostics[0].Code != code {
			t.Fatalf("published format/type was not inspected at its actual field: %s", report.Text())
		}
		for _, private := range []string{"PRIVATE_FORMAT_DRAFT", "2024-02-29", "YWJj", "123e4567-e89b-12d3-a456-426614174000", "AB==", "AAB="} {
			if strings.Contains(report.Text(), private) {
				t.Fatal("format report echoed a draft value")
			}
		}
		if !strings.Contains(report.Text(), "Server dry-run remains required") {
			t.Fatal("format report omitted the server admission boundary")
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
		t.Fatal("server rejection did not identify the locally diagnosed format/type field")
	}
	assertLiveUnchanged := func(t *testing.T) {
		t.Helper()
		after, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, f.Live.GetName())
		if err != nil || after == nil || after.GetUID() != f.Live.GetUID() || after.GetResourceVersion() != f.Live.GetResourceVersion() || after.GroupVersionKind() != f.GVK || after.GetNamespace() != f.Namespace || after.GetName() != f.Live.GetName() || !reflect.DeepEqual(after.Object["spec"], f.Live.Object["spec"]) {
			t.Fatalf("format inspection or dry-run changed the live fixture identity or spec: %v", err)
		}
	}
	nextName := 0
	createDryRun := func(t *testing.T, field string, value any, code string) {
		t.Helper()
		nextName++
		draft := f.Draft(fmt.Sprintf("schema-format-%02d", nextName))
		if field != "" {
			draft.Object["spec"].(map[string]any)[field] = value
		}
		check(t, draft, field, code)
		result, err := f.Backend.CreateObject(ctx, f.GVR, f.Namespace, draft, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "schema-format-dry-run", FieldValidation: "Strict"})
		if code == "" {
			if err != nil || result == nil || result.GetName() != draft.GetName() || result.GetNamespace() != f.Namespace || result.GroupVersionKind() != f.GVK {
				t.Fatalf("server rejected a supported format boundary: %v", err)
			}
			if !reflect.DeepEqual(result.Object["spec"], draft.Object["spec"]) {
				t.Fatal("server dry-run did not preserve the tested string/null fields")
			}
		} else {
			rejected(t, result, err, field)
		}
		if _, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, draft.GetName()); !apierrors.IsNotFound(err) {
			t.Fatalf("checked/dry-run format draft unexpectedly persisted: %v", err)
		}
		assertLiveUnchanged(t)
	}
	// Use fresh creates so validation ratcheting cannot accept an unchanged
	// invalid old value. The byte and UUID cases intentionally include values
	// accepted by Kubernetes that a stricter base64/RFC UUID parser may reject.
	for _, tc := range []struct {
		name, field string
		value       any
		code        string
	}{
		{"valid-defaults", "", nil, ""},
		{"date-leap-century", "date", "2000-02-29", ""},
		{"date-non-leap-century", "date", "1900-02-29", "format"},
		{"date-year-zero", "date", "0000-02-29", ""},
		{"date-invalid-month", "date", "2024-13-01", "format"},
		{"date-invalid-day", "date", "2024-04-31", "format"},
		{"date-non-padded-month", "date", "2024-2-29", "format"},
		{"date-empty", "date", "", "format"},
		{"date-trailing-time", "date", "2024-02-29T00:00:00Z", "format"},
		{"byte-padded", "byte", "Zg==", ""},
		{"byte-empty", "byte", "", "format"},
		{"byte-nonzero-padding-bits-two", "byte", "AB==", ""},
		{"byte-nonzero-padding-bits-one", "byte", "AAB=", ""},
		{"byte-missing-padding", "byte", "Zg", "format"},
		{"byte-url-alphabet", "byte", "AA-A", "format"},
		{"byte-line-feed", "byte", "YW\nJj", "format"},
		{"byte-invalid-padding", "byte", "AA=A", "format"},
		{"uuid-uppercase", "uuid", "123E4567-E89B-12D3-A456-426614174000", ""},
		{"uuid-without-hyphens", "uuid", "123e4567e89b12d3a456426614174000", ""},
		{"uuid-partial-hyphens", "uuid", "123e4567e89b-12d3a456426614174000", ""},
		{"uuid-zero", "uuid", "00000000-0000-0000-0000-000000000000", ""},
		{"uuid-invalid-hex", "uuid", "123e4567-e89b-12d3-a456-42661417400g", "format"},
		{"uuid-too-short", "uuid", "123e4567-e89b-12d3-a456-42661417400", "format"},
		{"nullable-null", "nullable", nil, ""},
		{"nullable-valid-string", "nullable", "2000-02-29", ""},
		{"nullable-invalid-string", "nullable", "PRIVATE_FORMAT_DRAFT_NULLABLE", "format"},
		{"string-type-integer", "date", int64(7), "type"},
		{"string-type-boolean", "byte", true, "type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			createDryRun(t, tc.field, tc.value, tc.code)
		})
	}
	for _, tc := range []struct {
		name, body, value string
		invalid           bool
	}{
		{"live-valid-dry-run", `{"spec":{"date":"2028-02-29"}}`, "2028-02-29", false},
		{"live-invalid-dry-run", `{"spec":{"date":"2025-02-29"}}`, "2025-02-29", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft := f.Live.DeepCopy()
			draft.Object["spec"].(map[string]any)["date"] = tc.value
			code := ""
			if tc.invalid {
				code = "format"
			}
			check(t, draft, "date", code)
			result, err := f.Backend.PatchObject(ctx, f.GVR, f.Namespace, f.Live.GetName(), types.MergePatchType, []byte(tc.body), metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "schema-format-dry-run", FieldValidation: "Strict"})
			if tc.invalid {
				rejected(t, result, err, "date")
			} else if err != nil || result == nil || result.GetUID() != f.Live.GetUID() || result.GetName() != f.Live.GetName() || result.GetNamespace() != f.Namespace || result.GroupVersionKind() != f.GVK || !reflect.DeepEqual(result.Object["spec"], draft.Object["spec"]) {
				t.Fatalf("live dry-run did not preview the same UID and intended string draft: %v", err)
			}
			assertLiveUnchanged(t)
		})
	}
	assertLiveUnchanged(t)
}
