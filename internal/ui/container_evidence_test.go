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

func TestNativeContainerEvidenceSelectsExactPreviousLogTarget(t *testing.T) {
	pod := modelObject("evidence-uid", "worker")
	pod.Object["spec"] = map[string]any{
		"containers":          []any{map[string]any{"name": "app", "image": "fixture"}},
		"initContainers":      []any{map[string]any{"name": "bootstrap", "image": "fixture"}},
		"ephemeralContainers": []any{map[string]any{"name": "debug", "image": "fixture"}},
	}
	pod.Object["status"] = map[string]any{
		"phase":                      "Running",
		"containerStatuses":          []any{map[string]any{"name": "app", "restartCount": int64(3), "state": map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff"}}, "lastState": map[string]any{"terminated": map[string]any{"exitCode": int64(137), "reason": "OOMKilled", "finishedAt": "2026-10-29T10:00:00Z"}}}},
		"initContainerStatuses":      []any{map[string]any{"name": "bootstrap", "restartCount": int64(0), "state": map[string]any{"terminated": map[string]any{"exitCode": int64(0), "reason": "Completed"}}}},
		"ephemeralContainerStatuses": []any{map[string]any{"name": "debug", "restartCount": int64(0), "state": map[string]any{"running": map[string]any{"startedAt": "2026-10-29T10:01:00Z"}}}},
	}
	var logs atomic.Int32
	var replacement atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected write %s", r.Method)
			http.Error(w, "read only", 405)
			return
		}
		switch r.URL.Path {
		case "/api/v1/namespaces/team/pods/worker":
			current := pod.DeepCopy()
			if replacement.Load() {
				current.SetUID("replacement-uid")
			}
			body, _ := json.Marshal(current)
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
		case "/api/v1/namespaces/team/pods/worker/log":
			logs.Add(1)
			if r.URL.Query().Get("container") != "app" || r.URL.Query().Get("previous") != "true" || r.URL.Query().Get("follow") == "true" {
				t.Errorf("incorrect evidence log target: %s", r.URL)
			}
			fmt.Fprintln(w, "fixture previous attempt")
		default:
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	h := newRelationshipHarness(t, 20*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.w.focusDiagnosis = true
	h.w.openResourceKind(catalog()[0], project(pod))
	h.pump(func() bool { return h.w.detail != nil })
	for _, label := range []string{"Container evidence", "Inspect container app", "Inspect init container bootstrap", "Inspect ephemeral container debug", "Previous exit code: 137", "OOMKilled", "Previous termination time: 2026-10-29T10:00:00Z"} {
		if _, ok := h.tt.Find(label); !ok {
			t.Fatalf("missing evidence %q", label)
		}
	}
	firstRow, _ := h.tt.Find("Inspect container app")
	lastRow, _ := h.tt.Find("Inspect ephemeral container debug")
	termination, _ := h.tt.Find("Last termination")
	if lastRow.H < firstRow.H || lastRow.Y+lastRow.H > termination.Y {
		t.Fatal("container table clips the last button row behind the termination evidence")
	}
	if _, ok := h.tt.Find("Resource UID"); ok {
		t.Fatal("metadata displaced primary fault evidence")
	}
	target := "Inspect init container bootstrap"
	for attempts := 0; attempts < 100 && (!h.tt.Focused(target) || h.tt.Focused("Container")); attempts++ {
		h.tt.Key(0, ui.KeyTab)
	}
	if !h.tt.Focused(target) || h.tt.Focused("Container") {
		t.Fatal("Tab did not reach " + target)
	}
	h.tt.Key(0, ui.KeyEnter)

	if _, ok := h.tt.Find("Current exit code: 0"); !ok {
		t.Fatal("completed init termination was omitted")
	}
	h.click("Inspect ephemeral container debug")
	if _, ok := h.tt.Find("No previous termination"); !ok {
		t.Fatal("last termination leaked across containers")
	}
	h.click("Inspect container app")
	if logs.Load() != 0 {
		t.Fatal("viewing evidence started logs without an action")
	}
	for _, scale := range []float32{1, 2} {
		h.tt.SetSize(1100, 700)
		h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
		r, ok := h.tt.Find("Previous logs for app")
		if !ok || r.W <= 0 || r.X+r.W > 1100 || r.Y+r.H > 660 {
			t.Fatalf("container action unreachable at scale %g: %+v", scale, r)
		}
		overview, _ := h.tt.Find("Object overview")
		h.tt.Scroll(overview.X+8, overview.Y+8, 0, 2000)
		h.tt.Frame()
		exit, ok := h.tt.Find("Previous exit code: 137")
		if !ok || exit.Y < overview.Y || exit.Y+exit.H > overview.Y+overview.H {
			t.Fatalf("last termination cannot be reached by scrolling at scale %g: %+v", scale, exit)
		}
		h.tt.Scroll(overview.X+8, overview.Y+8, 0, -2000)
		h.tt.Frame()
	}
	saveNativeScreenshot(t, h.tt)
	h.tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	h.click("Previous logs for app")
	h.pump(func() bool { return logs.Load() == 1 && len(h.w.logRows) == 1 })
	if h.w.diagnostic == nil || h.w.diagnostic.target.UID != "evidence-uid" || h.w.container != "app" {
		t.Fatal("container evidence bound a different diagnostic")
	}
	h.click("Close logs")
	replacement.Store(true)
	h.click("Previous logs for app")
	h.pump(func() bool { return strings.HasPrefix(h.w.logStatus, "Stream ended:") })
	if logs.Load() != 1 {
		t.Fatal("replaced Pod passed the log preflight")
	}
}

func TestNativeContainerEvidencePreservesUnknownAndGVK(t *testing.T) {
	for _, scenario := range []string{"missing", "partial termination", "malformed", "custom kind", "init failure", "completed init"} {
		t.Run(scenario, func(t *testing.T) {
			pod := modelObject("unknown-uid", "unknown-pod")
			pod.Object["spec"] = map[string]any{"containers": []any{map[string]any{"name": "app", "image": "fixture"}}}
			pod.Object["status"] = map[string]any{}
			switch scenario {
			case "partial termination":
				pod.Object["status"] = map[string]any{"containerStatuses": []any{map[string]any{"name": "app", "state": map[string]any{"terminated": map[string]any{"reason": "Unknown"}}}}}
			case "malformed":
				pod.Object["status"] = map[string]any{"containerStatuses": []any{map[string]any{"name": "app", "restartCount": "invalid"}}}
			case "custom kind":
				pod.SetAPIVersion("tenant.example/v1")
			case "completed init":
				pod.Object["spec"].(map[string]any)["initContainers"] = []any{map[string]any{"name": "bootstrap", "image": "fixture"}}
				pod.Object["status"] = map[string]any{
					"containerStatuses":     []any{map[string]any{"name": "app", "state": map[string]any{"running": map[string]any{}}}},
					"initContainerStatuses": []any{map[string]any{"name": "bootstrap", "state": map[string]any{"terminated": map[string]any{"exitCode": int64(0), "reason": "Completed"}}}},
				}
			case "init failure":
				pod.Object["spec"].(map[string]any)["initContainers"] = []any{map[string]any{"name": "bootstrap", "image": "fixture"}}
				pod.Object["status"] = map[string]any{
					"containerStatuses":     []any{map[string]any{"name": "app", "state": map[string]any{"waiting": map[string]any{"reason": "PodInitializing"}}}},
					"initContainerStatuses": []any{map[string]any{"name": "bootstrap", "state": map[string]any{"waiting": map[string]any{"reason": "ErrImagePull"}}}},
				}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := json.Marshal(pod)
				w.Header().Set("Content-Type", "application/json")
				w.Write(body)
			}))
			t.Cleanup(server.Close)
			h := newRelationshipHarness(t, 10*time.Second)
			attachRelationshipBackend(t, h, server.URL)
			kind := catalog()[0]
			if scenario == "custom kind" {
				kind.GVR.Group = "tenant.example"
			}
			h.w.openResourceKind(kind, project(pod))
			h.pump(func() bool { return h.w.detail != nil })
			if scenario == "completed init" {
				if _, ok := h.tt.Find("Follow logs for app"); !ok {
					t.Fatal("completed init displaced the running application container")
				}
				return
			}
			if scenario == "init failure" {
				if _, ok := h.tt.Find("Follow logs for bootstrap"); !ok {
					t.Fatal("blocked app displaced the failing init container")
				}
				return
			}
			if scenario == "custom kind" {
				if _, ok := h.tt.Find("Container evidence"); ok {
					t.Fatal("custom Pod received built-in container semantics")
				}
				return
			}
			if scenario == "malformed" {
				if _, ok := h.tt.Find("Container status unavailable"); !ok {
					t.Fatal("malformed status was shown as usable evidence")
				}
				return
			}
			if _, ok := h.tt.Find("Restart count for app: Unknown"); !ok {
				t.Fatal("missing restart count became zero")
			}
			if scenario == "partial termination" {
				if _, ok := h.tt.Find("Current exit code: Unknown"); !ok {
					t.Fatal("missing exit code became zero")
				}
			}
			if _, ok := h.tt.Find("No previous termination"); !ok {
				t.Fatal("missing previous termination became fabricated evidence")
			}
		})
	}
}

func TestNativeContainerEvidenceReadinessFollowsSelection(t *testing.T) {
	pod := modelObject("readiness-uid", "worker")
	pod.Object["spec"] = map[string]any{"containers": []any{
		map[string]any{"name": "api-server", "image": "fixture"},
		map[string]any{"name": "sidecar", "image": "fixture"},
		map[string]any{"name": "unknown", "image": "fixture"},
	}}
	pod.Object["status"] = map[string]any{"phase": "Running", "containerStatuses": []any{
		map[string]any{"name": "api-server", "ready": false, "state": map[string]any{"running": map[string]any{}}},
		map[string]any{"name": "sidecar", "ready": true, "state": map[string]any{"running": map[string]any{}}},
		map[string]any{"name": "unknown", "state": map[string]any{"running": map[string]any{}}},
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/namespaces/team/pods/worker" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		body, _ := json.Marshal(pod)
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	t.Cleanup(server.Close)
	h := newRelationshipHarness(t, 20*time.Second)
	widths := make(map[string]float32)
	measure := ui.NewTester(func(c *ui.Context) {
		for _, name := range []string{"api-server", "sidecar", "unknown"} {
			widths[name], _ = c.MeasureText(0, ui.Span{Text: name, Weight: 700})
		}
	}, 1100, 700)
	attachRelationshipBackend(t, h, server.URL)
	h.w.focusDiagnosis = true
	h.w.openResourceKind(catalog()[0], project(pod))
	h.pump(func() bool { return h.w.detail != nil })
	for _, scale := range []float32{1, 2} {
		measure.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
		h.tt.SetSize(1100, 700)
		h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
		for _, selection := range []struct{ name, readiness string }{{"api-server", "No"}, {"sidecar", "Yes"}, {"unknown", "Unknown"}, {"api-server", "No"}} {
			overview, _ := h.tt.Find("Object overview")
			h.tt.Scroll(overview.X+8, overview.Y+8, 0, -2000)
			h.tt.Frame()
			for range 20 {
				button, ok := h.tt.Find("Inspect container " + selection.name)
				if ok && button.H >= 20*scale && button.Y >= overview.Y && button.Y+button.H <= overview.Y+overview.H {
					break
				}
				h.tt.Scroll(overview.X+8, overview.Y+8, 0, 60)
				h.tt.Frame()
			}
			h.click("Inspect container " + selection.name)
			h.tt.Scroll(overview.X+8, overview.Y+8, 0, -2000)
			h.tt.Frame()
			heading, visible := h.tt.Find(selection.name)
			if !visible || heading.W+1 < widths[selection.name] || heading.X+heading.W > 1100 {
				saveNativeScreenshot(t, h.tt)
				t.Fatalf("selected container identity truncated at scale %g: %s width=%g bounds=%+v", scale, selection.name, widths[selection.name], heading)
			}
			label := "Readiness for " + selection.name + ": " + selection.readiness
			r, ok := h.tt.Find(label)
			if !ok || r.W <= 0 || r.X+r.W > 1100 || r.Y+r.H > 660 {
				saveNativeScreenshot(t, h.tt)
				t.Fatalf("readiness missing or clipped at scale %g: %s %+v", scale, label, r)
			}
			if _, ok := h.tt.Find("Follow logs for " + selection.name); !ok {
				t.Fatal("readiness displaced the container log action")
			}
		}
		saveNativeScreenshot(t, h.tt)
	}
}
