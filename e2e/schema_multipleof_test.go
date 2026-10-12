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

func TestRealSchemaMultipleOfAgreeWithDryRun(t *testing.T) {
	f := testcluster.NewSchemaMultiples(t)
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
		t.Fatal("same-identity fresh discovery did not advertise the multipleOf CRD")
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
			t.Fatal("read-only multipleOf inspection changed its draft")
		}
		if code == "" {
			if len(report.Diagnostics) != 0 {
				t.Fatalf("exact multiple produced a local constraint hint: %s", report.Text())
			}
		} else if len(report.Diagnostics) != 1 || report.Diagnostics[0].Pointer != "/spec/"+field || report.Diagnostics[0].Code != code {
			t.Fatalf("published multipleOf was not inspected at its actual field: %s", report.Text())
		}
		if strings.Contains(report.Text(), "0.30000000001") || !strings.Contains(report.Text(), "Server dry-run remains required") {
			t.Fatal("multipleOf report echoed a numeric value or omitted the server boundary")
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
		t.Fatal("server rejection did not identify the locally diagnosed multipleOf field")
	}
	assertLiveUnchanged := func(t *testing.T) {
		t.Helper()
		after, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, f.Live.GetName())
		if err != nil || after == nil || after.GetUID() != f.Live.GetUID() || after.GetResourceVersion() != f.Live.GetResourceVersion() || !reflect.DeepEqual(after.Object["spec"], f.Live.Object["spec"]) {
			t.Fatalf("multipleOf inspection or dry-run changed the live fixture: %v", err)
		}
	}
	nextName := 0
	createDryRun := func(t *testing.T, field string, value any, code string, serverAccepts bool) {
		t.Helper()
		nextName++
		draft := f.Draft(fmt.Sprintf("schema-multiple-%02d", nextName))
		if field != "" {
			draft.Object["spec"].(map[string]any)[field] = value
		}
		check(t, draft, field, code)
		result, err := f.Backend.CreateObject(ctx, f.GVR, f.Namespace, draft, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "schema-multiple-dry-run", FieldValidation: "Strict"})
		if serverAccepts {
			if err != nil || result == nil || result.GetName() != draft.GetName() || result.GetNamespace() != f.Namespace || result.GroupVersionKind() != f.GVK {
				t.Fatalf("server rejected the expected dry-run boundary: %v", err)
			}
			if !reflect.DeepEqual(result.Object["spec"], draft.Object["spec"]) {
				t.Fatal("server dry-run did not preserve the tested multipleOf fields")
			}
		} else {
			rejected(t, result, err, field)
		}
		if _, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, draft.GetName()); !apierrors.IsNotFound(err) {
			t.Fatalf("checked/dry-run multipleOf draft unexpectedly persisted: %v", err)
		}
		assertLiveUnchanged(t)
	}
	// This agreement group uses binary-exact factors and values. Zero and
	// integer wire values exercise the integer factor; fractional factors use
	// non-integral JSON values so this does not claim all server numeric
	// conversions behave like the local decimal inspector.
	for _, tc := range []struct {
		name, field string
		value       any
		invalid     bool
	}{
		{"valid-defaults", "", nil, false},
		{"integer-zero", "even", int64(0), false},
		{"integer-positive", "even", int64(4), false},
		{"integer-negative", "even", int64(-4), false},
		{"integer-large-positive", "even", int64(1 << 40), false},
		{"integer-large-negative", "even", int64(-1 << 40), false},
		{"integer-positive-nonmultiple", "even", int64(3), true},
		{"integer-negative-nonmultiple", "even", int64(-3), true},
		{"quarter-first", "quarter", 0.25, false},
		{"quarter-positive", "quarter", 1.25, false},
		{"quarter-negative", "quarter", -0.75, false},
		{"quarter-positive-nonmultiple", "quarter", 0.625, true},
		{"quarter-negative-nonmultiple", "quarter", -0.625, true},
		{"half-first", "half", 0.5, false},
		{"half-positive", "half", 2.5, false},
		{"half-negative", "half", -0.5, false},
		{"half-positive-nonmultiple", "half", 0.75, true},
		{"half-negative-nonmultiple", "half", -0.75, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := ""
			if tc.invalid {
				code = "multipleOf"
			}
			createDryRun(t, tc.field, tc.value, code, !tc.invalid)
		})
	}
	t.Run("decimal-reciprocal-tenth", func(t *testing.T) {
		createDryRun(t, "tenth", 0.3, "", true)
	})
	t.Run("server-epsilon-accepts-near-multiple", func(t *testing.T) {
		// Kubernetes' vendored MultipleOf uses reciprocal multiplication for
		// factors below one, followed by swag's tolerant integer predicate.
		// This is deliberately outside the agreement group: the local exact
		// decimal rule rejects a near multiple that these server versions
		// accept. A live strict dry-run, not a reimplementation, proves it.
		// https://github.com/kubernetes/kubernetes/blob/v1.37.0/vendor/k8s.io/kube-openapi/pkg/validation/validate/values.go
		// https://github.com/kubernetes/kubernetes/blob/v1.37.0/vendor/github.com/go-openapi/swag/conv/convert.go
		createDryRun(t, "tenth", 0.30000000001, "multipleOf", true)
		t.Log("exact decimal multipleOf hint retained; real server strict dry-run accepted its float tolerance boundary; no object persisted")
	})
	for _, tc := range []struct {
		name, body string
		value      float64
		invalid    bool
	}{
		{"live-valid-dry-run", `{"spec":{"quarter":0.5}}`, 0.5, false},
		{"live-invalid-dry-run", `{"spec":{"quarter":0.625}}`, 0.625, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft := f.Live.DeepCopy()
			draft.Object["spec"].(map[string]any)["quarter"] = tc.value
			code := ""
			if tc.invalid {
				code = "multipleOf"
			}
			check(t, draft, "quarter", code)
			result, err := f.Backend.PatchObject(ctx, f.GVR, f.Namespace, f.Live.GetName(), types.MergePatchType, []byte(tc.body), metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "schema-multiple-dry-run", FieldValidation: "Strict"})
			if tc.invalid {
				rejected(t, result, err, "quarter")
			} else if err != nil || result == nil || result.GetUID() != f.Live.GetUID() || result.GroupVersionKind() != f.GVK || !reflect.DeepEqual(result.Object["spec"], draft.Object["spec"]) {
				t.Fatalf("live dry-run did not preview the same UID and intended numeric draft: %v", err)
			}
			assertLiveUnchanged(t)
		})
	}
	assertLiveUnchanged(t)
}
