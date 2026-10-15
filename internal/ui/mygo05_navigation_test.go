package uiworkbench

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

// A window-bound button can still overlap the footer. Exercise the navigation
// viewport separately, including reaching items that need scrolling at scale.
func TestNativeMyGo05ScaledNavigationStaysAboveStatus(t *testing.T) {
	for _, scale := range []float32{1, 1.25, 1.5} {
		for _, rtl := range []bool{false, true} {
			t.Run(fmt.Sprintf("scale-%g-rtl-%t", scale, rtl), func(t *testing.T) {
				w := New()
				defer w.Close()
				tt := ui.NewTester(w.View, 1100, 700)
				if rtl {
					tt.SetLocale("ar")
					tt.SetDirection(ui.RightToLeft)
				}
				tt.SetPreferences(ui.Preferences{TextScale: scale, HighContrast: true, ReduceMotion: true})
				requireVisible := func(label string) ui.Rect {
					t.Helper()
					r, ok := tt.Find(label)
					status, exists := tt.Find("Connection status")
					if !ok || !exists || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 1100 || r.Y+r.H > status.Y || status.Y+status.H > 700 {
						t.Fatalf("navigation %q overlaps status or is unreachable: item=%+v status=%+v", label, r, status)
					}
					return r
				}
				firstVisible := requireVisible("Pods")
				// Use the real pointer/wheel path. The scroll must be confined
				// to the sidebar; no model state or private widget is poked.
				first, _ := tt.Find("Pods")
				x, y := first.X+first.W/2, first.Y+first.H/2
				tt.Scroll(x, y, 0, 4000)
				tt.Frame()
				last := requireVisible("Namespaces")
				nav, ok := tt.Find("Resource navigation")
				status, _ := tt.Find("Connection status")
				if !ok || nav.Y < 0 || nav.H <= 0 || nav.Y+nav.H > status.Y || last.Y < nav.Y || last.Y+last.H > nav.Y+nav.H || last.H < firstVisible.H {
					t.Fatalf("navigation viewport or last label is clipped: viewport=%+v first=%+v last=%+v status=%+v", nav, firstVisible, last, status)
				}
				saveNativeScreenshot(t, tt)
				if err := tt.Click("Namespaces"); err != nil {
					t.Fatal(err)
				}
				if w.currentKind.GVR.Resource != "namespaces" {
					t.Fatal("visible last navigation item could not be activated")
				}
				// Home routes through the focused sidebar button's scroll
				// ancestor. Fresh keyboard navigation must reach the start.
				tt.Key(0, ui.KeyHome)
				tt.Frame()
				requireVisible("Pods")
				if err := tt.Click("Pods"); err != nil {
					t.Fatal(err)
				}
				if w.currentKind.GVR.Resource != "pods" {
					t.Fatal("navigation could not return to the first item")
				}
			})
		}
	}
}
