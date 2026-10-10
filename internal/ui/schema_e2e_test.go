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

func TestNativeSchemaAssistanceAgainstRealCluster(t *testing.T) {
	f := testcluster.NewHTTPPod(t)
	_, current, err := kubeconfig.Contexts("")
	if err != nil {
		t.Fatal(err)
	}
	h := newRelationshipHarness(t, 90*time.Second)
	h.w.currentContext = current
	h.w.contexts = []string{current}
	h.w.namespace = f.Pod.Namespace
	h.tt.Frame()
	h.click("Connect")
	h.pump(func() bool { return containsRowUID(h.w.rows, string(f.Pod.UID)) })
	h.click(f.Pod.Name)
	h.pump(func() bool { return h.w.detail != nil })
	h.click("Edit")
	h.click("Schema field pointer")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("/spec/containers/0/image")
	h.click("Field help")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "type string") {
		t.Fatalf("%s", h.w.schemaStatus)
	}
	obj, err := manifest.Decode([]byte(h.w.editor))
	if err != nil {
		t.Fatal(err)
	}
	containers, _, _ := unstructured.NestedSlice(obj.Object, "spec", "containers")
	containers[0].(map[string]any)["image"] = int64(42)
	_ = unstructured.SetNestedSlice(obj.Object, containers, "spec", "containers")
	text, err := manifest.Display(obj, false)
	if err != nil {
		t.Fatal(err)
	}
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(text)
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/containers/0/image · type") || h.w.editor != text || h.w.plan != nil {
		t.Fatalf("%s %s", h.w.schemaStatus, h.w.schemaText)
	}
	saveNativeScreenshot(t, h.tt)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pod, err := f.Client.CoreV1().Pods(f.Pod.Namespace).Get(ctx, f.Pod.Name, metav1.GetOptions{})
	if err != nil || pod.UID != f.Pod.UID || pod.Spec.Containers[0].Image != testcluster.Image {
		t.Fatalf("schema check mutated live Pod: %v", err)
	}
	h.click("Disconnect")
	if h.w.schemaText != "" || h.w.schemaActive || h.w.backend != nil {
		t.Fatal("schema retained on disconnect")
	}
}

// This exercises native input/navigation against a real API server. It never
// invokes Preview/Execute; changing the local draft must not mutate the Pod.
func TestNativeSchemaDiagnosticNavigationAgainstRealCluster(t *testing.T) {
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
	obj, err := manifest.Decode([]byte(h.w.editor))
	if err != nil {
		t.Fatal(err)
	}
	containers, _, _ := unstructured.NestedSlice(obj.Object, "spec", "containers")
	containers[0].(map[string]any)["image"] = int64(42)
	if err := unstructured.SetNestedSlice(obj.Object, containers, "spec", "containers"); err != nil {
		t.Fatal(err)
	}
	text, err := manifest.Display(obj, false)
	if err != nil {
		t.Fatal(err)
	}
	text = "# 中文 🚢 é — local draft only\n" + text
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(text)
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/containers/0/image · type") {
		t.Fatal(h.w.schemaText)
	}
	target := chooseDiagnostic(h, "/spec/containers/0/image")
	if !target.position.Exact {
		t.Fatal("explicit image key has no exact source location")
	}
	h.click("Go to location")
	if h.w.editor != text || h.w.plan != nil || !h.tt.Focused("Manifest editor") {
		t.Fatal("navigation modified the draft or plan")
	}
	saveNativeScreenshot(t, h.tt)
	h.tt.Key(ui.Shift, ui.KeyEnd)
	h.tt.Type("image: " + testcluster.Image)
	corrected, err := manifest.Decode([]byte(h.w.editor))
	if err != nil {
		t.Fatal(err)
	}
	cs, _, _ := unstructured.NestedSlice(corrected.Object, "spec", "containers")
	if cs[0].(map[string]any)["image"] != testcluster.Image {
		t.Fatal("navigation did not select the actual source key")
	}
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if strings.Contains(h.w.schemaText, "/spec/containers/0/image · type") || h.w.schemaText == "" || h.w.plan != nil {
		t.Fatal("correction did not refresh advisory diagnostics")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pod, err := f.Client.CoreV1().Pods(f.Pod.Namespace).Get(ctx, f.Pod.Name, metav1.GetOptions{})
	if err != nil || pod.UID != f.Pod.UID || pod.Spec.Containers[0].Image != testcluster.Image {
		t.Fatalf("source inspection mutated live Pod: %v", err)
	}
	h.click("Disconnect")
	if h.w.schemaSource != nil || h.w.schemaJump != nil || len(h.w.schemaLocations) != 0 {
		t.Fatal("disconnect kept source navigation")
	}
}
