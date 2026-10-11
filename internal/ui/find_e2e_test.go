//go:build e2e

package uiworkbench

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/testcluster"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// This is native selection/input against a disposable real Kubernetes Pod.
// It deliberately never invokes Preview or Execute: changing a local match
// and undoing it must leave the live resource's image and identity untouched.
func TestNativeFindCorrectionUndoAgainstRealCluster(t *testing.T) {
	f := testcluster.NewHTTPPod(t)
	_, current, err := kubeconfig.Contexts("")
	if err != nil {
		t.Fatal(err)
	}
	h := newRelationshipHarness(t, 90*time.Second)
	h.w.currentContext, h.w.contexts, h.w.namespace = current, []string{current}, f.Pod.Namespace
	h.tt.Frame()
	h.click("Connect")
	h.pump(func() bool { return containsRowUID(h.w.rows, string(f.Pod.UID)) })
	h.click(f.Pod.Name)
	h.pump(func() bool { return h.w.detail != nil })
	h.click("Edit")
	text := "# 中文 🚢 é — local search only\r\n" + strings.ReplaceAll(h.w.editor, "\n", "\r\n")
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(text)
	h.click("Find")
	h.click("Find text")
	h.tt.Type(testcluster.Image)
	findSearch(h)
	if h.w.find.result.Len() < 1 || !h.tt.Focused("Manifest editor") || h.w.editor != text || h.w.plan != nil {
		t.Fatal("read-only Find did not select a live-image occurrence in the local draft")
	}
	positions, err := manifest.IndexSource(h.ctx, []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	field, ok := positions.Locate("/spec/containers/0/image")
	if !ok || !field.Exact {
		t.Fatal("fixture image field has no explicit location")
	}
	for attempts := 0; attempts < h.w.find.result.Len(); attempts++ {
		match, _ := h.w.find.result.At(h.w.find.selected)
		if match.Line == field.Line {
			break
		}
		h.click("Next match")
	}
	selected, _ := h.w.find.result.At(h.w.find.selected)
	if selected.Line != field.Line {
		t.Fatal("Find could not reach the exact spec image")
	}
	saveNativeScreenshot(t, h.tt)
	const replacement = "example.invalid/aster-local-draft:never-executed"
	h.tt.Type(replacement)
	obj, err := manifest.Decode([]byte(h.w.editor))
	if err != nil {
		t.Fatal(err)
	}
	containers, found, err := unstructured.NestedSlice(obj.Object, "spec", "containers")
	if err != nil || !found || len(containers) != 1 || containers[0].(map[string]any)["image"] != replacement || h.w.find.result != nil || h.w.plan != nil {
		t.Fatal("native selection did not modify only the intended local match")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editor != text {
		t.Fatal("Find lost same-document Undo")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pod, err := f.Client.CoreV1().Pods(f.Pod.Namespace).Get(ctx, f.Pod.Name, metav1.GetOptions{})
	if err != nil || pod.UID != f.Pod.UID || pod.Spec.Containers[0].Image != testcluster.Image {
		t.Fatalf("Find or local typing modified the live resource: %v", err)
	}
	findSearch(h)
	h.click("Disconnect")
	if h.w.find.open || h.w.find.query != "" || h.w.find.result != nil || h.w.find.jump != nil {
		t.Fatal("disconnect retained Find state")
	}
}
