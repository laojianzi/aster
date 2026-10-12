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

func TestNativeSchemaConstraintsAgainstRealCluster(t *testing.T) {
	f := testcluster.NewSchemaConstraints(t)
	h := newRelationshipHarness(t, 90*time.Second)
	// The fixture already requires an explicit isolated cluster. The native
	// connection uses that same selected path/context, including real discovery.
	h.w.path = os.Getenv("KUBECONFIG")
	h.w.currentContext = os.Getenv("ASTER_E2E_CONTEXT")
	h.w.contexts = []string{h.w.currentContext}
	h.w.namespace = f.Namespace
	h.tt.Frame()
	h.click("Connect")
	label := ""
	h.pump(func() bool {
		for _, kind := range h.w.kinds {
			if kind.GVR == f.GVR && kind.Kind == f.GVK.Kind && kind.Namespaced {
				label = kind.Label()
				return true
			}
		}
		return false
	})
	h.click("Resource kind")
	if err := h.tt.ChooseMenuItem(label); err != nil {
		t.Fatal(err)
	}
	h.pump(func() bool { return containsRowUID(h.w.rows, string(f.Live.GetUID())) })
	h.click(f.Live.GetName())
	h.pump(func() bool { return h.w.detail != nil })
	if h.w.detail.GroupVersionKind() != f.GVK || h.w.detail.GetUID() != f.Live.GetUID() {
		t.Fatal("native discovery opened a different CRD identity")
	}
	h.click("Edit")
	buffer, original := h.w.editor, h.w.editor.Snapshot()
	obj, err := manifest.Decode([]byte(h.w.editorText()))
	if err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedField(obj.Object, "PRIVATE_NATIVE_DRAFT_CHOICE", "spec", "mode"); err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedField(obj.Object, "🚢", "spec", "title"); err != nil {
		t.Fatal(err)
	}
	draft, err := manifest.Display(obj, false)
	if err != nil {
		t.Fatal(err)
	}
	draft = "# 中文 🚢 é — local constraint correction\n" + draft
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(draft)
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/mode · enum") || !strings.Contains(h.w.schemaText, "/spec/title · minLength") || strings.Contains(h.w.schemaText+h.w.schemaStatus, "PRIVATE_NATIVE_DRAFT_CHOICE") {
		t.Fatal("native CRD constraint hints were absent or echoed a draft value")
	}
	if h.w.editor != buffer || h.w.editorText() != draft || original.String() != h.w.detailText || h.w.plan != nil {
		t.Fatal("native CRD inspection changed its draft, snapshot or plan")
	}
	target := chooseDiagnostic(h, "/spec/title")
	if !target.position.Exact || !strings.HasPrefix(string([]rune(draft)[target.position.RuneOffset:]), "title:") {
		t.Fatal("native CRD constraint did not locate its original Unicode source")
	}
	h.click("Go to location")
	if !h.tt.Focused("Manifest editor") || h.w.editorText() != draft || h.w.plan != nil {
		t.Fatal("native CRD navigation changed the draft or plan")
	}
	saveNativeScreenshot(t, h.tt)
	h.tt.Key(ui.Shift, ui.KeyEnd)
	h.tt.Type("title: 中🚢e\u0301")
	corrected := h.w.editorText()
	obj, err = manifest.Decode([]byte(corrected))
	if err != nil {
		t.Fatal(err)
	}
	title, found, err := unstructured.NestedString(obj.Object, "spec", "title")
	if err != nil || !found || title != "中🚢e\u0301" {
		t.Fatal("source navigation corrected a different field")
	}
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/mode · enum") || strings.Contains(h.w.schemaText, "/spec/title · minLength") || strings.Contains(h.w.schemaText, "/spec/title · maxLength") || h.w.editorText() != corrected || h.w.plan != nil {
		t.Fatal("native Unicode correction did not refresh the actual published constraint")
	}
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != draft {
		t.Fatal("checking a CRD correction broke Undo")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != original.String() || h.w.editor != buffer || h.w.plan != nil {
		t.Fatal("CRD navigation reset the pre-inspection native history")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	after, err := f.Backend.GetObject(ctx, f.GVR, f.Namespace, f.Live.GetName())
	if err != nil || after.GetUID() != f.Live.GetUID() || after.GetResourceVersion() != f.Live.GetResourceVersion() || !reflect.DeepEqual(after.Object["spec"], f.Live.Object["spec"]) {
		t.Fatalf("native constraint inspection mutated its live CRD: %v", err)
	}
	h.click("Disconnect")
	if h.w.backend != nil || h.w.schemaSource != nil || h.w.schemaJump != nil || len(h.w.schemaLocations) != 0 || h.w.schemaText != "" {
		t.Fatal("disconnect retained CRD constraint assistance")
	}
}
