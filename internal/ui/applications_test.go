package uiworkbench

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kube"
)

func TestNativeApplicationsLongNamesAtMinimumWindow(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprintf("scale-%g", scale), func(t *testing.T) {
			name := "checkout-" + strings.Repeat("worker-", 12) + "production"
			obj := modelObject("application-scale", name)
			obj.SetKind("StatefulSet")
			obj.SetAPIVersion("apps/v1")
			obj.SetLabels(map[string]string{"app.kubernetes.io/name": "checkout-production"})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("unexpected mutation: %s", r.Method)
					http.Error(w, "read only", 405)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/"+name) {
					json.NewEncoder(w).Encode(obj)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"apiVersion": "apps/v1", "kind": "StatefulSetList", "metadata": map[string]any{"resourceVersion": "1"}, "items": []any{obj}})
			}))
			t.Cleanup(server.Close)
			h := newRelationshipHarness(t, 20*time.Second)
			attachRelationshipBackend(t, h, server.URL)
			h.w.namespace = "team"
			h.w.kinds = []kube.ResourceKind{catalog()[2]}
			h.tt.SetSize(936, 700)
			h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			h.click("Applications")
			h.pump(func() bool { return !h.w.applications.busy })
			h.click("Application checkout-production")
			button, ok := h.tt.Find("Open StatefulSet/" + name)
			if !ok || button.W < 140*scale || button.X+button.W > 936 || button.Y+button.H > 700 {
				saveNativeScreenshot(t, h.tt)
				t.Fatalf("application member is unreadable at scale %g: %+v", scale, button)
			}
			h.tt.Scroll(button.X, button.Y, 1000, 0)
			h.tt.Frame()
			status, ok := h.tt.Find("Progressing")
			if !ok || status.W < 75*scale {
				saveNativeScreenshot(t, h.tt)
				t.Fatalf("application status is unreachable after horizontal scroll: %+v", status)
			}
			h.tt.Scroll(800, button.Y, -1000, 0)
			h.tt.Frame()
			saveNativeScreenshot(t, h.tt)
			target := "Open StatefulSet/" + name
			for attempts := 0; attempts < 100 && (!h.tt.Focused(target) || h.tt.Focused("Name")); attempts++ {
				h.tt.Key(0, ui.KeyTab)
			}
			if !h.tt.Focused(target) || h.tt.Focused("Name") {
				t.Fatal("Tab did not reach " + target)
			}
			h.tt.Key(0, ui.KeyEnter)
			if h.w.applications.open {
				t.Fatal("Enter did not open the application member")
			}
			h.pump(func() bool { return h.w.detail != nil && string(h.w.detail.GetUID()) == "application-scale" })
			if h.w.detailKind.Kind != "StatefulSet" {
				t.Fatal("scaled application navigation opened the wrong kind")
			}
		})
	}
}

func TestNativeApplicationsPaginateGroupAndOpenVerifiedObject(t *testing.T) {
	pod := modelObject("app-pod", "api")
	pod.SetLabels(map[string]string{"app.kubernetes.io/name": "checkout"})
	other := modelObject("other-pod", "worker")
	other.SetLabels(map[string]string{"app.kubernetes.io/name": "billing"})
	unlabelled := modelObject("unlabelled", "not-an-application")
	service := pod.DeepCopy()
	service.SetKind("Service")
	service.SetUID("app-service")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("unexpected mutation %s", r.Method)
			http.Error(w, "read only", 405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/namespaces/team/pods/api" {
			json.NewEncoder(w).Encode(pod)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/v1/namespaces/team/") || r.URL.Query().Get("labelSelector") != "app.kubernetes.io/name" {
			t.Errorf("unscoped application read: %s", r.URL)
			http.NotFound(w, r)
			return
		}
		items := []any{service}
		kind, token := "ServiceList", ""
		if strings.HasSuffix(r.URL.Path, "/pods") {
			kind = "PodList"
			if r.URL.Query().Get("continue") == "next" {
				items = []any{other}
			} else {
				items = []any{pod, unlabelled}
				token = "next"
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": kind, "metadata": map[string]any{"resourceVersion": "1", "continue": token}, "items": items})
	}))
	t.Cleanup(server.Close)
	h := newRelationshipHarness(t, 20*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.w.namespace = "team"
	h.w.kinds = []kube.ResourceKind{catalog()[0], catalog()[6]}
	h.click("Applications")
	h.pump(func() bool { _, ok := h.tt.Find("Application checkout"); return ok })
	if _, ok := h.tt.Find("Application billing"); !ok {
		t.Fatal("second list page lost")
	}
	if h.tt.HasText("not-an-application") {
		t.Fatal("unlabelled resource inferred into an application")
	}
	h.click("Application checkout")
	for _, label := range []string{"Open Pod/api", "Open Service/api"} {
		if _, ok := h.tt.Find(label); !ok {
			t.Fatalf("application member missing: %s", label)
		}
	}
	status, _ := h.tt.Find("2 applications / 3 resources")
	refresh, _ := h.tt.Find("Refresh applications")
	if status.Y >= refresh.Y+refresh.H || status.Y+status.H <= refresh.Y {
		saveNativeScreenshot(t, h.tt)
		t.Fatalf("application summary consumes a separate row: status=%+v refresh=%+v", status, refresh)
	}
	if h.tt.HasText("app.kubernetes.io/name") {
		saveNativeScreenshot(t, h.tt)
		t.Fatal("grouping implementation detail occupies a permanent application heading row")
	}
	member, _ := h.tt.Find("Open Pod/api")
	if member.Y > status.Y+status.H+120 {
		saveNativeScreenshot(t, h.tt)
		t.Fatalf("application members detached from heading: status=%+v member=%+v", status, member)
	}
	saveNativeScreenshot(t, h.tt)
	h.click("Open Pod/api")
	h.pump(func() bool { return h.w.detail != nil && string(h.w.detail.GetUID()) == "app-pod" })
	if h.w.detailKind.Kind != "Pod" {
		t.Fatal("application navigation opened wrong kind")
	}
	h.click("Applications")
	h.pump(func() bool { _, ok := h.tt.Find("Application checkout"); return ok })
	h.click("Namespace")
	h.tt.Type("-other")
	h.tt.Frame()
	if _, ok := h.tt.Find("Application checkout"); ok {
		t.Fatal("old application scope survived namespace change")
	}
}

func TestNativeApplicationsReadBoundaries(t *testing.T) {
	for _, scenario := range []string{"forbidden", "empty", "all-namespaces", "page-budget"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count := calls.Add(1)
				if r.Method != "GET" || strings.Contains(r.URL.Path, "/secrets") {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.Error(w, "unexpected", 400)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if scenario == "forbidden" {
					w.WriteHeader(http.StatusForbidden)
					json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "Status", "status": "Failure", "reason": "Forbidden", "message": "list denied", "code": 403})
					return
				}
				var items []any
				token := ""
				if scenario == "page-budget" {
					pod := modelObject(fmt.Sprint(count), fmt.Sprintf("api-%d", count))
					pod.SetLabels(map[string]string{"app.kubernetes.io/name": "checkout"})
					items = append(items, pod)
					token = fmt.Sprint(count)
				}
				json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "1", "continue": token}, "items": items})
			}))
			t.Cleanup(server.Close)
			h := newRelationshipHarness(t, 20*time.Second)
			attachRelationshipBackend(t, h, server.URL)
			h.w.namespace = "team"
			for _, kind := range catalog() {
				if kind.GVR.Resource == "secrets" {
					h.w.kinds = []kube.ResourceKind{catalog()[0], kind}
					break
				}
			}
			if scenario == "all-namespaces" {
				h.w.namespace = "*"
			}
			h.click("Applications")
			expected := "Partial results"
			if scenario == "forbidden" {
				expected = "No applications loaded"
			}
			if scenario == "empty" {
				expected = "No labelled applications in this namespace."
			}
			if scenario == "all-namespaces" {
				expected = "Choose a namespace to browse applications."
			}
			h.pump(func() bool { return !h.w.applications.busy })
			if !h.tt.HasText(expected) {
				t.Errorf("application state missing %q: %s", expected, h.w.applications.status)
			}
			if scenario != "page-budget" {
				for _, label := range []string{"Application navigation", "Members", "Map"} {
					if _, ok := h.tt.Find(label); ok {
						t.Errorf("empty application result exposes %s", label)
					}
				}
			} else if _, ok := h.tt.Find("Application navigation"); !ok {
				t.Fatal("partial application results are inaccessible")
			}
			if scenario == "all-namespaces" && calls.Load() != 0 {
				t.Fatal("implicit cluster-wide application scan")
			}
			if scenario == "page-budget" && calls.Load() != 64 {
				t.Fatalf("unbounded pagination: %d", calls.Load())
			}
			if scenario == "forbidden" {
				h.click("Read issues")
				if !h.tt.HasText("list denied") {
					t.Fatal("permission failure hidden")
				}
			}
			saveNativeScreenshot(t, h.tt)
		})
	}
}
