package uiworkbench

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestNativeObjectNavigationHierarchy(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("scale-%g-dark-%t", scale, dark), func(t *testing.T) {
				h := newRelationshipHarness(t, 10*time.Second)
				h.w.detail, h.w.detailKind, h.w.detailMode = modelObject("navigation", "api-worker"), catalog()[0], "Overview"
				h.tt.SetSize(1100, 700)
				h.tt.SetDark(dark)
				h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
				for _, label := range []string{"Edit", "Field ownership"} {
					if _, ok := h.tt.Find(label); ok {
						t.Fatalf("%s exposed outside YAML", label)
					}
				}
				primary, ok := h.tt.Find("Object sections")
				if !ok {
					t.Fatal("missing primary navigation")
				}
				for _, label := range []string{"Overview", "Related", "Events", "YAML"} {
					r, ok := h.tt.Find(label)
					if !ok || r.Y < primary.Y || r.Y+r.H > primary.Y+primary.H {
						t.Fatalf("missing primary section %s", label)
					}
				}
				for _, label := range []string{"Health", "Metrics"} {
					r, ok := h.tt.Find(label)
					if !ok || r.Y < primary.Y+primary.H {
						t.Fatalf("%s is not nested below primary navigation", label)
					}
				}
				h.click("YAML")
				for _, label := range []string{"Health", "Metrics"} {
					if _, ok := h.tt.Find(label); ok {
						t.Fatalf("%s exposed outside Overview", label)
					}
				}
				h.click("Field ownership")
				if h.w.detailMode != "Owners" {
					t.Fatal("ownership view did not open")
				}
				h.click("Read-only")
				if h.w.detailMode != "YAML" {
					t.Fatal("read-only document did not reopen")
				}
				h.click("Edit")
				if h.w.detailMode != "Edit" {
					t.Fatal("editor did not open")
				}
				h.click("Overview")
				if h.w.detailMode != "Overview" {
					t.Fatal("overview did not reopen")
				}
				saveNativeScreenshot(t, h.tt)
			})
		}
	}
}
