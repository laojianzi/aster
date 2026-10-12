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

func TestNativeSchemaPatternAgainstRealCluster(t *testing.T) {
	f := testcluster.NewSchemaPatterns(t)
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
	// The actual discovered index can be outside the Select popup viewport.
	h.tt.Key(0, ui.KeyHome)
	for range choiceIndex {
		h.tt.Key(0, ui.KeyDown)
	}
	h.tt.Key(0, ui.KeyEnter)
	if h.w.kindChoice != label || h.w.currentKind.GVR != f.GVR || h.w.currentKind.Kind != f.GVK.Kind || !h.w.currentKind.Namespaced {
		t.Fatal("native select did not choose the actually discovered pattern CRD")
	}
	h.pump(func() bool { return containsRowUID(h.w.rows, string(f.Live.GetUID())) })
	h.click(f.Live.GetName())
	h.pump(func() bool { return h.w.detail != nil })
	if h.w.detail.GroupVersionKind() != f.GVK || h.w.detail.GetUID() != f.Live.GetUID() || h.w.detail.GetResourceVersion() != f.Live.GetResourceVersion() {
		t.Fatal("native discovery opened a different live pattern resource identity")
	}
	h.click("Edit")
	buffer, original := h.w.editor, h.w.editor.Snapshot()
	obj, err := manifest.Decode([]byte(h.w.editorText()))
	if err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedField(obj.Object, "PRIVATE_INVALID_SLUG", "spec", "slug"); err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedField(obj.Object, "PRIVATE_MISSING_TOKEN", "spec", "contains"); err != nil {
		t.Fatal(err)
	}
	draft, err := manifest.Display(obj, false)
	if err != nil {
		t.Fatal(err)
	}
	draft = "# 中文 🚢 é — PRIVATE_NATIVE_PATTERN_SOURCE\n" + draft
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(draft)
	snapshot, identity := h.w.editor.Snapshot(), h.w.draftIdentity()
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/slug · pattern") || !strings.Contains(h.w.schemaText, "/spec/contains · pattern") || len(h.w.schemaLocations) != 2 {
		t.Fatal("native inspection did not diagnose the actual published pattern fields")
	}
	for _, private := range []string{"PRIVATE_NATIVE_PATTERN_SOURCE", "PRIVATE_INVALID_SLUG", "PRIVATE_MISSING_TOKEN", "^[a-z][a-z0-9-]*$", "中文船🚢"} {
		if strings.Contains(h.w.schemaText+h.w.schemaStatus, private) {
			t.Fatal("native pattern assistance echoed a source value or advertised expression")
		}
		for _, location := range h.w.schemaLocations {
			if strings.Contains(location.label, private) {
				t.Fatal("native pattern location echoed a source value or expression")
			}
		}
	}
	if h.w.editor != buffer || h.w.editorText() != draft || h.w.draftIdentity() != identity || snapshot.String() != draft || original.String() != h.w.detailText || h.w.plan != nil || h.w.pendingWrites != 0 || len(h.w.history) != 0 {
		t.Fatal("advisory pattern inspection changed input, identity, history or operation state")
	}
	saveNativeScreenshot(t, h.tt)
	assertLiveUnchanged := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		after, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, f.Live.GetName())
		if err != nil || after == nil || after.GetUID() != f.Live.GetUID() || after.GetResourceVersion() != f.Live.GetResourceVersion() || !reflect.DeepEqual(after.Object["spec"], f.Live.Object["spec"]) {
			t.Fatalf("native pattern inspection or dry-run changed the live CRD: %v", err)
		}
	}
	assertLiveUnchanged()
	// Require the authoritative server's specific field validation response.
	// An arbitrary transport or authorization failure cannot satisfy this case.
	h.click("Preview edit")
	h.pump(func() bool { return !h.w.preparing })
	if h.w.plan != nil || h.w.detailMode != "Edit" || !strings.Contains(h.w.detailMessage, "is invalid") || !strings.Contains(h.w.detailMessage, "spec.slug") || !strings.Contains(h.w.detailMessage, "spec.contains") || h.w.editorText() != draft || h.w.pendingWrites != 0 || len(h.w.history) != 0 {
		t.Fatal("native invalid pattern preview did not report server field validation without a plan/write")
	}
	assertLiveUnchanged()
	target := chooseDiagnostic(h, "/spec/contains")
	if !target.position.Exact || !strings.HasPrefix(string([]rune(draft)[target.position.RuneOffset:]), "contains:") {
		t.Fatal("published pattern did not retain the original Unicode source coordinate")
	}
	h.click("Go to location")
	if !h.tt.Focused("Manifest editor") || h.w.editorText() != draft || h.w.plan != nil {
		t.Fatal("native pattern navigation changed the draft or plan")
	}
	h.tt.Key(ui.Shift, ui.KeyEnd)
	// YAML escapes keep this edit on one native line; the decoded value really
	// contains two newlines and an unanchored match between them.
	h.tt.Type(`contains: "before\nok\nafter"`)
	containsCorrected := h.w.editorText()
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/slug · pattern") || strings.Contains(h.w.schemaText, "/spec/contains · pattern") || len(h.w.schemaLocations) != 1 || h.w.editorText() != containsCorrected || h.w.plan != nil {
		t.Fatal("native correction did not refresh the actual unanchored pattern constraint")
	}
	chooseDiagnostic(h, "/spec/slug")
	h.click("Go to location")
	h.tt.Key(ui.Shift, ui.KeyEnd)
	h.tt.Type("slug: native-fixed")
	corrected := h.w.editorText()
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if len(h.w.schemaLocations) != 0 || !strings.Contains(h.w.schemaText, "No supported structural or constraint hints found.") || h.w.editorText() != corrected || h.w.plan != nil {
		t.Fatal("native valid patterns were not accepted as read-only assistance")
	}
	// All history assertions happen while the source editor is still mounted.
	// Entering the later Diff view is not claimed to preserve native history.
	focusMultipleOfEditorByKeyboard(h)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != containsCorrected {
		t.Fatal("checking the CRD pattern correction broke native Undo")
	}
	h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if h.w.editorText() != corrected {
		t.Fatal("checking the CRD pattern correction broke native Redo")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != draft {
		t.Fatal("native pattern checks lost preceding correction history")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != original.String() || h.w.editor != buffer || snapshot.String() != draft || h.w.plan != nil {
		t.Fatal("native pattern checks lost the original live-document history")
	}
	for range 3 {
		h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	}
	if h.w.editorText() != corrected || h.w.editor != buffer || h.w.plan != nil {
		t.Fatal("native pattern correction sequence did not survive Redo")
	}
	h.click("Preview edit")
	h.pump(func() bool { return !h.w.preparing })
	if h.w.plan == nil || h.w.plan.Kind() != "edit" || h.w.plan.Target().UID != f.Live.GetUID() || h.w.detailMode != "Diff" || h.w.pendingWrites != 0 || len(h.w.history) != 0 {
		t.Fatal("corrected native pattern draft did not produce an explicit read-only server preview")
	}
	_, afterBytes := h.w.plan.Preview()
	after := &unstructured.Unstructured{}
	if err := after.UnmarshalJSON(afterBytes); err != nil {
		t.Fatal(err)
	}
	slug, slugFound, slugErr := unstructured.NestedString(after.Object, "spec", "slug")
	contains, containsFound, containsErr := unstructured.NestedString(after.Object, "spec", "contains")
	unicodeValue, unicodeFound, unicodeErr := unstructured.NestedString(after.Object, "spec", "unicode")
	empty, emptyFound, emptyErr := unstructured.NestedString(after.Object, "spec", "empty")
	nullable, nullableFound, nullableErr := unstructured.NestedFieldNoCopy(after.Object, "spec", "nullable")
	if after.GetUID() != f.Live.GetUID() || after.GroupVersionKind() != f.GVK || slugErr != nil || !slugFound || slug != "native-fixed" || containsErr != nil || !containsFound || contains != "before\nok\nafter" || unicodeErr != nil || !unicodeFound || unicodeValue != "中文船🚢" || emptyErr != nil || !emptyFound || empty != "" || nullableErr != nil || !nullableFound || nullable != nil || !strings.Contains(h.w.diff, "/spec/slug") || !strings.Contains(h.w.diff, "/spec/contains") {
		t.Fatal("actual native server preview lost corrected identity/values or unchanged Unicode/empty/nullable fields")
	}
	assertLiveUnchanged()
	h.click("Disconnect")
	if h.w.backend != nil || h.w.plan != nil || h.w.schemaSource != nil || h.w.schemaJump != nil || len(h.w.schemaLocations) != 0 || h.w.schemaText != "" {
		t.Fatal("disconnect retained pattern assistance or its reviewed plan")
	}
}
