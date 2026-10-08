package uiworkbench

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/resourcemetrics"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func metricsPod() *unstructured.Unstructured {
	pod := relatedPod()
	_ = unstructured.SetNestedSlice(pod.Object, []interface{}{map[string]interface{}{"name": "app"}}, "spec", "containers")
	return pod
}
func serveMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/v1/namespaces/team/pods/related-pod":
		_ = json.NewEncoder(w).Encode(metricsPod())
	case "/apis/metrics.k8s.io":
		fmt.Fprint(w, `{"name":"metrics.k8s.io","versions":[{"version":"v1beta1","groupVersion":"metrics.k8s.io/v1beta1"}]}`)
	default:
		fmt.Fprintf(w, `{"apiVersion":"metrics.k8s.io/v1beta1","kind":"PodMetrics","metadata":{"name":"related-pod","namespace":"team"},"timestamp":%q,"window":"15s","containers":[{"name":"app","usage":{"cpu":"125m","memory":"64Mi"}}]}`, time.Now().UTC().Format(time.RFC3339Nano))
	}
}
func TestNativeMetricsReadOnlyAndTabLifecycle(t *testing.T) {
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes.Add(1)
		}
		serveMetrics(w, r)
	}))
	defer server.Close()
	h := newRelationshipHarness(t, 10*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.click("Metrics")
	h.pump(func() bool { return !h.w.metricsLoading })
	if h.w.metricsResult.State != resourcemetrics.Ready || len(h.w.metricsResult.Entries) != 1 || h.w.metricsResult.Total.MemoryBytes != 64<<20 {
		t.Fatalf("%+v", h.w.metricsResult)
	}
	h.click("Start sampling")
	h.pump(func() bool { return !h.w.metricsLoading })
	if !h.w.metricsActive {
		t.Fatal("sampler not running")
	}
	h.click("YAML")
	if h.w.metricsActive || h.w.metricsCancel != nil {
		t.Fatal("invisible metrics panel kept polling")
	}
	if writes.Load() != 0 {
		t.Fatal("metrics performed mutation")
	}
	h.click("Metrics")
	h.pump(func() bool { return !h.w.metricsLoading })
	saveNativeScreenshot(t, h.tt)
	h.click("Close detail")
	if h.w.metricsResult.State != "" || len(h.w.metricsHistory.Points) != 0 {
		t.Fatal("metrics retained across resource change")
	}
}
func TestNativeMetricsQueuedResultsCannotCrossDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(serveMetrics))
	defer server.Close()
	h := newRelationshipHarness(t, 10*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.click("Metrics")
	var completion func()
	select {
	case completion = <-h.updates:
	case <-h.ctx.Done():
		t.Fatal("metrics read did not finish")
	}
	h.click("Close detail")
	h.w.detail = metricsPod()
	h.w.detail.SetUID("new")
	h.w.detailKind = catalog()[0]
	completion()
	h.tt.Frame()
	if h.w.metricsResult.State != "" || len(h.w.metricsHistory.Points) != 0 || h.w.metricsLoading {
		t.Fatal("queued metrics crossed identity")
	}
}
func TestNativeMetricsCloseCancelsRequest(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(canceled) }))
	defer server.Close()
	h := newRelationshipHarness(t, 10*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.click("Metrics")
	select {
	case <-started:
	case <-h.ctx.Done():
		t.Fatal("read not started")
	}
	h.click("Close detail")
	select {
	case <-canceled:
	case <-h.ctx.Done():
		t.Fatal("read did not cancel")
	}
}
func TestNativeMetricsControlsStayInsideMinimumWindow(t *testing.T) {
	w := New()
	w.detail = metricsPod()
	w.detailKind = catalog()[0]
	w.detailMode = "Metrics"
	now := time.Now().UTC()
	w.metricsResult = resourcemetrics.Snapshot{State: resourcemetrics.Ready, Timestamp: now, ReceivedAt: now, Window: 15 * time.Second, Entries: []resourcemetrics.Usage{{Name: "app", CPUCores: 0.125, MemoryBytes: 64 << 20}}, Total: resourcemetrics.Usage{CPUCores: 0.125, MemoryBytes: 64 << 20}}
	for i := 0; i < 5; i++ {
		s := w.metricsResult
		s.Timestamp = now.Add(time.Duration(i) * time.Second)
		s.ReceivedAt = s.Timestamp
		w.metricsHistory.Add(s)
	}
	tt := ui.NewTester(w.View, 1100, 700)
	for _, label := range []string{"Metrics", "Refresh metrics", "Start sampling", "Pause metrics", "CPU cores trend", "Memory MiB trend", "Usage app", "Close detail", "Preview delete"} {
		r, ok := tt.Find(label)
		if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > 1088 || r.Y+r.H > 700 || r.H <= 0 || r.W <= 0 {
			t.Errorf("%s out of bounds: %+v found=%v", label, r, ok)
		}
	}
	saveNativeScreenshot(t, tt)
}
