//go:build e2e

package uiworkbench

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/testcluster"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestNativeSchemaListSetAgainstRealCluster(t *testing.T) {
	runNativeSchemaListSetAgainstRealCluster(t, "string")
}

func TestNativeSchemaIntegerSetAgainstRealCluster(t *testing.T) {
	runNativeSchemaListSetAgainstRealCluster(t, "integer")
}

func runNativeSchemaListSetAgainstRealCluster(t *testing.T, kind string) {
	t.Helper()
	var f testcluster.SchemaListSetsFixture
	if kind == "integer" {
		f = testcluster.NewSchemaIntegerSets(t)
	} else if kind == "boolean" {
		f = testcluster.NewSchemaBooleanSets(t)
	} else {
		f = testcluster.NewSchemaListSets(t)
	}
	h := newRelationshipHarness(t, 90*time.Second)
	h.w.path = os.Getenv("KUBECONFIG")
	h.w.currentContext = os.Getenv("ASTER_E2E_CONTEXT")
	h.w.contexts = []string{h.w.currentContext}
	h.w.namespace = f.Namespace
	h.tt.Frame()
	h.click("Connect")
	label, choiceIndex := "", -1
	h.pump(func() bool {
		for i, kind := range h.w.kinds {
			if kind.GVR == f.GVR && kind.Kind == f.GVK.Kind && kind.Namespaced {
				label, choiceIndex = kind.Label(), i
				return true
			}
		}
		return false
	})
	h.click("Resource kind")
	if !h.tt.Focused("Resource kind") {
		t.Fatal("resource kind select did not receive native focus")
	}
	h.tt.Key(0, ui.KeyHome)
	for range choiceIndex {
		h.tt.Key(0, ui.KeyDown)
	}
	h.tt.Key(0, ui.KeyEnter)
	if h.w.kindChoice != label || h.w.currentKind.GVR != f.GVR || h.w.currentKind.Kind != f.GVK.Kind || !h.w.currentKind.Namespaced {
		t.Fatal("native select did not choose the actually discovered list-set CRD")
	}
	h.pump(func() bool { return containsRowUID(h.w.rows, string(f.Live.GetUID())) })
	h.click(f.Live.GetName())
	h.pump(func() bool { return h.w.detail != nil })
	if h.w.detail.GroupVersionKind() != f.GVK || h.w.detail.GetName() != f.Live.GetName() || h.w.detail.GetNamespace() != f.Namespace || h.w.detail.GetUID() != f.Live.GetUID() || h.w.detail.GetResourceVersion() != f.Live.GetResourceVersion() {
		t.Fatal("native discovery opened a different live list-set resource identity")
	}
	h.click("Edit")
	buffer, original := h.w.editor, h.w.editor.Snapshot()
	obj, err := manifest.Decode([]byte(h.w.editorText()))
	if err != nil {
		t.Fatal(err)
	}
	fields := obj.Object["spec"].(map[string]any)
	fields["values"].([]any)[1] = "api"
	fields["defaulted"] = []any{"PRIVATE_DEFAULTED", "PRIVATE_DEFAULTED"}
	if kind == "integer" {
		fields["values"].([]any)[1] = int64(0)
		fields["defaulted"] = []any{int64(42), int64(42)}
	}
	if kind == "boolean" {
		fields["values"] = []any{false, false}
		fields["defaulted"] = []any{true, true}
	}
	// Retain all live metadata and the nullable parent. Flow-style arrays
	// allow correction at the reported parent pointer using ordinary input.
	delete(obj.Object, "spec")
	header, err := manifest.Display(obj, false)
	if err != nil {
		t.Fatal(err)
	}
	fieldLine := func(name string, value any) string {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return name + ": " + string(data)
	}
	draft := "# 中文 🚢 é — PRIVATE_NATIVE_LIST_SET_SOURCE\n" + header + "spec:\n"
	for _, name := range []string{"values", "defaulted", "nullable"} {
		draft += "  " + fieldLine(name, fields[name]) + "\n"
	}
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(draft)
	snapshot, identity := h.w.editor.Snapshot(), h.w.draftIdentity()
	decoded, err := manifest.Decode([]byte(h.w.editorText()))
	if err != nil || decoded == nil || !reflect.DeepEqual(decoded.Object["spec"], fields) {
		t.Fatalf("native list-set input did not retain complete decoded strings and nullable parent: %v", err)
	}
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/values · list-set") || !strings.Contains(h.w.schemaText, "/spec/defaulted · list-set") || len(h.w.schemaLocations) != 2 {
		t.Fatal("native inspection did not diagnose the actual published string set lists")
	}
	if kind != "string" && !strings.Contains(h.w.schemaText, "string/int64/boolean set-list values") {
		t.Fatal("integer set report scope is missing")
	}
	for _, private := range []string{"9007199254740993", "9223372036854775807", "PRIVATE_NATIVE_LIST_SET_SOURCE", "PRIVATE_DEFAULTED", "中文船🚢", `"api"`} {
		if strings.Contains(h.w.schemaText+h.w.schemaStatus, private) {
			t.Fatal("native list-set assistance echoed a string value")
		}
		for _, location := range h.w.schemaLocations {
			if strings.Contains(location.label, private) {
				t.Fatal("native list-set source location echoed a string value")
			}
		}
	}
	if h.w.editor != buffer || h.w.editorText() != draft || h.w.draftIdentity() != identity || snapshot.String() != draft || original.String() != h.w.detailText || h.w.plan != nil || h.w.pendingWrites != 0 || len(h.w.history) != 0 {
		t.Fatal("advisory list-set inspection changed source, identity, history or operation state")
	}
	saveNativeScreenshot(t, h.tt)
	assertLiveUnchanged := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		after, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, f.Live.GetName())
		if err != nil || after == nil || after.GroupVersionKind() != f.GVK || after.GetName() != f.Live.GetName() || after.GetNamespace() != f.Namespace || after.GetUID() != f.Live.GetUID() || after.GetResourceVersion() != f.Live.GetResourceVersion() || !reflect.DeepEqual(after.Object["spec"], f.Live.Object["spec"]) {
			t.Fatalf("native list-set inspection or dry-run changed live identity or complete values: %v", err)
		}
	}
	assertLiveUnchanged()
	h.click("Preview edit")
	h.pump(func() bool { return !h.w.preparing })
	if h.w.plan != nil || h.w.detailMode != "Edit" || !strings.Contains(h.w.detailMessage, "is invalid") || !strings.Contains(h.w.detailMessage, "spec.values") || !strings.Contains(h.w.detailMessage, "spec.defaulted") || h.w.editorText() != draft || h.w.pendingWrites != 0 || len(h.w.history) != 0 {
		t.Fatal("native duplicate-list preview did not report both real server field errors without a plan/write")
	}
	assertLiveUnchanged()
	validSpec := f.Draft("schema-list-set-preview-values").Object["spec"].(map[string]any)
	validSpec["values"] = []any{"api", "中文船🚢", "", "é", "e\u0301", "x\x00y"}
	validSpec["defaulted"] = []any{"explicit", "fallback"}
	if kind == "integer" {
		validSpec["values"] = []any{int64(0), int64(9007199254740992), int64(9007199254740993), int64(-9223372036854775808), int64(9223372036854775807)}
		validSpec["defaulted"] = []any{int64(7), int64(42)}
	}
	if kind == "boolean" {
		validSpec["values"] = []any{true, false}
		validSpec["defaulted"] = []any{false, true}
	}
	target := chooseDiagnostic(h, "/spec/defaulted")
	if !target.position.Exact || !strings.HasPrefix(string([]rune(draft)[target.position.RuneOffset:]), "defaulted:") {
		t.Fatal("defaulted-string diagnostic did not retain its original parent array's Unicode source coordinate")
	}
	h.click("Go to location")
	if !h.tt.Focused("Manifest editor") || h.w.editorText() != draft || h.w.plan != nil {
		t.Fatal("defaulted-string navigation changed source or did not focus its parent array")
	}
	selectNativeSchemaEnumLine(t, h.tt, fieldLine("defaulted", fields["defaulted"]))
	h.tt.Type(fieldLine("defaulted", validSpec["defaulted"]))
	defaultedCorrected := h.w.editorText()
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/values · list-set") || strings.Contains(h.w.schemaText, "/spec/defaulted · list-set") || len(h.w.schemaLocations) != 1 || h.w.editorText() != defaultedCorrected || h.w.plan != nil {
		t.Fatal("native defaulted-string correction did not refresh the actual parent-array diagnostic")
	}
	target = chooseDiagnostic(h, "/spec/values")
	if !target.position.Exact || !strings.HasPrefix(string([]rune(defaultedCorrected)[target.position.RuneOffset:]), "values:") {
		t.Fatal("string-value diagnostic lost its refreshed parent source coordinate")
	}
	h.click("Go to location")
	selectNativeSchemaEnumLine(t, h.tt, fieldLine("values", fields["values"]))
	h.tt.Type(fieldLine("values", validSpec["values"]))
	corrected := h.w.editorText()
	decoded, err = manifest.Decode([]byte(corrected))
	if err != nil || decoded == nil || !reflect.DeepEqual(decoded.Object["spec"], validSpec) {
		t.Fatalf("native list-set corrections lost complete decoded string values, nullable parent or array order: %v", err)
	}
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if len(h.w.schemaLocations) != 0 || !strings.Contains(h.w.schemaText, "No supported structural or constraint hints found.") || h.w.editorText() != corrected || h.w.plan != nil {
		t.Fatal("native distinct string values were not accepted as read-only assistance")
	}
	// The source widget remains mounted throughout this history sequence.
	// The later Diff view does not imply history survival across unmounting.
	focusMultipleOfEditorByKeyboard(h)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != defaultedCorrected {
		t.Fatal("checking the string-value correction broke mounted native Undo")
	}
	h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if h.w.editorText() != corrected {
		t.Fatal("checking the string-value correction broke mounted native Redo")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != draft {
		t.Fatal("list-set checks lost the preceding defaulted-string correction history")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != original.String() || h.w.editor != buffer || snapshot.String() != draft || h.w.plan != nil {
		t.Fatal("list-set checks lost the original live-document history")
	}
	for range 3 {
		h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	}
	if h.w.editorText() != corrected || h.w.editor != buffer || h.w.plan != nil {
		t.Fatal("native list-set correction sequence did not survive Redo")
	}
	h.click("Preview edit")
	h.pump(func() bool { return !h.w.preparing })
	if h.w.plan == nil || h.w.plan.Kind() != "edit" || h.w.plan.Target().UID != f.Live.GetUID() || h.w.detailMode != "Diff" || h.w.pendingWrites != 0 || len(h.w.history) != 0 {
		t.Fatal("corrected list-set draft did not produce an explicit read-only server preview")
	}
	_, afterBytes := h.w.plan.Preview()
	after := &unstructured.Unstructured{}
	if err := after.UnmarshalJSON(afterBytes); err != nil {
		t.Fatal(err)
	}
	if after.GroupVersionKind() != f.GVK || after.GetName() != f.Live.GetName() || after.GetNamespace() != f.Namespace || after.GetUID() != f.Live.GetUID() || !reflect.DeepEqual(after.Object["spec"], validSpec) || !strings.Contains(h.w.diff, "/spec/values") || !strings.Contains(h.w.diff, "/spec/defaulted") {
		t.Fatal("actual native server preview lost complete corrected strings or nullable parent")
	}
	assertLiveUnchanged()
	h.click("Disconnect")
	if h.w.backend != nil || h.w.plan != nil || h.w.schemaSource != nil || h.w.schemaJump != nil || len(h.w.schemaLocations) != 0 || h.w.schemaText != "" {
		t.Fatal("disconnect retained list-set assistance or its reviewed plan")
	}
}

func TestNativeSchemaBooleanSetAgainstRealCluster(t *testing.T) {
	runNativeSchemaListSetAgainstRealCluster(t, "boolean")
}
