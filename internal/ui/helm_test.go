package uiworkbench

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func helmTestChart(t *testing.T, template string) (string, string) {
	t.Helper()
	distribution := os.Getenv("ASTER_HELM_ARCHIVE")
	if distribution == "" {
		t.Skip("requires approved Helm archive; mandatory in CI")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "local.tgz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, item := range []struct{ name, body string }{
		{"sample/Chart.yaml", "apiVersion: v2\nname: sample\nversion: 0.1.0\ntype: application\n"},
		{"sample/values.yaml", "message: '中文 🚢 é'\n"},
		{"sample/templates/config.yaml", template},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: item.name, Mode: 0600, Size: int64(len(item.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(item.body)); err != nil {
			t.Fatal(err)
		}
	}
	for _, err := range []error{tw.Close(), gz.Close(), f.Close()} {
		if err != nil {
			t.Fatal(err)
		}
	}
	return distribution, path
}

const helmConfigTemplate = `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}-one
  namespace: {{ .Release.Namespace }}
data:
  message: {{ .Values.message | quote }}
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}-two
  namespace: {{ .Release.Namespace }}
data:
  message: second
`

func configureHelmUI(h *relationshipHarness, distribution, chart, release string) {
	h.t.Helper()
	h.click("Render Helm chart")
	for _, field := range []struct{ label, value string }{
		{"Helm distribution archive", distribution}, {"Helm chart archive", chart},
		{"Helm release name", release}, {"Helm Kubernetes version", "1.37.0"},
	} {
		h.click(field.label)
		h.tt.Key(ui.Cmd, ui.KeyA)
		h.tt.Type(field.value)
	}
	h.click("I trust this local chart (not sandboxed)")
}
func captureHelmUI(h *relationshipHarness) {
	h.t.Helper()
	h.click("Render local Helm")
	h.pump(func() bool { return h.w.delivery.busy == "" })
	if h.w.delivery.helm.result == nil {
		h.t.Fatalf("Helm capture failed: %s", h.w.delivery.status)
	}
}
func acceptHelmUI(h *relationshipHarness) {
	h.t.Helper()
	h.click("Replace current input; start a new Undo/review history")
	h.click("Use Helm result")
	if h.w.delivery.helm.open || h.w.delivery.origin == "" {
		h.t.Fatal("explicit Helm result acceptance failed")
	}
}

func TestNativeHelmRenderRequiresImportAndPerObjectReview(t *testing.T) {
	distribution, chart := helmTestChart(t, helmConfigTemplate)
	f := newDeliveryUIServer(t)
	h := deliveryHarness(t, f, "helm-review")
	pasteDelivery(h, deliveryYAML("manual", "original"))
	original, snapshot := h.w.delivery.editor, h.w.delivery.editor.Snapshot()
	configureHelmUI(h, distribution, chart, "rendered")
	captureHelmUI(h)
	if h.w.delivery.editor != original || original.Snapshot() != snapshot {
		t.Fatal("render overwrote the original input")
	}
	result := h.w.delivery.helm.result.Manifest()
	if !strings.Contains(result, "中文 🚢 é") || !strings.Contains(result, "rendered-two") {
		t.Fatal("real Helm did not render values and release")
	}
	h.w.useHelmResult() // The handler, not only its disabled control, must reject missing consent.
	if h.w.delivery.editor != original {
		t.Fatal("result acceptance bypassed replacement consent")
	}
	if a, b, c := f.counts(); a != 0 || b != 0 || c != 0 || h.w.delivery.set != nil || h.w.delivery.plan != nil {
		t.Fatal("render created discovery, a plan, or a write")
	}
	saveNativeScreenshot(t, h.tt)
	// Source files changing after capture cannot change the accepted immutable output.
	if err := os.WriteFile(chart, []byte("changed after capture"), 0600); err != nil {
		t.Fatal(err)
	}
	acceptHelmUI(h)
	if h.w.delivery.editor.Snapshot().String() != result || h.w.delivery.editor == original {
		t.Fatal("acceptance rerendered or reused old history")
	}
	if a, b, c := f.counts(); a != 0 || b != 0 || c != 0 {
		t.Fatal("acceptance automatically imported or wrote")
	}
	importDeliveryUI(h)
	if h.w.delivery.set.Len() != 2 || h.w.delivery.plan != nil {
		t.Fatal("import did not preserve separate review")
	}
	previewDeliveryUI(h, "Preview next object")
	submitDeliveryUI(h, "incorrect")
	if _, _, writes := f.counts(); writes != 0 {
		t.Fatal("wrong confirmation wrote Helm output")
	}
	submitDeliveryUI(h, "rendered-one")
	if _, _, writes := f.counts(); writes != 1 || h.w.delivery.plan != nil || h.w.delivery.outcomes[1].State == "Succeeded" {
		t.Fatal("first confirmation submitted later objects")
	}
	previewDeliveryUI(h, "Preview next object")
	submitDeliveryUI(h, "rendered-two")
	if _, _, writes := f.counts(); writes != 2 {
		t.Fatal("separate confirmation did not submit second object")
	}
	saveNativeScreenshot(t, h.tt)
}

func TestNativeHelmDiscardFailureAndMountedHistory(t *testing.T) {
	distribution, chart := helmTestChart(t, helmConfigTemplate)
	f := newDeliveryUIServer(t)
	h := deliveryHarness(t, f, "helm-history")
	base := deliveryYAML("manual", "original")
	pasteDelivery(h, base)
	h.tt.SetClipboard("# prior edit 中文 🚢\n")
	h.tt.Key(ui.Cmd, ui.KeyV)
	original, snapshot := h.w.delivery.editor, h.w.delivery.editor.Snapshot()
	configureHelmUI(h, distribution, chart, "history")
	captureHelmUI(h)
	h.click("Discard Helm result")
	if h.w.delivery.helm.result != nil || h.w.delivery.editor != original || original.Snapshot() != snapshot {
		t.Fatal("discard lost original bytes/buffer")
	}
	// A renderer error must not replace the source or expose arbitrary template text.
	_, invalid := helmTestChart(t, `{{ fail "PRIVATE_HELM_FAILURE" }}`)
	h.click("Helm chart archive")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(invalid)
	if h.w.delivery.helm.trusted {
		t.Fatal("changed chart kept prior trust")
	}
	h.click("I trust this local chart (not sandboxed)")
	h.click("Render local Helm")
	h.pump(func() bool { return h.w.delivery.busy == "" })
	if h.w.delivery.helm.result != nil || strings.Contains(h.w.delivery.status, "PRIVATE_") || h.w.delivery.editor != original || original.Snapshot() != snapshot {
		t.Fatal("failed render changed original input or disclosed details")
	}
	h.click("Close Helm rendering")
	h.click("Delivery manifests")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Key(ui.Cmd, ui.KeyC)
	if h.tt.Clipboard() != snapshot.String() {
		t.Fatal("original source is no longer selectable")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if original.Snapshot().String() != base {
		t.Fatal("render/discard/failure reset mounted Undo")
	}
	h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if original.Snapshot().String() != snapshot.String() {
		t.Fatal("render/discard/failure reset mounted Redo")
	}
	if a, b, c := f.counts(); a != 0 || b != 0 || c != 0 {
		t.Fatal("discard/failure reached cluster")
	}
}

func TestNativeHelmMinimumWindowStaleResultsAndTeardown(t *testing.T) {
	distribution, chart := helmTestChart(t, helmConfigTemplate)
	f := newDeliveryUIServer(t)
	h := deliveryHarness(t, f, "helm-stale")
	h.tt = ui.NewTester(h.w.View, 1100, 700)
	pasteDelivery(h, deliveryYAML("manual", "original"))
	configureHelmUI(h, distribution, chart, "stale")
	captureHelmUI(h)
	for _, label := range []string{"Helm chart archive", "Render local Helm", "Use Helm result", "Discard Helm result", "Close Helm rendering", "Delivery manifests", "Import manifests"} {
		r, ok := h.tt.Find(label)
		if !ok || r.Y < 0 || r.Y+r.H > 700 || r.X < 0 || r.X+r.W > 1100 {
			t.Fatalf("minimum-window control outside viewport: %s %+v", label, r)
		}
	}
	saveNativeScreenshot(t, h.tt)
	oldResult, oldBinding := h.w.delivery.helm.result, h.w.delivery.helm.binding
	original := h.w.delivery.editor
	// A source edit invalidates even a captured result with its old binding.
	h.click("Delivery manifests")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(deliveryYAML("changed", "new"))
	if h.w.delivery.helm.result != nil {
		t.Fatal("source edit retained a captured render")
	}
	h.w.delivery.helm.result, h.w.delivery.helm.binding, h.w.delivery.helm.replace = oldResult, oldBinding, true
	h.w.useHelmResult()
	if h.w.delivery.editor != original || !strings.Contains(original.Snapshot().String(), "changed") {
		t.Fatal("stale result changed a newer source")
	}
	h.w.stopDeliveryReview()
	h.w.delivery.helm.trusted = true
	h.w.renderHelm()
	if h.w.delivery.cancel == nil {
		t.Fatal("render worker was not cancellable")
	}
	done := h.w.delivery.cancel
	h.click("Close Helm rendering")
	done() // Cancellation is idempotent; subsequent callbacks must remain stale.
	h.click("Back to resources")
	h.click("Disconnect")
	h.pump(func() bool { return h.w.backend == nil })
	h.tt.Frame()
	if h.w.delivery.helm.result != nil || h.w.delivery.open || h.w.delivery.origin != "" {
		t.Fatal("teardown retained private Helm state")
	}
	if a, b, c := f.counts(); a != 0 || b != 0 || c != 0 {
		t.Fatal("stale result or cancellation contacted cluster")
	}
}

func TestNativeHelmLifecycleOutputRemainsRejected(t *testing.T) {
	for _, scenario := range []struct{ name, source string }{
		{"hook", strings.Replace(helmConfigTemplate, "  name: {{ .Release.Name }}-one", "  annotations:\n    helm.sh/hook: pre-install\n  name: {{ .Release.Name }}-one", 1)},
		{"secret", `apiVersion: v1
kind: Secret
metadata:
  name: {{ .Release.Name }}-secret
  namespace: {{ .Release.Namespace }}
stringData:
  token: PRIVATE_SECRET_VALUE
`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			distribution, chart := helmTestChart(t, scenario.source)
			f := newDeliveryUIServer(t)
			h := deliveryHarness(t, f, "helm-policy")
			configureHelmUI(h, distribution, chart, "restricted")
			captureHelmUI(h)
			acceptHelmUI(h)
			h.click("Import manifests")
			h.pump(func() bool { return h.w.delivery.busy == "" })
			if h.w.delivery.set != nil || h.w.delivery.plan != nil || strings.Contains(h.w.delivery.status, "PRIVATE_SECRET_VALUE") {
				t.Fatal("Helm output bypassed import policy or disclosed error data")
			}
			if _, previews, writes := f.counts(); previews != 0 || writes != 0 {
				t.Fatal("unsupported Helm output was previewed or written")
			}
		})
	}
}
