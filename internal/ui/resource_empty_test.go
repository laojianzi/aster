package uiworkbench

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kube"
)

func TestNativeResourceToolsUseProgressiveDisclosure(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			t.Run(fmt.Sprintf("dark-%t-scale-%g", dark, scale), func(t *testing.T) {
				h := newRelationshipHarness(t, 10*time.Second)
				h.w.backend = &kube.Backend{}
				h.w.status = "Live"
				h.w.helmJournalPath = t.TempDir()
				h.tt.SetSize(1100, 700)
				h.tt.SetDark(dark)
				h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
				for _, label := range []string{"Deliver manifests", "Helm releases", "Helm recovery"} {
					if _, ok := h.tt.Find(label); ok {
						t.Errorf("secondary action %s is still exposed before opening Tools", label)
					}
				}
				t.Run("closed", func(t *testing.T) { saveNativeScreenshot(t, h.tt) })
				h.click("Tools")
				for _, label := range []string{"Deliver manifests", "Helm releases", "Helm recovery"} {
					r, ok := h.tt.Find(label)
					if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > 1100 || r.Y+r.H > 700 {
						t.Fatalf("tool %s is not reachable inside the viewport: %+v", label, r)
					}
				}
				t.Run("open", func(t *testing.T) { saveNativeScreenshot(t, h.tt) })
				h.tt.Key(0, ui.KeyEscape)
				if _, ok := h.tt.Find("Deliver manifests"); ok || !h.tt.Focused("Tools") {
					t.Fatal("Escape did not close tools and restore focus")
				}
				h.w.pendingWrites = 1
				h.tt.Frame()
				h.click("Tools")
				for _, label := range []string{"Deliver manifests", "Helm releases", "Helm recovery"} {
					_ = h.tt.Click(label)
					if h.w.delivery.open || h.w.helmReleases.open || h.w.helmUninstall.open {
						t.Fatalf("pending write did not block %s", label)
					}
				}
				h.tt.Key(0, ui.KeyEscape)
				h.w.pendingWrites = 0
				h.tt.Frame()
				h.click("Tools")
				h.click("Helm recovery")
				if !h.w.helmUninstall.open || h.w.helmUninstall.mode != "history" {
					t.Fatal("Tools did not open recovery")
				}
			})
		}
	}
}

func TestNativeResourceSortFitsSelectedLabel(t *testing.T) {
	for _, width := range []int{900, 1100} {
		for _, scale := range []float32{1, 2} {
			t.Run(fmt.Sprintf("width-%d-scale-%g", width, scale), func(t *testing.T) {
				w := New()
				t.Cleanup(w.Close)
				w.backend = &kube.Backend{}
				w.status, w.activeNamespace = "Live", "team"
				w.sortBy = "Namespace"
				var labelWidth float32
				tt := ui.NewTester(func(c *ui.Context) {
					w.View(c)
					labelWidth, _ = c.MeasureText(0, ui.Span{Text: w.sortBy})
				}, width, 700)
				tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
				r, ok := tt.Find("Sort resources")
				if !ok || r.W < labelWidth+24 || r.X+r.W > float32(width) {
					t.Errorf("sort label lacks space for text and control: text=%g bounds=%+v", labelWidth, r)
				}
				search, ok := tt.Find("Filter resources")
				if !ok || search.W < 100 || search.X+search.W > float32(width) {
					t.Errorf("sorting crowded out search: %+v", search)
				}
				if err := tt.Click("Sort resources"); err != nil {
					t.Fatal(err)
				}
				tt.Key(0, ui.KeyEscape)
				if w.sortBy != "Namespace" || !tt.Focused("Sort resources") {
					t.Fatal("cancel changed selection or lost focus")
				}
				saveNativeScreenshot(t, tt)
			})
		}
	}
}

func TestNativeResourceEmptyStateStaysNearFilters(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprintf("scale-%g", scale), func(t *testing.T) {
			h := newRelationshipHarness(t, 10*time.Second)
			h.w.status = "Live"
			h.tt.SetSize(1100, 700)
			h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			notice, ok := h.tt.Find("No resources match this scope and filter.")
			if !ok {
				t.Fatal("live empty list has no empty state")
			}
			search, ok := h.tt.Find("Filter resources")
			if !ok {
				t.Fatal("resource search missing")
			}
			if notice.Y < search.Y+search.H || notice.Y > search.Y+search.H+100*scale {
				t.Fatalf("empty state is detached from list controls: search=%+v notice=%+v", search, notice)
			}
			saveNativeScreenshot(t, h.tt)
			for _, status := range []string{"Synchronizing", "Unavailable", "Not connected"} {
				h.w.status = status
				h.tt.Frame()
				if _, ok := h.tt.Find("No resources match this scope and filter."); ok {
					t.Fatalf("%s was presented as successful empty result", status)
				}
			}
			h.w.status = "Live"
			h.w.rows = []resourceRow{project(modelObject("empty-check", "visible-pod"))}
			h.tt.Frame()
			if _, ok := h.tt.Find("No resources match this scope and filter."); ok {
				t.Fatal("empty state remains after a resource arrives")
			}
			if _, ok := h.tt.Find("visible-pod"); !ok {
				t.Fatal("resource arrival did not restore table content")
			}
		})
	}
}

func TestNativeResourceNamespaceScalesWithText(t *testing.T) {
	h := newRelationshipHarness(t, 10*time.Second)
	h.tt.SetSize(1100, 700)
	h.tt.SetPreferences(ui.Preferences{TextScale: 2, ReduceMotion: true})
	namespace, ok := h.tt.Find("Namespace")
	if !ok || namespace.W < 300 {
		t.Fatalf("namespace input cannot show its scaled placeholder: %+v", namespace)
	}
	if namespace.X+namespace.W > 1100 {
		t.Fatal("namespace input exceeds window")
	}
	h.click("Namespace")
	h.tt.Type("team-production")
	if h.w.namespace != "team-production" {
		t.Fatal("scaled namespace input lost native editing")
	}
	saveNativeScreenshot(t, h.tt)
}

func TestNativeDisconnectedResourceStateHasConnectionEntry(t *testing.T) {
	for _, status := range []string{"Not connected", "Disconnected", "No kubeconfig contexts", "Choose a context and connect"} {
		t.Run(status, func(t *testing.T) {
			w := New()
			t.Cleanup(w.Close)
			w.status = status
			tt := ui.NewTester(w.View, 1100, 700)
			for _, scale := range []float32{1, 2} {
				tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
				if tt.HasText("0 / 0 resources") {
					t.Error("unknown resources shown as an empty cluster")
				}
				if _, ok := tt.Find("Open connections"); !ok {
					t.Fatal("disconnected resource task has no connection entry")
				}
				saveNativeScreenshot(t, tt)
			}
			if err := tt.Click("Open connections"); err != nil {
				t.Fatal(err)
			}
			if _, ok := tt.Find("Kubeconfig path"); !ok {
				t.Fatal("connection entry did not open setup")
			}
			if w.backend != nil || w.connectionPending {
				t.Fatal("opening setup started a connection")
			}
		})
	}
	w := New()
	t.Cleanup(w.Close)
	w.status = "Connecting"
	w.connectionPending = true
	tt := ui.NewTester(w.View, 1100, 700)
	if tt.HasText("0 / 0 resources") {
		t.Error("pending list shown as successful empty result")
	}
	if _, ok := tt.Find("Open connections"); ok {
		t.Error("pending connection replaced by setup prompt")
	}
}

func TestNativeDisconnectedActionsReturnWithConnection(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprintf("scale-%g", scale), func(t *testing.T) {
			h := newRelationshipHarness(t, 10*time.Second)
			backend := &kube.Backend{}
			h.tt.SetSize(1100, 700)
			h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			for _, status := range []string{"Not connected", "Disconnected", "No kubeconfig contexts", "Choose a context and connect"} {
				h.w.backend = nil
				h.w.status = status
				h.tt.Frame()
				for _, label := range []string{"Refresh", "New resource", "Deliver manifests", "Helm releases"} {
					if _, ok := h.tt.Find(label); ok {
						t.Errorf("%s exposes unavailable action %s", status, label)
					}
				}
				for _, label := range []string{"Open connections", "Helm recovery"} {
					if _, ok := h.tt.Find(label); !ok {
						t.Errorf("%s lost available action %s", status, label)
					}
				}
			}
			saveNativeScreenshot(t, h.tt)
			h.w.backend = backend
			h.w.status = "Live"
			h.tt.Frame()
			for _, label := range []string{"Refresh", "New resource", "Tools"} {
				if _, ok := h.tt.Find(label); !ok {
					t.Errorf("connected resource task lost action %s", label)
				}
			}
		})
	}
}

func TestNativeDisconnectedFiltersReturnWithConnection(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprint(scale), func(t *testing.T) {
			h := newRelationshipHarness(t, 10*time.Second)
			h.tt.SetSize(1100, 700)
			h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			h.w.filter, h.w.labelSelector, h.w.filtersOpen, h.w.resourceView = "api", "app=api", true, 1
			labels := []string{"Filter resources", "Sort resources", "Filters", "Label selector", "Needs attention", "Focus diagnosis"}
			for _, status := range []string{"Not connected", "Disconnected", "No kubeconfig contexts", "Choose a context and connect"} {
				h.w.status = status
				h.tt.Frame()
				for _, label := range labels {
					if _, ok := h.tt.Find(label); ok {
						t.Errorf("%s exposes %s", status, label)
					}
				}
				if _, ok := h.tt.Find("Open connections"); !ok {
					t.Fatal("missing connection entry")
				}
			}
			saveNativeScreenshot(t, h.tt)
			h.w.backend, h.w.status = &kube.Backend{}, "Live"
			h.tt.Frame()
			for _, label := range labels {
				if _, ok := h.tt.Find(label); !ok {
					t.Errorf("connected page lost %s", label)
				}
			}
			if h.w.filter != "api" || h.w.labelSelector != "app=api" || !h.w.filtersOpen || h.w.resourceView != 1 {
				t.Fatal("connection discarded filter state")
			}
		})
	}
}
