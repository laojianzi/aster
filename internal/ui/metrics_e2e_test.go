//go:build e2e

package uiworkbench

import (
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/resourcemetrics"
	"github.com/laojianzi/aster/internal/testcluster"
)

func TestNativeMetricsAgainstRealServerAndTeardown(t *testing.T) {
	f := testcluster.NewHTTPPod(t)
	b, err := kube.New(f.Config)
	if err != nil {
		t.Fatal(err)
	}
	testcluster.AwaitMetrics(t, b, f.Target("prerequisite"))
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
	h.click("Metrics")
	h.pump(func() bool { return !h.w.metricsLoading })
	if h.w.metricsResult.State != resourcemetrics.Ready || h.w.metricsResult.Total.MemoryBytes <= 0 {
		t.Fatalf("%+v", h.w.metricsResult)
	}
	h.click("Start sampling")
	h.pump(func() bool { return len(h.w.metricsHistory.Points) >= 2 })
	saveNativeScreenshot(t, h.tt)
	h.click("Pause metrics")
	if h.w.metricsActive || h.w.metricsCancel != nil {
		t.Fatal("sampler not paused")
	}
	h.click("Close detail")
	if h.w.metricsResult.State != "" || len(h.w.metricsHistory.Points) != 0 || h.w.metricsLoading {
		t.Fatal("metrics survived detail teardown")
	}
	h.click("Disconnect")
	if h.w.backend != nil {
		t.Fatal("cluster still connected")
	}
}
