package uiworkbench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/helmrender"
)

const helmDiagnosticPrefix = "Helm capture diagnostic v1: "

func assertNativeHelmDiagnosticFailure(t *testing.T, h *relationshipHarness, f *deliveryUIServer, errorText string, private ...string) {
	t.Helper()
	status := h.w.delivery.status
	if !strings.HasPrefix(status, errorText+" · "+helmDiagnosticPrefix) {
		t.Fatalf("capture failure omitted its original error or bounded diagnostic: %s", status)
	}
	if len(status) > len(errorText)+len(" · ")+4096 {
		t.Fatal("capture diagnostic exceeded its fixed output budget")
	}
	for _, canary := range private {
		if canary != "" && strings.Contains(status, canary) {
			t.Fatal("capture diagnostic disclosed a private input")
		}
	}
	if h.w.delivery.helm.result != nil || h.w.delivery.set != nil || h.w.delivery.plan != nil || h.w.pendingWrites != 0 {
		t.Fatal("capture diagnostic created an accepted result or write authority")
	}
	if a, b, c := f.counts(); a != 0 || b != 0 || c != 0 {
		t.Fatal("capture diagnostic contacted the cluster")
	}
	// This exact UI status is also what the unchanged captureHelmUI helper
	// records on an unexpected failure; it contains no source or renderer text.
	t.Log(status)
}

func TestNativeHelmRenderDiagnosticsFailurePrivacy(t *testing.T) {
	const templateCanary = "PRIVATE_DIAGNOSTIC_TEMPLATE_FAILURE"
	const valuesCanary = "PRIVATE_DIAGNOSTIC_VALUES_CONTENT"
	const pathCanary = "PRIVATE_DIAGNOSTIC_CHART_PATH"
	const envCanary = "PRIVATE_DIAGNOSTIC_ENVIRONMENT_VALUE"
	t.Setenv("ASTER_PRIVATE_DIAGNOSTIC_CANARY", envCanary)
	distribution, chart := helmTestChart(t, `{{ fail "`+templateCanary+`" }}`)
	dir := filepath.Join(t.TempDir(), pathCanary)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	privateChart := filepath.Join(dir, "chart.tgz")
	if err := os.Rename(chart, privateChart); err != nil {
		t.Fatal(err)
	}
	values := filepath.Join(dir, "values.yaml")
	if err := os.WriteFile(values, []byte("message: "+valuesCanary+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f := newDeliveryUIServer(t)
	h := deliveryHarness(t, f, "diagnostic-failure")
	pasteDelivery(h, deliveryYAML("manual", "PRIVATE_DIAGNOSTIC_ORIGINAL_DRAFT"))
	original, before := h.w.delivery.editor, h.w.delivery.editor.Snapshot()
	configureHelmUI(h, distribution, privateChart, "private-diagnostic-release")
	h.click("Helm values file")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(values)
	if h.w.delivery.helm.trusted {
		t.Fatal("changed diagnostic input retained trust")
	}
	h.click("I trust this local chart (not sandboxed)")
	h.click("Render local Helm")
	h.pump(func() bool { return h.w.delivery.busy == "" })
	assertNativeHelmDiagnosticFailure(t, h, f, helmrender.ErrRender.Error(), templateCanary, valuesCanary, pathCanary, envCanary,
		privateChart, values, distribution, "private-diagnostic-release", "PRIVATE_DIAGNOSTIC_ORIGINAL_DRAFT")
	if h.w.delivery.editor != original || original.Snapshot() != before {
		t.Fatal("diagnostic failure changed the mounted draft or history")
	}
	// A later explicit, separately trusted capture succeeds normally and does
	// not retain the prior failure's diagnostic in the current status.
	_, valid := helmTestChart(t, helmConfigTemplate)
	h.click("Helm chart archive")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(valid)
	if h.w.delivery.helm.trusted {
		t.Fatal("replacement chart retained trust")
	}
	h.click("I trust this local chart (not sandboxed)")
	captureHelmUI(h)
	if strings.Contains(h.w.delivery.status, helmDiagnosticPrefix) || h.w.delivery.editor != original || original.Snapshot() != before {
		t.Fatal("a fresh successful capture retained failure state or replaced the draft")
	}
	if a, b, c := f.counts(); a != 0 || b != 0 || c != 0 {
		t.Fatal("diagnostic success bypassed import and per-object consent")
	}
}

func TestNativeHelmRenderDiagnosticsEarlyFailures(t *testing.T) {
	for _, mode := range []string{"invalid-chart", "parent-cancel"} {
		t.Run(mode, func(t *testing.T) {
			distribution, chart := helmTestChart(t, helmConfigTemplate)
			f := newDeliveryUIServer(t)
			h := deliveryHarness(t, f, "diagnostic-early")
			configureHelmUI(h, distribution, chart, "private-early-release")
			want := helmrender.ErrInput.Error()
			if mode == "invalid-chart" {
				if err := os.WriteFile(chart, []byte("PRIVATE_DIAGNOSTIC_INVALID_ARCHIVE"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				ctx, cancel := context.WithCancel(h.ctx)
				cancel()
				h.w.connectionCtx = ctx
				want = context.Canceled.Error()
			}
			h.click("Render local Helm")
			h.pump(func() bool { return h.w.delivery.busy == "" })
			assertNativeHelmDiagnosticFailure(t, h, f, want, "PRIVATE_DIAGNOSTIC_INVALID_ARCHIVE", chart, distribution, "private-early-release")
		})
	}
}

func TestNativeHelmRenderDiagnosticsStaleCallback(t *testing.T) {
	distribution, chart := helmTestChart(t, helmConfigTemplate)
	if err := os.WriteFile(chart, []byte("PRIVATE_DIAGNOSTIC_STALE_INPUT"), 0600); err != nil {
		t.Fatal(err)
	}
	f := newDeliveryUIServer(t)
	h := deliveryHarness(t, f, "diagnostic-stale")
	configureHelmUI(h, distribution, chart, "private-stale-release")
	h.click("Render local Helm")
	var late func()
	select {
	case late = <-h.updates:
	case <-h.ctx.Done():
		t.Fatal("diagnostic callback was not delivered within the original harness deadline")
	}
	h.click("Close Helm rendering")
	h.click("Render Helm chart")
	before, epoch := h.w.delivery.status, h.w.delivery.epoch
	late()
	h.tt.Frame()
	if h.w.delivery.status != before || h.w.delivery.epoch != epoch || !h.w.delivery.helm.open || h.w.delivery.helm.result != nil || strings.Contains(h.w.delivery.status, helmDiagnosticPrefix) {
		t.Fatal("late diagnostic failure crossed into a fresh Helm review")
	}
	if a, b, c := f.counts(); a != 0 || b != 0 || c != 0 {
		t.Fatal("late diagnostic callback contacted the cluster")
	}
}
