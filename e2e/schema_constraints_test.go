//go:build e2e

package e2e

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/testcluster"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRealSchemaConstraintsAgreeWithDryRun(t *testing.T) {
	f := testcluster.NewSchemaConstraints(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	document, err := f.Backend.SchemaDocument(ctx, f.GVK)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, field, code string
		value             any
	}{
		{"valid-defaults", "", "", nil},
		{"valid-minimum", "replicas", "", int64(1)},
		{"valid-maximum", "replicas", "", int64(5)},
		{"valid-string-choice", "mode", "", "secondary"},
		{"valid-number-choice", "choice", "", int64(4)},
		{"valid-length-minimum", "title", "", "e\u0301"},
		{"valid-length-maximum", "title", "", "中🚢e\u0301"},
		{"valid-items-maximum", "items", "", []any{"one", "two"}},
		{"valid-properties-maximum", "attributes", "", map[string]any{"one": "a", "two": "b"}},
		{"enum-string", "mode", "enum", "PRIVATE_DRAFT_CHOICE"},
		{"enum-boolean", "enabled", "enum", false},
		{"enum-number", "choice", "enum", int64(3)},
		{"minimum", "replicas", "minimum", int64(0)},
		{"maximum", "replicas", "maximum", int64(6)},
		{"exclusive-minimum", "exclusive", "minimum", int64(0)},
		{"exclusive-maximum", "exclusive", "maximum", int64(10)},
		{"decimal-minimum", "fraction", "minimum", 0.09},
		{"decimal-maximum", "fraction", "maximum", 0.11},
		{"min-length", "title", "minLength", "🚢"},
		{"max-length", "title", "maxLength", "中文🚢ab"},
		{"min-items", "items", "minItems", []any{}},
		{"max-items", "items", "maxItems", []any{"one", "two", "three"}},
		{"min-properties", "attributes", "minProperties", map[string]any{}},
		{"max-properties", "attributes", "maxProperties", map[string]any{"one": "a", "two": "b", "three": "c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft := f.Draft("schema-" + tc.name)
			if tc.field != "" {
				draft.Object["spec"].(map[string]any)[tc.field] = tc.value
			}
			before := draft.DeepCopy()
			report, err := document.Check(ctx, draft.Object)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(draft.Object, before.Object) {
				t.Fatal("read-only constraint check changed its input")
			}
			// Published metadata may carry additional server semantics. Compare
			// the explicitly published constraints, without calling a local
			// report complete admission validation or relying on builtin bounds.
			if tc.code == "" {
				if len(report.Diagnostics) != 0 {
					t.Fatalf("valid boundary produced local hints: %s", report.Text())
				}
			} else if len(report.Diagnostics) != 1 || report.Diagnostics[0].Pointer != "/spec/"+tc.field || report.Diagnostics[0].Code != tc.code {
				t.Fatalf("published constraint %s was not inspected at its actual field: %s", tc.code, report.Text())
			}
			for _, private := range []string{"PRIVATE_DRAFT_CHOICE", "allowed", "secondary"} {
				if strings.Contains(report.Text(), private) {
					t.Fatal("constraint report exposed a draft value or scalar choice")
				}
			}
			result, dryRunErr := f.Backend.CreateObject(ctx, f.GVR, f.Namespace, draft, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}, FieldValidation: "Strict"})
			if tc.code == "" {
				if dryRunErr != nil || result == nil || result.GetName() != draft.GetName() || result.GroupVersionKind() != f.GVK {
					t.Fatalf("server rejected an accepted published boundary: %v", dryRunErr)
				}
			} else {
				var status apierrors.APIStatus
				if !apierrors.IsInvalid(dryRunErr) || !errors.As(dryRunErr, &status) || status.Status().Details == nil {
					t.Fatalf("expected field validation rejection, got %v", dryRunErr)
				}
				matched := false
				for _, cause := range status.Status().Details.Causes {
					if cause.Field == "spec."+tc.field {
						matched = true
					}
				}
				if !matched || result != nil {
					t.Fatal("server rejection did not identify the locally diagnosed constraint field")
				}
			}
			if _, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, draft.GetName()); !apierrors.IsNotFound(err) {
				t.Fatalf("checked/dry-run draft unexpectedly persisted: %v", err)
			}
		})
	}
	after, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, f.Live.GetName())
	if err != nil || after.GetUID() != f.Live.GetUID() || after.GetResourceVersion() != f.Live.GetResourceVersion() || !reflect.DeepEqual(after.Object["spec"], f.Live.Object["spec"]) {
		t.Fatalf("constraint inspection or dry-run changed the live fixture: %v", err)
	}
}
