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

func TestNativeSchemaMultipleOfAgainstRealCluster(t *testing.T) {
	f := testcluster.NewSchemaMultiples(t)
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
	// Resource kind is a rendered Select popup. Its discovered index may be
	// outside the visible rows, so use its public Home/Down/Enter interaction.
	h.tt.Key(0, ui.KeyHome)
	for range choiceIndex {
		h.tt.Key(0, ui.KeyDown)
	}
	h.tt.Key(0, ui.KeyEnter)
	if h.w.kindChoice != label || h.w.currentKind.GVR != f.GVR || h.w.currentKind.Kind != f.GVK.Kind || !h.w.currentKind.Namespaced {
		t.Fatal("native select did not choose the actually discovered multipleOf CRD")
	}
	h.pump(func() bool { return containsRowUID(h.w.rows, string(f.Live.GetUID())) })
	h.click(f.Live.GetName())
	h.pump(func() bool { return h.w.detail != nil })
	if h.w.detail.GroupVersionKind() != f.GVK || h.w.detail.GetUID() != f.Live.GetUID() || h.w.detail.GetResourceVersion() != f.Live.GetResourceVersion() {
		t.Fatal("native discovery opened a different live multipleOf resource identity")
	}
	h.click("Edit")
	buffer, original := h.w.editor, h.w.editor.Snapshot()
	obj, err := manifest.Decode([]byte(h.w.editorText()))
	if err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedField(obj.Object, int64(3), "spec", "even"); err != nil {
		t.Fatal(err)
	}
	// Both the published factor 0.25 and this nonmultiple 0.625 have exact
	// binary representations. Floating server-tolerance differences have a
	// separate backend regression and are not hidden by this native case.
	if err := unstructured.SetNestedField(obj.Object, 0.625, "spec", "quarter"); err != nil {
		t.Fatal(err)
	}
	draft, err := manifest.Display(obj, false)
	if err != nil {
		t.Fatal(err)
	}
	draft = "# 中文 🚢 é — PRIVATE_NATIVE_MULTIPLEOF_SOURCE\n" + draft
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(draft)
	snapshot, identity := h.w.editor.Snapshot(), h.w.draftIdentity()
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/even · multipleOf") || !strings.Contains(h.w.schemaText, "/spec/quarter · multipleOf") || strings.Contains(h.w.schemaText, "/spec/half · multipleOf") || len(h.w.schemaLocations) != 2 {
		t.Fatal("native inspection did not diagnose the actual published multipleOf fields")
	}
	for _, private := range []string{"PRIVATE_NATIVE_MULTIPLEOF_SOURCE", "0.625", "0.25"} {
		if strings.Contains(h.w.schemaText+h.w.schemaStatus, private) {
			t.Fatal("native multipleOf assistance echoed a source value or advertised divisor")
		}
		for _, location := range h.w.schemaLocations {
			if strings.Contains(location.label, private) {
				t.Fatal("native multipleOf location echoed a source value or divisor")
			}
		}
	}
	if h.w.editor != buffer || h.w.editorText() != draft || h.w.draftIdentity() != identity || snapshot.String() != draft || original.String() != h.w.detailText || h.w.plan != nil || h.w.pendingWrites != 0 || len(h.w.history) != 0 {
		t.Fatal("advisory multipleOf inspection changed input, identity, history or operation state")
	}
	saveNativeScreenshot(t, h.tt)
	assertLiveUnchanged := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		after, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, f.Live.GetName())
		if err != nil || after == nil || after.GetUID() != f.Live.GetUID() || after.GetResourceVersion() != f.Live.GetResourceVersion() || !reflect.DeepEqual(after.Object["spec"], f.Live.Object["spec"]) {
			t.Fatalf("native inspection or dry-run changed the live CRD: %v", err)
		}
	}
	assertLiveUnchanged()
	// This explicit native action reaches the existing strict server dry-run.
	// Require the actual invalid-field response, not an arbitrary failure.
	h.click("Preview edit")
	h.pump(func() bool { return !h.w.preparing })
	if h.w.plan != nil || h.w.detailMode != "Edit" || !strings.Contains(h.w.detailMessage, "is invalid") || !strings.Contains(h.w.detailMessage, "spec.even") || !strings.Contains(h.w.detailMessage, "spec.quarter") || h.w.editorText() != draft || h.w.pendingWrites != 0 || len(h.w.history) != 0 {
		t.Fatal("native invalid multipleOf preview did not report server field validation without a plan/write")
	}
	assertLiveUnchanged()
	target := chooseDiagnostic(h, "/spec/quarter")
	if !target.position.Exact || !strings.HasPrefix(string([]rune(draft)[target.position.RuneOffset:]), "quarter:") {
		t.Fatal("published multipleOf did not retain the original Unicode source coordinate")
	}
	h.click("Go to location")
	if !h.tt.Focused("Manifest editor") || h.w.editorText() != draft || h.w.plan != nil {
		t.Fatal("native multipleOf navigation changed the draft or plan")
	}
	h.tt.Key(ui.Shift, ui.KeyEnd)
	h.tt.Type("quarter: 0.5")
	quarterCorrected := h.w.editorText()
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/even · multipleOf") || strings.Contains(h.w.schemaText, "/spec/quarter · multipleOf") || len(h.w.schemaLocations) != 1 || h.w.editorText() != quarterCorrected || h.w.plan != nil {
		t.Fatal("native correction did not refresh the actual published quarter constraint")
	}
	chooseDiagnostic(h, "/spec/even")
	h.click("Go to location")
	h.tt.Key(ui.Shift, ui.KeyEnd)
	h.tt.Type("even: 4")
	corrected := h.w.editorText()
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if len(h.w.schemaLocations) != 0 || !strings.Contains(h.w.schemaText, "No supported structural or constraint hints found.") || h.w.editorText() != corrected || h.w.plan != nil {
		t.Fatal("native valid multiples were not accepted as read-only assistance")
	}
	// All history assertions happen while the source editor is still mounted.
	// Entering the later Diff view is not claimed to preserve native history.
	focusMultipleOfEditorByKeyboard(h)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != quarterCorrected {
		t.Fatal("checking the CRD correction broke native Undo")
	}
	h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if h.w.editorText() != corrected {
		t.Fatal("checking the CRD correction broke native Redo")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != draft {
		t.Fatal("native multipleOf checks lost preceding correction history")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != original.String() || h.w.editor != buffer || snapshot.String() != draft || h.w.plan != nil {
		t.Fatal("native multipleOf checks lost the original live-document history")
	}
	for range 3 {
		h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	}
	if h.w.editorText() != corrected || h.w.editor != buffer || h.w.plan != nil {
		t.Fatal("native multipleOf correction sequence did not survive Redo")
	}
	h.click("Preview edit")
	h.pump(func() bool { return !h.w.preparing })
	if h.w.plan == nil || h.w.plan.Kind() != "edit" || h.w.plan.Target().UID != f.Live.GetUID() || h.w.detailMode != "Diff" || h.w.pendingWrites != 0 || len(h.w.history) != 0 {
		t.Fatal("corrected native multipleOf draft did not produce an explicit read-only server preview")
	}
	_, afterBytes := h.w.plan.Preview()
	after := &unstructured.Unstructured{}
	if err := after.UnmarshalJSON(afterBytes); err != nil {
		t.Fatal(err)
	}
	even, evenFound, evenErr := unstructured.NestedInt64(after.Object, "spec", "even")
	quarter, quarterFound, quarterErr := unstructured.NestedFloat64(after.Object, "spec", "quarter")
	if after.GetUID() != f.Live.GetUID() || after.GroupVersionKind() != f.GVK || evenErr != nil || !evenFound || even != 4 || quarterErr != nil || !quarterFound || quarter != 0.5 || !strings.Contains(h.w.diff, "/spec/even") || !strings.Contains(h.w.diff, "/spec/quarter") {
		t.Fatal("actual native server preview did not contain the corrected resource identity and values")
	}
	assertLiveUnchanged()
	h.click("Disconnect")
	if h.w.backend != nil || h.w.plan != nil || h.w.schemaSource != nil || h.w.schemaJump != nil || len(h.w.schemaLocations) != 0 || h.w.schemaText != "" {
		t.Fatal("disconnect retained multipleOf assistance or its reviewed plan")
	}
}
