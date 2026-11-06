package uiworkbench

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestNativeDiagnosisCountRequiresSuccessfulRead(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprintf("scale-%g", scale), func(t *testing.T) {
			var phase atomic.Int32
			release := make(chan struct{})
			pod := modelObject("diagnosis-count-uid", "pending-pod")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("watch") == "true" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				switch phase.Load() {
				case 0:
					http.Error(w, "forbidden fixture", http.StatusForbidden)
					return
				case 1:
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/pending-pod") {
					json.MarshalWrite(w, pod)
					return
				}
				items := []any{}
				if phase.Load() == 2 {
					items = append(items, pod)
				}
				json.MarshalWrite(w, map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "1"}, "items": items})
			}))
			t.Cleanup(server.Close)
			h := newRelationshipHarness(t, 15*time.Second)
			attachRelationshipBackend(t, h, server.URL)
			h.tt.SetSize(1100, 800)
			h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			h.w.namespace = "team"
			h.w.startScope()
			h.pump(func() bool { return h.w.status == "Unavailable" })
			h.click("Needs attention")
			h.click("Focus diagnosis")
			for _, stage := range []string{"failed", "loading"} {
				if stage == "loading" {
					phase.Store(1)
					h.click("Refresh diagnosis")
					h.pump(func() bool { return h.w.status == "Synchronizing" })
				}
				for _, text := range h.tt.Texts() {
					if strings.HasPrefix(text, "Needs attention (") {
						saveNativeScreenshot(t, h.tt)
						t.Fatalf("%s read claims a measured count: %s", stage, text)
					}
				}
				if stage == "failed" && !h.tt.HasText("Diagnosis unavailable") {
					t.Fatal("lost read failure")
				}
				saveNativeScreenshot(t, h.tt)
			}
			close(release)
			h.pump(func() bool { return h.w.status == "Live" })
			if !h.tt.HasText("Needs attention (0)") || !h.tt.HasText("No resources need attention") {
				t.Fatal("successful empty read lost its zero result")
			}
			phase.Store(2)
			h.click("Refresh diagnosis")
			h.pump(func() bool { return h.w.status == "Live" && len(h.w.rows) == 1 })
			if !h.tt.HasText("Needs attention (1)") {
				t.Fatal("successful read lost its count")
			}
			h.click("Diagnose team/pending-pod")
			h.pump(func() bool { return h.w.detail != nil && h.w.detail.GetUID() == "diagnosis-count-uid" })
			saveNativeScreenshot(t, h.tt)
		})
	}
}
