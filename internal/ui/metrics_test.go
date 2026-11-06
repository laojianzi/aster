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
	h.click("Overview")
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
	h.click("Metrics")
	if !h.w.metricsActive {
		t.Fatal("reselecting Metrics stopped sampling")
	}
	h.click("YAML")
	h.click("Read-only")
	if h.w.metricsActive || h.w.metricsCancel != nil {
		t.Fatal("invisible metrics panel kept polling")
	}
	if writes.Load() != 0 {
		t.Fatal("metrics performed mutation")
	}
	h.click("Overview")
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
	h.click("Overview")
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
	h.click("Overview")
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
	for _, label := range []string{"Metrics", "Refresh metrics", "Start sampling", "Pause metrics", "CPU cores trend", "Memory MiB trend", "Usage app", "Close detail", "Actions"} {
		r, ok := tt.Find(label)
		if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > 1088 || r.Y+r.H > 700 || r.H <= 0 || r.W <= 0 {
			t.Errorf("%s out of bounds: %+v found=%v", label, r, ok)
		}
	}
	saveNativeScreenshot(t, tt)
}

func TestNativeMetricsReadableValuesAndUnavailableSamples(t *testing.T) {
	for _, scenario := range []string{"available", "zero", "forbidden"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("metrics wrote %s", r.Method)
					http.Error(w, "read only", 405)
					return
				}
				if r.URL.Path == "/api/v1/namespaces/team/pods/related-pod" || r.URL.Path == "/apis/metrics.k8s.io" {
					serveMetrics(w, r)
					return
				}
				if scenario == "forbidden" {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
				if scenario == "zero" {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"apiVersion":"metrics.k8s.io/v1beta1","kind":"PodMetrics","metadata":{"name":"related-pod","namespace":"team"},"timestamp":%q,"window":"15s","containers":[{"name":"app","usage":{"cpu":"0","memory":"0"}}]}`, time.Now().UTC().Format(time.RFC3339Nano))
					return
				}
				serveMetrics(w, r)
			}))
			t.Cleanup(server.Close)
			h := newRelationshipHarness(t, 20*time.Second)
			attachRelationshipBackend(t, h, server.URL)
			h.click("Overview")
			h.click("Metrics")
			h.pump(func() bool { return !h.w.metricsLoading })
			cpu, memory := "0.125", "64"
			if scenario == "zero" {
				cpu, memory = "0", "0"
			}
			if scenario == "forbidden" {
				cpu, memory = "Unavailable", "Unavailable"
			}
			for _, label := range []string{"CPU cores latest: " + cpu, "Memory MiB latest: " + memory, "Sample details"} {
				if _, ok := h.tt.Find(label); !ok {
					t.Fatalf("missing metrics evidence %q", label)
				}
			}
			if scenario != "forbidden" {
				for _, label := range []string{"Usage app", "CPU cores time range", "Memory MiB time range"} {
					if _, ok := h.tt.Find(label); !ok {
						t.Fatalf("missing usage or observation time %q", label)
					}
				}
			}
			h.click("Sample details")
			if scenario == "forbidden" {
				if _, ok := h.tt.Find("metrics.k8s.io; window 0s"); ok {
					t.Fatal("missing sampling window was shown as a real zero duration")
				}
			} else if _, ok := h.tt.Find("metrics.k8s.io/v1beta1; window 15s"); !ok {
				t.Fatal("sample details lost API version or sampling window")
			}
			h.click("Sample details")
			for _, scale := range []float32{1, 2} {
				h.tt.SetSize(1100, 700)
				h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
				viewport, ok := h.tt.Find("Metrics content")
				if !ok || viewport.H < 100 {
					t.Fatal("metrics content has no usable scroll viewport")
				}
				h.tt.Scroll(viewport.X+8, viewport.Y+8, 0, 4000)
				h.tt.Frame()
				r, ok := h.tt.Find("Sample details")
				if !ok || r.H <= 0 || r.Y < viewport.Y || r.Y+r.H > viewport.Y+viewport.H {
					t.Fatalf("sample details unreachable at scale %g: %+v", scale, r)
				}
				h.tt.Scroll(viewport.X+8, viewport.Y+8, 0, -4000)
				h.tt.Frame()
				if r, ok := h.tt.Find("Refresh metrics"); !ok || r.W <= 0 || r.Y+r.H > 660 {
					t.Fatalf("refresh unreachable at scale %g: %+v", scale, r)
				}
			}
			h.tt.SetSize(1440, 1000)
			h.tt.SetPreferences(ui.Preferences{TextScale: 1, ReduceMotion: true})
			saveNativeScreenshot(t, h.tt)
		})
	}
}

func TestNativeMetricsSnapshotWaitsForHistory(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprint(scale), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(serveMetrics))
			t.Cleanup(server.Close)
			h := newRelationshipHarness(t, 20*time.Second)
			attachRelationshipBackend(t, h, server.URL)
			h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			h.click("Overview")
			h.click("Metrics")
			h.pump(func() bool { return !h.w.metricsLoading })
			if len(h.w.metricsHistory.Points) != 1 {
				t.Fatal("expected one observation")
			}
			for _, metric := range []string{"CPU cores", "Memory MiB"} {
				if _, ok := h.tt.Find(metric + " trend"); ok {
					saveNativeScreenshot(t, h.tt)
					t.Fatal("single observation reserves a trend chart", metric)
				}
				if _, ok := h.tt.Find(metric + " time range"); !ok {
					t.Fatal("snapshot time missing", metric)
				}
			}
			t.Run("snapshot", func(t *testing.T) { saveNativeScreenshot(t, h.tt) })
			h.click("Refresh metrics")
			h.pump(func() bool { return !h.w.metricsLoading })
			if len(h.w.metricsHistory.Points) < 2 {
				t.Fatal("refresh did not retain history")
			}
			for _, metric := range []string{"CPU cores", "Memory MiB"} {
				if _, ok := h.tt.Find(metric + " trend"); !ok {
					t.Fatal("history trend missing", metric)
				}
			}
			saveNativeScreenshot(t, h.tt)
		})
	}
}

func TestNativeMetricsTrendConnectsObservedValues(t *testing.T) {
	w := New()
	t.Cleanup(w.Close)
	w.detail, w.detailKind, w.detailMode = metricsPod(), catalog()[0], "Metrics"
	now := time.Now().UTC()
	for i, value := range []float64{0.2, 0.8} {
		s := resourcemetrics.Snapshot{State: resourcemetrics.Ready, Timestamp: now.Add(time.Duration(i) * time.Second), ReceivedAt: now.Add(time.Duration(i) * time.Second), Total: resourcemetrics.Usage{CPUCores: value, MemoryBytes: value * (1 << 20)}}
		w.metricsResult = s
		w.metricsHistory.Add(s)
	}
	tt := ui.NewTester(w.View, 1440, 1000)
	for _, label := range []string{"CPU cores trend", "Memory MiB trend"} {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatal("missing chart", label)
		}
		x, y := int(r.X+r.W/2), int(r.Y+r.H-3-0.625*(r.H-6))
		found := false
		for dy := -2; dy <= 2; dy++ {
			pixel := tt.Image().RGBAAt(x, y+dy)
			if int(pixel.B)-int(pixel.R) > 40 {
				found = true
			}
		}
		if !found {
			saveNativeScreenshot(t, tt)
			t.Fatal("trend does not pass between observed values", label)
		}
	}
	saveNativeScreenshot(t, tt)
}
