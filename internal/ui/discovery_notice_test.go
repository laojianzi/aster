package uiworkbench

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestNativeDiscoveryShowsOnlyActionableNotices(t *testing.T) {
	var mode atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/apis":
			if mode.Load() == 1 {
				http.Error(w, "discovery unavailable", 403)
				return
			}
			fmt.Fprint(w, `{"kind":"APIGroupList","apiVersion":"v1","groups":[{"name":"sample.example","preferredVersion":{"groupVersion":"sample.example/v1","version":"v1"}}]}`)
		case "/api/v1":
			fmt.Fprint(w, `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"v1","resources":[{"name":"pods","kind":"Pod","namespaced":true,"verbs":["list","watch"]}]}`)
		case "/apis/sample.example/v1":
			if mode.Load() == 0 {
				http.Error(w, "group unavailable", 403)
				return
			}
			fmt.Fprint(w, `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"sample.example/v1","resources":[{"name":"widgets","kind":"Widget","namespaced":true,"verbs":["list","watch"]}]}`)
		default:
			if r.URL.Query().Get("watch") == "true" {
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				return
			}
			fmt.Fprint(w, `{"kind":"PodList","apiVersion":"v1","metadata":{"resourceVersion":"1"},"items":[]}`)
		}
	}))
	defer server.Close()
	h := newRelationshipHarness(t, 20*time.Second)
	defer h.w.Close()
	h.w.path = filepath.Join(t.TempDir(), "config")
	config := fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: fixture\n  cluster:\n    server: %s\ncontexts:\n- name: fixture\n  context:\n    cluster: fixture\n    namespace: team\ncurrent-context: fixture\n", server.URL)
	if err := os.WriteFile(h.w.path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	h.w.currentContext, h.w.contexts = "fixture", []string{"fixture"}
	for phase, want := range []string{"1 resource types; 1 unavailable API group(s)", "Discovery unavailable; built-in resources remain accessible", ""} {
		mode.Store(int32(phase))
		h.tt.Frame()
		h.click("Connect")
		h.pump(func() bool { return !h.w.connectionPending })
		if h.w.trustRequired {
			h.click("Trust this context and connect")
		}
		h.pump(func() bool {
			if phase == 2 {
				return len(h.w.kinds) == 2
			}
			return h.w.notice != ""
		})
		if h.w.notice != want {
			t.Fatalf("phase %d: notice = %q, want %q", phase, h.w.notice, want)
		}
		for _, scale := range []float32{1, 2} {
			h.tt.SetSize(1100, 700)
			h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			if want != "" {
				r, ok := h.tt.Find(want)
				if !ok || r.X < 0 || r.X+r.W > 1100 || r.Y+r.H > 700 {
					t.Fatalf("warning clipped: %+v", r)
				}
			}
			t.Run(fmt.Sprintf("phase-%d-scale-%g", phase, scale), func(t *testing.T) { saveNativeScreenshot(t, h.tt) })
		}
		if h.w.activeContext != "fixture" || h.w.namespace != "team" {
			t.Fatal("discovery changed scope")
		}
		if phase == 2 {
			h.click("Custom resources")
			if _, ok := h.tt.Find("Widget"); !ok {
				t.Fatal("successful discovery did not populate navigation")
			}
		}
		h.click("Disconnect")
	}
}
