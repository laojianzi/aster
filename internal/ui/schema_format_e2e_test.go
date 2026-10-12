//go:build e2e

package uiworkbench

import (
	"context"
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

func TestNativeSchemaFormatAgainstRealCluster(t *testing.T) {
	f := testcluster.NewSchemaFormats(t)
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
	// Navigate the real discovered index, including choices outside the popup.
	h.tt.Key(0, ui.KeyHome)
	for range choiceIndex {
		h.tt.Key(0, ui.KeyDown)
	}
	h.tt.Key(0, ui.KeyEnter)
	if h.w.kindChoice != label || h.w.currentKind.GVR != f.GVR || h.w.currentKind.Kind != f.GVK.Kind || !h.w.currentKind.Namespaced {
		t.Fatal("native select did not choose the actually discovered format CRD")
	}
	h.pump(func() bool { return containsRowUID(h.w.rows, string(f.Live.GetUID())) })
	h.click(f.Live.GetName())
	h.pump(func() bool { return h.w.detail != nil })
	if h.w.detail.GroupVersionKind() != f.GVK || h.w.detail.GetName() != f.Live.GetName() || h.w.detail.GetNamespace() != f.Namespace || h.w.detail.GetUID() != f.Live.GetUID() || h.w.detail.GetResourceVersion() != f.Live.GetResourceVersion() {
		t.Fatal("native discovery opened a different live format resource identity")
	}
	h.click("Edit")
	buffer, original := h.w.editor, h.w.editor.Snapshot()
	obj, err := manifest.Decode([]byte(h.w.editorText()))
	if err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedField(obj.Object, "2023-02-29", "spec", "date"); err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedField(obj.Object, "PRIVATE_NATIVE_FORMAT_VALUE", "spec", "byte"); err != nil {
		t.Fatal(err)
	}
	draft, err := manifest.Display(obj, false)
	if err != nil {
		t.Fatal(err)
	}
	draft = "# 中文 🚢 é — PRIVATE_NATIVE_FORMAT_SOURCE\n" + draft
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(draft)
	snapshot, identity := h.w.editor.Snapshot(), h.w.draftIdentity()
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/date · format") || !strings.Contains(h.w.schemaText, "/spec/byte · format") || len(h.w.schemaLocations) != 2 {
		t.Fatal("native inspection did not diagnose the actual published date and byte formats")
	}
	for _, private := range []string{"PRIVATE_NATIVE_FORMAT_SOURCE", "PRIVATE_NATIVE_FORMAT_VALUE", "2023-02-29", "123e4567-e89b-12d3-a456-426614174000"} {
		if strings.Contains(h.w.schemaText+h.w.schemaStatus, private) {
			t.Fatal("native format assistance echoed a source value")
		}
		for _, location := range h.w.schemaLocations {
			if strings.Contains(location.label, private) {
				t.Fatal("native format location echoed a source value")
			}
		}
	}
	if h.w.editor != buffer || h.w.editorText() != draft || h.w.draftIdentity() != identity || snapshot.String() != draft || original.String() != h.w.detailText || h.w.plan != nil || h.w.pendingWrites != 0 || len(h.w.history) != 0 {
		t.Fatal("advisory format inspection changed input, identity, history or operation state")
	}
	saveNativeScreenshot(t, h.tt)
	assertLiveUnchanged := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		after, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, f.Live.GetName())
		if err != nil || after == nil || after.GroupVersionKind() != f.GVK || after.GetName() != f.Live.GetName() || after.GetNamespace() != f.Namespace || after.GetUID() != f.Live.GetUID() || after.GetResourceVersion() != f.Live.GetResourceVersion() || !reflect.DeepEqual(after.Object["spec"], f.Live.Object["spec"]) {
			t.Fatalf("native format inspection or dry-run changed the live CRD identity or values: %v", err)
		}
	}
	assertLiveUnchanged()
	// Require the server's two specific field errors. A transport or permission
	// failure is not evidence that these advertised formats were enforced.
	h.click("Preview edit")
	h.pump(func() bool { return !h.w.preparing })
	if h.w.plan != nil || h.w.detailMode != "Edit" || !strings.Contains(h.w.detailMessage, "is invalid") || !strings.Contains(h.w.detailMessage, "spec.date") || !strings.Contains(h.w.detailMessage, "spec.byte") || h.w.editorText() != draft || h.w.pendingWrites != 0 || len(h.w.history) != 0 {
		t.Fatal("native invalid format preview did not report server field validation without a plan/write")
	}
	assertLiveUnchanged()
	target := chooseDiagnostic(h, "/spec/byte")
	if !target.position.Exact || !strings.HasPrefix(string([]rune(draft)[target.position.RuneOffset:]), "byte:") {
		t.Fatal("published format did not retain the original Unicode source coordinate")
	}
	h.click("Go to location")
	if !h.tt.Focused("Manifest editor") || h.w.editorText() != draft || h.w.plan != nil {
		t.Fatal("native format navigation changed the draft or plan")
	}
	h.tt.Key(ui.Shift, ui.KeyEnd)
	// Kubernetes validates this base64 shape without canonical padding bits.
	h.tt.Type("byte: AB==")
	byteCorrected := h.w.editorText()
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/date · format") || strings.Contains(h.w.schemaText, "/spec/byte · format") || len(h.w.schemaLocations) != 1 || h.w.editorText() != byteCorrected || h.w.plan != nil {
		t.Fatal("native correction did not refresh the actual byte format constraint")
	}
	target = chooseDiagnostic(h, "/spec/date")
	if !target.position.Exact || !strings.HasPrefix(string([]rune(byteCorrected)[target.position.RuneOffset:]), "date:") {
		t.Fatal("date format diagnostic lost its refreshed source coordinate")
	}
	h.click("Go to location")
	h.tt.Key(ui.Shift, ui.KeyEnd)
	// Use a valid date distinct from the live value so both changes reach Diff.
	h.tt.Type("date: '2000-02-29'")
	corrected := h.w.editorText()
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if len(h.w.schemaLocations) != 0 || !strings.Contains(h.w.schemaText, "No supported structural or constraint hints found.") || h.w.editorText() != corrected || h.w.plan != nil {
		t.Fatal("native valid formats were not accepted as read-only assistance")
	}
	// These assertions all precede Diff, while the source widget is mounted.
	// Native history across a later editor unmount is not claimed here.
	focusMultipleOfEditorByKeyboard(h)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != byteCorrected {
		t.Fatal("checking the CRD format correction broke native Undo")
	}
	h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if h.w.editorText() != corrected {
		t.Fatal("checking the CRD format correction broke native Redo")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != draft {
		t.Fatal("native format checks lost preceding correction history")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != original.String() || h.w.editor != buffer || snapshot.String() != draft || h.w.plan != nil {
		t.Fatal("native format checks lost the original live-document history")
	}
	for range 3 {
		h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	}
	if h.w.editorText() != corrected || h.w.editor != buffer || h.w.plan != nil {
		t.Fatal("native format correction sequence did not survive Redo")
	}
	h.click("Preview edit")
	h.pump(func() bool { return !h.w.preparing })
	if h.w.plan == nil || h.w.plan.Kind() != "edit" || h.w.plan.Target().UID != f.Live.GetUID() || h.w.detailMode != "Diff" || h.w.pendingWrites != 0 || len(h.w.history) != 0 {
		t.Fatal("corrected native format draft did not produce an explicit read-only server preview")
	}
	_, afterBytes := h.w.plan.Preview()
	after := &unstructured.Unstructured{}
	if err := after.UnmarshalJSON(afterBytes); err != nil {
		t.Fatal(err)
	}
	date, dateFound, dateErr := unstructured.NestedString(after.Object, "spec", "date")
	bytes, byteFound, byteErr := unstructured.NestedString(after.Object, "spec", "byte")
	uuid, uuidFound, uuidErr := unstructured.NestedString(after.Object, "spec", "uuid")
	nullable, nullableFound, nullableErr := unstructured.NestedFieldNoCopy(after.Object, "spec", "nullable")
	if after.GroupVersionKind() != f.GVK || after.GetName() != f.Live.GetName() || after.GetNamespace() != f.Namespace || after.GetUID() != f.Live.GetUID() || dateErr != nil || !dateFound || date != "2000-02-29" || byteErr != nil || !byteFound || bytes != "AB==" || uuidErr != nil || !uuidFound || uuid != "123e4567-e89b-12d3-a456-426614174000" || nullableErr != nil || !nullableFound || nullable != nil || !strings.Contains(h.w.diff, "/spec/date") || !strings.Contains(h.w.diff, "/spec/byte") {
		t.Fatal("actual native server preview lost corrected identity/values or unchanged UUID/nullable fields")
	}
	assertLiveUnchanged()
	h.click("Disconnect")
	if h.w.backend != nil || h.w.plan != nil || h.w.schemaSource != nil || h.w.schemaJump != nil || len(h.w.schemaLocations) != 0 || h.w.schemaText != "" {
		t.Fatal("disconnect retained format assistance or its reviewed plan")
	}
}
