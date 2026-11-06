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
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/relationship"
	"github.com/laojianzi/aster/internal/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func TestNativeApplicationMapChainAndIdentity(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprintf("scale-%g", scale), func(t *testing.T) {
			deployment := modelObject("deployment-uid", "checkout")
			deployment.SetAPIVersion("apps/v1")
			deployment.SetKind("Deployment")
			deployment.SetLabels(map[string]string{"app.kubernetes.io/name": "checkout"})
			rs := relatedReplicaSet("rs-uid")
			rs.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: "checkout", UID: "deployment-uid"}})
			pod := relatedPod()
			pod.SetLabels(map[string]string{"role": "api"})
			pod.Object["spec"] = map[string]any{"containers": []any{map[string]any{"name": "app", "envFrom": []any{
				map[string]any{"configMapRef": map[string]any{"name": "settings-a"}},
				map[string]any{"configMapRef": map[string]any{"name": "settings-b"}},
				map[string]any{"configMapRef": map[string]any{"name": "settings-c"}},
			}}}}
			pod.Object["status"] = map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "app", "state": map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff"}}}}}
			service := modelObject("service-uid", "checkout")
			service.SetKind("Service")
			service.SetLabels(deployment.GetLabels())
			service.Object["spec"] = map[string]any{"selector": map[string]any{"role": "api"}}
			objects := map[string]*unstructured.Unstructured{"deployments": deployment, "replicasets": rs, "pods": pod, "services": service}
			var replaced atomic.Bool
			var healthy atomic.Bool
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != "GET" || strings.Contains(r.URL.Path, "/secrets") || strings.Contains(r.URL.Path, "/configmaps") {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
					http.Error(w, "unexpected", 400)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				for plural, obj := range objects {
					if strings.HasSuffix(r.URL.Path, "/"+plural+"/"+obj.GetName()) {
						result := obj.DeepCopy()
						if healthy.Load() && plural == "pods" {
							result.Object["status"] = map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}
						}
						if replaced.Load() && plural == "pods" {
							result.SetUID("replacement")
						}
						json.MarshalWrite(w, result.Object)
						return
					}
					if strings.HasSuffix(r.URL.Path, "/"+plural) {
						items := []any{}
						if r.URL.Query().Get("labelSelector") != "app.kubernetes.io/name" || obj.GetLabels()["app.kubernetes.io/name"] != "" {
							items = append(items, obj.Object)
						}
						json.MarshalWrite(w, map[string]any{"apiVersion": obj.GetAPIVersion(), "kind": obj.GetKind() + "List", "metadata": map[string]any{"resourceVersion": "1"}, "items": items})
						return
					}
				}
				json.MarshalWrite(w, map[string]any{"apiVersion": "discovery.k8s.io/v1", "kind": "EndpointSliceList", "metadata": map[string]any{"resourceVersion": "1"}, "items": []any{}})
			}))
			t.Cleanup(server.Close)
			h := newRelationshipHarness(t, 20*time.Second)
			attachRelationshipBackend(t, h, server.URL)
			h.w.namespace = "team"
			h.w.kinds = nil
			for _, k := range catalog() {
				if _, ok := objects[k.GVR.Resource]; ok {
					h.w.kinds = append(h.w.kinds, k)
				}
			}
			h.tt.SetSize(1100, 800)
			h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			h.click("Applications")
			h.pump(func() bool { return !h.w.applications.busy })
			h.click("Map")
			h.pump(func() bool { return h.tt.HasText("Map ready") })
			for _, name := range []string{"Map Deployment/checkout", "Map ReplicaSet/owner-rs", "Map Pod/related-pod", "Map Service/checkout"} {
				if _, ok := h.tt.Find(name); !ok {
					t.Fatalf("missing map node %s", name)
				}
			}
			if !h.tt.HasText("owns") || !h.tt.HasText("selects") {
				t.Fatal("relationship semantics missing")
			}
			if requests.Load() > 64+4 {
				t.Fatalf("unbounded graph read: %d", requests.Load())
			}
			for _, label := range []string{"Health Pod/related-pod: Degraded", "Health Service/checkout: Not assessed"} {
				if _, ok := h.tt.Find(label); !ok {
					t.Fatalf("missing observed health: %s", label)
				}
			}
			saveNativeScreenshot(t, h.tt)
			healthy.Store(true)
			h.click("Refresh map")
			h.pump(func() bool { return h.tt.HasText("Map ready") })
			if _, ok := h.tt.Find("Health Pod/related-pod: Ready"); !ok {
				t.Fatal("map refresh retained stale health")
			}
			h.click("Map Pod/related-pod")
			h.pump(func() bool { return h.w.detail != nil && string(h.w.detail.GetUID()) == "pod-uid" })
			beforeReturn := requests.Load()
			h.click("Applications")
			h.pump(func() bool { return !h.w.applications.busy })
			if !h.tt.HasText("Map ready") || h.w.applications.selected != "checkout" {
				t.Fatal("returning from the object lost the application map")
			}
			if requests.Load() != beforeReturn {
				t.Fatal("returning to the map repeated API reads")
			}
			if scale == 2 {
				podBox, _ := h.tt.Find("Map Pod/related-pod")
				before, _ := h.tt.Find("Map ReplicaSet/owner-rs")
				h.tt.Scroll(podBox.X+podBox.W/2, podBox.Y+podBox.H/2, 0, 120)
				scrolled, _ := h.tt.Find("Map ReplicaSet/owner-rs")
				if scrolled.Y >= before.Y {
					t.Fatal("map cannot scroll to an offscreen node")
				}
				h.click("Map ReplicaSet/owner-rs")
				h.pump(func() bool { return h.w.detail != nil && string(h.w.detail.GetUID()) == "rs-uid" })
				h.click("Applications")
				restored, _ := h.tt.Find("Map ReplicaSet/owner-rs")
				if restored.Y != scrolled.Y {
					t.Fatalf("map scroll lost on return: before=%+v after=%+v", scrolled, restored)
				}
				h.click("Members")
				h.click("Map")
				restored, _ = h.tt.Find("Map ReplicaSet/owner-rs")
				if restored.Y != scrolled.Y {
					t.Fatal("map scroll lost across member tab")
				}
				for i := 0; i < 12 && !h.tt.Focused("Map ReplicaSet/owner-rs"); i++ {
					h.tt.Key(0, ui.KeyTab)
				}
				if !h.tt.Focused("Map ReplicaSet/owner-rs") {
					t.Fatal("map node is not keyboard reachable")
				}
				h.tt.Key(0, ui.KeyEnter)
				h.pump(func() bool { return h.w.detail != nil && string(h.w.detail.GetUID()) == "rs-uid" })
				h.click("Applications")
				// Return to the top before opening the first row later in this scenario.
				box, _ := h.tt.Find("Map ReplicaSet/owner-rs")
				h.tt.Scroll(box.X+box.W/2, box.Y+box.H/2, 0, -1000)
			}
			saveNativeScreenshot(t, h.tt)
			h.click("Members")
			if _, ok := h.tt.Find("Open Pod/related-pod"); ok {
				t.Fatal("unlabelled related Pod became an application member")
			}
			h.click("Map")
			replaced.Store(true)
			h.click("Map Pod/related-pod")
			h.pump(func() bool {
				return h.w.errText != "" || h.w.detail != nil && string(h.w.detail.GetUID()) == "replacement"
			})
			if h.w.detail != nil && string(h.w.detail.GetUID()) == "replacement" {
				t.Fatal("map opened a replaced UID")
			}
		})
	}
}

func TestNativeApplicationMapForbiddenIsPartial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("labelSelector") == "app.kubernetes.io/name" {
			pod := relatedPod()
			pod.SetLabels(map[string]string{"app.kubernetes.io/name": "checkout"})
			json.MarshalWrite(w, map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "1"}, "items": []any{pod.Object}})
			return
		}
		w.WriteHeader(403)
		json.MarshalWrite(w, map[string]any{"apiVersion": "v1", "kind": "Status", "status": "Failure", "reason": "Forbidden", "code": 403})
	}))
	t.Cleanup(server.Close)
	h := newRelationshipHarness(t, 20*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.w.namespace = "team"
	h.w.kinds = []kube.ResourceKind{catalog()[0]}
	h.click("Applications")
	h.pump(func() bool { return !h.w.applications.busy })
	h.click("Map")
	h.pump(func() bool { return h.tt.HasText("Partial map") })
	if h.tt.HasText("Map ready") {
		t.Fatal("denied map appears complete")
	}
	if _, ok := h.tt.Find("Health Pod/related-pod: Unknown"); !ok {
		t.Fatal("denied read did not show unknown health")
	}
	saveNativeScreenshot(t, h.tt)
}

func TestNativeApplicationMapTextHasStableLeftEdge(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprintf("scale-%g", scale), func(t *testing.T) {
			w := New()
			t.Cleanup(w.Close)
			w.namespace = "team"
			w.applications = applicationState{open: true, namespace: "team", selected: "checkout", names: []string{"checkout"}, view: 1, graph: applicationMap{status: "Map ready"}}
			for i, name := range []string{"api", "checkout-worker-production-with-a-long-name"} {
				w.applications.graph.nodes = append(w.applications.graph.nodes, relationship.Link{Type: relationship.Type{Kind: "Pod"}, Target: resource.Identity{SessionID: "map-layout", Namespace: "team", Name: name, UID: types.UID(fmt.Sprint(i))}, State: relationship.Verified})
			}
			tt := ui.NewTester(w.View, 1440, 1400)
			tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			tt.Frame()
			saveNativeScreenshot(t, tt)
			if len(w.applications.graph.nodes) != 2 {
				t.Fatal("map fixture lost its nodes")
			}
			for _, node := range w.applications.graph.nodes {
				name := "Pod/" + node.Target.Name
				card, ok := tt.Find("Map " + name)
				if !ok {
					t.Fatal("map node missing", name)
				}
				text, ok := tt.Find("Health " + name + ": Unknown")
				if !ok || text.X-card.X < 7 || text.X-card.X > 10 {
					t.Errorf("%s health left inset = %g, want card padding 8", name, text.X-card.X)
				}
			}
		})
	}
}

func TestNativeApplicationMapRoutesAroundIntermediateNodes(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprintf("scale-%g", scale), func(t *testing.T) {
			w := New()
			t.Cleanup(w.Close)
			w.namespace = "team"
			w.applications = applicationState{open: true, namespace: "team", selected: "checkout", names: []string{"checkout"}, status: "Snapshot", view: 1, graph: applicationMap{status: "Map ready"}}
			for i, kind := range []string{"StatefulSet", "Pod", "Pod", "Pod"} {
				w.applications.graph.nodes = append(w.applications.graph.nodes, relationship.Link{Type: relationship.Type{Kind: kind}, Target: resource.Identity{SessionID: "map-layout", Namespace: "team", Name: fmt.Sprintf("node-%d", i), UID: types.UID(fmt.Sprint(i))}, State: relationship.Verified})
			}
			tt := ui.NewTester(w.View, 1440, 1400)
			tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			baseline := append([]byte(nil), tt.Image().Pix...)
			source, _ := tt.Find("Map StatefulSet/node-0")
			target, _ := tt.Find("Map Pod/node-3")
			middle, _ := tt.Find("Map Pod/node-2")
			w.applications.graph.edges = []applicationEdge{{from: 0, to: 3, relation: "owns"}}
			tt.Frame()
			img := tt.Image()
			changed := 0
			for y := int(middle.Y+middle.H/2) - 8; y < int(middle.Y+middle.H/2)+8; y++ {
				for x := int(source.X+source.W) + 2; x < int(target.X)-2; x++ {
					at := img.PixOffset(x, y)
					if img.Pix[at] != baseline[at] || img.Pix[at+1] != baseline[at+1] || img.Pix[at+2] != baseline[at+2] {
						changed++
					}
				}
			}
			saveNativeScreenshot(t, tt)
			if changed == 0 {
				t.Fatal("ownership route is hidden behind intermediate Pod nodes instead of using the gutter")
			}
		})
	}
}

func TestNativeApplicationMapOwnershipLayers(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprintf("scale-%g", scale), func(t *testing.T) {
			w := New()
			t.Cleanup(w.Close)
			w.namespace = "team"
			w.applications = applicationState{open: true, namespace: "team", selected: "checkout", names: []string{"checkout"}, status: "Snapshot", view: 1, graph: applicationMap{status: "Map ready"}}
			kinds := []string{"Pod", "ReplicaSet", "Deployment", "Job", "ConfigMap", "Service", "CustomChild", "CustomRoot"}
			for i, kind := range kinds {
				w.applications.graph.nodes = append(w.applications.graph.nodes, relationship.Link{Type: relationship.Type{Kind: kind}, Target: resource.Identity{SessionID: "layers", Namespace: "team", Name: fmt.Sprintf("node-%d", i), UID: types.UID(fmt.Sprint(i))}, State: relationship.Verified})
			}
			w.applications.graph.edges = []applicationEdge{{2, 1, "owns"}, {1, 0, "owns"}, {3, 0, "owns"}, {7, 6, "owns"}, {0, 4, "References"}, {5, 0, "selects"}}
			tt := ui.NewTester(w.View, 1440, 1000)
			tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			for _, edge := range w.applications.graph.edges {
				from, okFrom := tt.Find(fmt.Sprintf("Map %s/node-%d", kinds[edge.from], edge.from))
				to, okTo := tt.Find(fmt.Sprintf("Map %s/node-%d", kinds[edge.to], edge.to))
				if !okFrom || !okTo || from.W < 100 || to.W < 100 {
					t.Fatalf("map lost readable nodes: %+v -> %+v", from, to)
				}
				if edge.relation == "owns" && from.X+from.W >= to.X {
					saveNativeScreenshot(t, tt)
					t.Fatalf("ownership chain does not advance: %s -> %s: %+v -> %+v", kinds[edge.from], kinds[edge.to], from, to)
				}
			}
			for _, pair := range [][2]int{{2, 1}, {1, 0}, {7, 6}} {
				parent, _ := tt.Find(fmt.Sprintf("Map %s/node-%d", kinds[pair[0]], pair[0]))
				child, _ := tt.Find(fmt.Sprintf("Map %s/node-%d", kinds[pair[1]], pair[1]))
				if parent.Y != child.Y {
					saveNativeScreenshot(t, tt)
					t.Fatalf("ownership branch loses its row: %+v -> %+v", parent, child)
				}
			}
			for i := range kinds {
				a, _ := tt.Find(fmt.Sprintf("Map %s/node-%d", kinds[i], i))
				for j := i + 1; j < len(kinds); j++ {
					b, _ := tt.Find(fmt.Sprintf("Map %s/node-%d", kinds[j], j))
					if a.X == b.X && a.Y < b.Y+b.H && b.Y < a.Y+a.H {
						t.Fatalf("nodes overlap in one layer: %+v %+v", a, b)
					}
				}
			}

			saveNativeScreenshot(t, tt)
		})
	}
}

func TestNativeApplicationMapReferenceFanout(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprintf("scale-%g", scale), func(t *testing.T) {
			w := New()
			t.Cleanup(w.Close)
			w.applications = applicationState{open: true, selected: "checkout", names: []string{"checkout"}, view: 1, graph: applicationMap{status: "Map ready"}}
			for i, kind := range []string{"Pod", "ConfigMap", "ConfigMap", "ConfigMap"} {
				w.applications.graph.nodes = append(w.applications.graph.nodes, relationship.Link{Type: relationship.Type{Kind: kind}, Target: resource.Identity{SessionID: "fanout", Name: fmt.Sprintf("node-%d", i)}, State: relationship.Verified})
			}
			var descriptions []string
			for i := 1; i < 4; i++ {
				w.applications.graph.edges = append(w.applications.graph.edges, applicationEdge{0, i, "References"})
				descriptions = append(descriptions, fmt.Sprintf("Pod/node-0 References ConfigMap/node-%d", i))
			}
			tt := ui.NewTester(w.View, 1440, 1400)
			tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			saveNativeScreenshot(t, tt)
			if _, ok := tt.Find(strings.Join(descriptions, "\n")); !ok {
				t.Fatal("reference fanout must have one label retaining every target")
			}
			for i := 1; i < 4; i++ {
				if _, ok := tt.Find(fmt.Sprintf("Map ConfigMap/node-%d", i)); !ok {
					t.Fatal("grouping hid a target node")
				}
			}
		})
	}
}

func TestNativeApplicationMapReferenceInspection(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprintf("scale-%g", scale), func(t *testing.T) {
			w := New()
			t.Cleanup(w.Close)
			w.applications = applicationState{open: true, selected: "checkout", names: []string{"checkout"}, view: 1, graph: applicationMap{status: "Map ready"}}
			for i, state := range []relationship.State{relationship.Referenced, relationship.Forbidden} {
				w.applications.graph.nodes = append(w.applications.graph.nodes, relationship.Link{Type: relationship.Type{Kind: "ConfigMap"}, Target: resource.Identity{SessionID: "inspection", Namespace: "team", Name: fmt.Sprintf("checkout-shared-configuration-for-production-region-%d", i)}, State: state, Note: fmt.Sprintf("Reference inspection reason %d", i)})
			}
			tt := ui.NewTester(w.View, 1100, 800)
			tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			for _, node := range w.applications.graph.nodes {
				name := node.Type.Kind + "/" + node.Target.Name
				for _, keyboard := range []bool{false, true} {
					tt.Move(1090, 790)
					if keyboard {
						for range 40 {
							tt.Key(0, ui.KeyTab)
							if tt.Focused("Map " + name) {
								break
							}
						}
						if !tt.Focused("Map " + name) {
							t.Fatal("reference cannot receive keyboard inspection focus")
						}
					} else {
						r, ok := tt.Find("Map " + name)
						if !ok {
							t.Fatal("missing reference node")
						}
						tt.Move(r.X+r.W/2, r.Y+r.H/2)
					}
					tt.Advance(time.Second)
					visible := strings.Join(tt.Texts(), "\n")
					if !strings.Contains(visible, name) || !strings.Contains(visible, node.Note) || !strings.Contains(visible, string(node.State)) {
						saveNativeScreenshot(t, tt)
						t.Fatalf("full reference information unavailable, keyboard=%v: %s", keyboard, visible)
					}
					saveNativeScreenshot(t, tt)
					if keyboard {
						tt.Key(0, ui.KeyEnter)
						tt.Key(0, ui.KeySpace)
					} else if err := tt.Click("Map " + name); err != nil {
						t.Fatal(err)
					}
					if !w.applications.open || w.detail != nil {
						t.Fatal("unverified reference activated resource navigation")
					}
				}
			}
		})
	}
}
