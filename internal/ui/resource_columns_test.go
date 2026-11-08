package uiworkbench

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestNativeResourceNamespaceColumnAtTextScales(t *testing.T) {
	for _, scale := range []float32{1, 1.25, 1.5, 2} {
		t.Run(fmt.Sprintf("scale-%g", scale), func(t *testing.T) {
			h := newRelationshipHarness(t, 10*time.Second)
			row := project(modelObject("column-probe", "checkout-api"))
			row.Namespace = "team-production"
			row.Status = "Running"
			h.w.rows = []resourceRow{row}
			h.w.total = 1
			h.tt.SetSize(1100, 700)
			h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			namespace, ok := h.tt.Find("team-production")
			// The column must show the whole namespace label, not a width in
			// pixels that only one host font happens to produce.
			if !ok || namespace.W <= 0 || namespace.H <= 0 || namespace.X < 0 ||
				namespace.X+namespace.W > 1100 {
				t.Fatalf("namespace cell cannot fit scaled header: %+v", namespace)
			}
			name, ok := h.tt.Find("checkout-api")
			// Find matches the exact label, so a hit proves the whole resource
			// name was laid out. The name column must end before the namespace
			// column begins: that ordering is a layout fact, whereas a width
			// in pixels only measures one host font.
			if !ok || name.W <= 0 || name.H <= 0 || name.X < 0 ||
				name.X+name.W > 1100 || name.X+name.W > namespace.X {
				t.Fatalf("namespace column crowded out resource name: %+v", name)
			}
			status, ok := h.tt.Find("Running")
			if !ok || status.X+status.W > 1100 {
				t.Fatalf("status exceeds window: %+v", status)
			}
			saveNativeScreenshot(t, h.tt)
		})
	}
}

func TestNativeFooterShowsOnlyActiveWrites(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprintf("scale-%g", scale), func(t *testing.T) {
			h := newRelationshipHarness(t, 10*time.Second)
			h.w.activeContext, h.w.activeNamespace = "fixture-cluster", "team"
			h.tt.SetSize(1100, 700)
			h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			for _, count := range []int{0, 1, 2, 0} {
				h.w.pendingWrites = count
				h.tt.Frame()
				_, shown := h.tt.Find(fmt.Sprintf("%d active write(s)", count))
				if shown != (count > 0) {
					t.Errorf("active writes %d: counter visibility %v", count, shown)
				}
				for _, label := range []string{"Connection status", "Active connection"} {
					if _, ok := h.tt.Find(label); !ok {
						t.Errorf("footer lost %s", label)
					}
				}
				saveNativeScreenshot(t, h.tt)
			}
		})
	}
}

func TestNativeResourceColumnsFollowLoadedScope(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprintf("scale-%g", scale), func(t *testing.T) {
			h := newRelationshipHarness(t, 10*time.Second)
			row := project(modelObject("scope-probe", "checkout-worker-production-europe-primary-background-reconciliation"))
			row.Namespace, row.Status = "team-production", "Running"
			h.w.rows, h.w.total = []resourceRow{row}, 1
			h.tt.SetSize(1100, 700)
			h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			for _, scope := range []string{"all namespaces", "team-production", "all namespaces", "cluster-scoped"} {
				h.w.activeNamespace = scope
				h.w.currentKind.Namespaced = scope != "cluster-scoped"
				h.w.namespace = "unapplied-scope"
				h.tt.Frame()
				_, hasNamespace := h.tt.Find("team-production")
				if hasNamespace != (scope == "all namespaces") {
					t.Fatalf("scope %s: namespace cell visibility %v", scope, hasNamespace)
				}
				if _, ok := h.tt.Find("checkout-worker-production-europe-primary-background-reconciliation"); !ok {
					t.Fatal("resource name missing")
				}
				if _, ok := h.tt.Find("Running"); !ok {
					t.Fatal("column change lost resource status")
				}
				saveNativeScreenshot(t, h.tt)
			}
		})
	}
}

func TestNativeResourceNamesStayQuietAndStatusScales(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			t.Run(fmt.Sprintf("dark-%t-scale-%g", dark, scale), func(t *testing.T) {
				h := newRelationshipHarness(t, 10*time.Second)
				row := project(modelObject("link-probe", "checkout-api"))
				row.Status = "CreateContainerConfigError"
				h.w.rows, h.w.total = []resourceRow{row}, 1
				h.w.activeNamespace = "team"
				h.tt.SetSize(1100, 700)
				h.tt.SetDark(dark)
				h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
				name, ok := h.tt.Find("checkout-api")
				if !ok || name.H < 24 {
					t.Fatal("resource navigation target missing or too short")
				}
				img := h.tt.Image()
				if img.RGBAAt(int(name.X+2), int(name.Y+2)) != img.RGBAAt(1, 1) {
					t.Error("resource name still has a separate button face")
				}
				status, ok := h.tt.Find("CreateContainerConfigError")
				if !ok || status.W <= 0 || status.H <= 0 || status.X < 0 ||
					status.X+status.W > 1100 {
					t.Errorf("long status lacks scaled space: %+v", status)
				}
				saveNativeScreenshot(t, h.tt)
			})
		}
	}
}
