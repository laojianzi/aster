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

func TestNativeSchemaListMapAgainstRealCluster(t *testing.T) {
	runNativeSchemaListMapAgainstRealCluster(t, false)
}

func TestNativeSchemaIntegerMapKeysAgainstRealCluster(t *testing.T) {
	runNativeSchemaListMapAgainstRealCluster(t, true)
}

func runNativeSchemaListMapAgainstRealCluster(t *testing.T, integerKeys bool) {
	t.Helper()
	var f testcluster.SchemaListMapsFixture
	var first, correctedKey any = "api", "batch"
	if integerKeys {
		f = testcluster.NewSchemaIntegerListMaps(t)
		first, correctedKey = int64(8080), int64(10000)
	} else {
		f = testcluster.NewSchemaListMaps(t)
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
		t.Fatal("native select did not choose the actually discovered list-map CRD")
	}
	h.pump(func() bool { return containsRowUID(h.w.rows, string(f.Live.GetUID())) })
	h.click(f.Live.GetName())
	h.pump(func() bool { return h.w.detail != nil })
	if h.w.detail.GroupVersionKind() != f.GVK || h.w.detail.GetName() != f.Live.GetName() || h.w.detail.GetNamespace() != f.Namespace || h.w.detail.GetUID() != f.Live.GetUID() || h.w.detail.GetResourceVersion() != f.Live.GetResourceVersion() {
		t.Fatal("native discovery opened a different live list-map resource identity")
	}
	h.click("Edit")
	buffer, original := h.w.editor, h.w.editor.Snapshot()
	obj, err := manifest.Decode([]byte(h.w.editorText()))
	if err != nil {
		t.Fatal(err)
	}
	fields := obj.Object["spec"].(map[string]any)
	fields["named"].([]any)[1].(map[string]any)["name"] = first
	fields["scoped"].([]any)[1].(map[string]any)["zone"] = "east"
	// Retain all live metadata and every non-key field. Flow-style arrays
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
	draft := "# 中文 🚢 é — PRIVATE_NATIVE_LIST_MAP_SOURCE\n" + header + "spec:\n"
	for _, name := range []string{"named", "scoped"} {
		draft += "  " + fieldLine(name, fields[name]) + "\n"
	}
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(draft)
	snapshot, identity := h.w.editor.Snapshot(), h.w.draftIdentity()
	decoded, err := manifest.Decode([]byte(h.w.editorText()))
	if err != nil || decoded == nil || !reflect.DeepEqual(decoded.Object["spec"], fields) {
		t.Fatalf("native list-map input did not retain complete tuples and non-key values: %v", err)
	}
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/named · list-map-keys") || !strings.Contains(h.w.schemaText, "/spec/scoped · list-map-keys") || len(h.w.schemaLocations) != 2 {
		t.Fatal("native inspection did not diagnose the actual published single-key and tuple-key list maps")
	}
	for _, private := range []string{"PRIVATE_NATIVE_LIST_MAP_SOURCE", "east", "west", "worker", `"api"`, `"one"`, `"two"`, "8080", "9090", "10000"} {
		if strings.Contains(h.w.schemaText+h.w.schemaStatus, private) {
			t.Fatal("native list-map assistance echoed an identity tuple or non-key value")
		}
		for _, location := range h.w.schemaLocations {
			if strings.Contains(location.label, private) {
				t.Fatal("native list-map source location echoed an identity tuple or non-key value")
			}
		}
	}
	if h.w.editor != buffer || h.w.editorText() != draft || h.w.draftIdentity() != identity || snapshot.String() != draft || original.String() != h.w.detailText || h.w.plan != nil || h.w.pendingWrites != 0 || len(h.w.history) != 0 {
		t.Fatal("advisory list-map inspection changed source, identity, history or operation state")
	}
	saveNativeScreenshot(t, h.tt)
	assertLiveUnchanged := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		after, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, f.Live.GetName())
		if err != nil || after == nil || after.GroupVersionKind() != f.GVK || after.GetName() != f.Live.GetName() || after.GetNamespace() != f.Namespace || after.GetUID() != f.Live.GetUID() || after.GetResourceVersion() != f.Live.GetResourceVersion() || !reflect.DeepEqual(after.Object["spec"], f.Live.Object["spec"]) {
			t.Fatalf("native list-map inspection or dry-run changed live identity or complete values: %v", err)
		}
	}
	assertLiveUnchanged()
	h.click("Preview edit")
	h.pump(func() bool { return !h.w.preparing })
	if h.w.plan != nil || h.w.detailMode != "Edit" || !strings.Contains(h.w.detailMessage, "is invalid") || !strings.Contains(h.w.detailMessage, "spec.named") || !strings.Contains(h.w.detailMessage, "spec.scoped") || h.w.editorText() != draft || h.w.pendingWrites != 0 || len(h.w.history) != 0 {
		t.Fatal("native duplicate-list preview did not report both real server field errors without a plan/write")
	}
	assertLiveUnchanged()
	validSpec := f.Draft("schema-list-map-preview-values").Object["spec"].(map[string]any)
	validSpec["named"].([]any)[1].(map[string]any)["name"] = correctedKey
	validSpec["scoped"].([]any)[1].(map[string]any)["zone"] = "north"
	target := chooseDiagnostic(h, "/spec/scoped")
	if !target.position.Exact || !strings.HasPrefix(string([]rune(draft)[target.position.RuneOffset:]), "scoped:") {
		t.Fatal("tuple-key diagnostic did not retain its original parent array's Unicode source coordinate")
	}
	h.click("Go to location")
	if !h.tt.Focused("Manifest editor") || h.w.editorText() != draft || h.w.plan != nil {
		t.Fatal("tuple-key navigation changed source or did not focus its parent array")
	}
	selectNativeSchemaEnumLine(t, h.tt, fieldLine("scoped", fields["scoped"]))
	h.tt.Type(fieldLine("scoped", validSpec["scoped"]))
	scopedCorrected := h.w.editorText()
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/named · list-map-keys") || strings.Contains(h.w.schemaText, "/spec/scoped · list-map-keys") || len(h.w.schemaLocations) != 1 || h.w.editorText() != scopedCorrected || h.w.plan != nil {
		t.Fatal("native tuple-key correction did not refresh the actual parent-array diagnostic")
	}
	target = chooseDiagnostic(h, "/spec/named")
	if !target.position.Exact || !strings.HasPrefix(string([]rune(scopedCorrected)[target.position.RuneOffset:]), "named:") {
		t.Fatal("single-key diagnostic lost its refreshed parent source coordinate")
	}
	h.click("Go to location")
	selectNativeSchemaEnumLine(t, h.tt, fieldLine("named", fields["named"]))
	h.tt.Type(fieldLine("named", validSpec["named"]))
	corrected := h.w.editorText()
	decoded, err = manifest.Decode([]byte(corrected))
	if err != nil || decoded == nil || !reflect.DeepEqual(decoded.Object["spec"], validSpec) {
		t.Fatalf("native list-map corrections lost complete tuple/non-key fields or array order: %v", err)
	}
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if len(h.w.schemaLocations) != 0 || !strings.Contains(h.w.schemaText, "No supported structural or constraint hints found.") || h.w.editorText() != corrected || h.w.plan != nil {
		t.Fatal("native distinct tuple identities were not accepted as read-only assistance")
	}
	// The source widget remains mounted throughout this history sequence.
	// The later Diff view does not imply history survival across unmounting.
	focusMultipleOfEditorByKeyboard(h)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != scopedCorrected {
		t.Fatal("checking the single-key correction broke mounted native Undo")
	}
	h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if h.w.editorText() != corrected {
		t.Fatal("checking the single-key correction broke mounted native Redo")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != draft {
		t.Fatal("list-map checks lost the preceding tuple-key correction history")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != original.String() || h.w.editor != buffer || snapshot.String() != draft || h.w.plan != nil {
		t.Fatal("list-map checks lost the original live-document history")
	}
	for range 3 {
		h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	}
	if h.w.editorText() != corrected || h.w.editor != buffer || h.w.plan != nil {
		t.Fatal("native list-map correction sequence did not survive Redo")
	}
	h.click("Preview edit")
	h.pump(func() bool { return !h.w.preparing })
	if h.w.plan == nil || h.w.plan.Kind() != "edit" || h.w.plan.Target().UID != f.Live.GetUID() || h.w.detailMode != "Diff" || h.w.pendingWrites != 0 || len(h.w.history) != 0 {
		t.Fatal("corrected list-map draft did not produce an explicit read-only server preview")
	}
	_, afterBytes := h.w.plan.Preview()
	after := &unstructured.Unstructured{}
	if err := after.UnmarshalJSON(afterBytes); err != nil {
		t.Fatal(err)
	}
	if after.GroupVersionKind() != f.GVK || after.GetName() != f.Live.GetName() || after.GetNamespace() != f.Namespace || after.GetUID() != f.Live.GetUID() || !reflect.DeepEqual(after.Object["spec"], validSpec) || !strings.Contains(h.w.diff, "/spec/named") || !strings.Contains(h.w.diff, "/spec/scoped") {
		t.Fatal("actual native server preview lost complete corrected identity tuples or non-key fields")
	}
	assertLiveUnchanged()
	h.click("Disconnect")
	if h.w.backend != nil || h.w.plan != nil || h.w.schemaSource != nil || h.w.schemaJump != nil || len(h.w.schemaLocations) != 0 || h.w.schemaText != "" {
		t.Fatal("disconnect retained list-map assistance or its reviewed plan")
	}
}
