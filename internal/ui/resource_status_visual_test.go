package uiworkbench

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/health"
	"github.com/laojianzi/aster/internal/kube"
)

func TestNativeResourceStatusEmphasizesObservedFailures(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, contrast := range []bool{false, true} {
			for _, scale := range []float32{1, 2} {
				t.Run(fmt.Sprintf("dark-%t-contrast-%t-scale-%g", dark, contrast, scale), func(t *testing.T) {
					w := New()
					t.Cleanup(w.Close)
					w.backend = &kube.Backend{}
					w.status = "Live"
					w.activeContext = "fixture"
					w.namespace, w.activeNamespace = "commerce", "commerce"
					w.rows = []resourceRow{
						{UID: "ready", Name: "api-ready", Status: "Running", Health: health.Ready},
						{UID: "degraded", Name: "api-degraded", Status: "CreateContainerConfigError", Health: health.Degraded},
						{UID: "failed", Name: "api-failed", Status: "Error", Health: health.Failed},
						{UID: "pending", Name: "api-pending", Status: "Pending", Health: health.Progressing},
						{UID: "unknown", Name: "api-unknown", Status: "Unknown", Health: health.Unknown},
					}
					w.total = len(w.rows)
					var theme ui.Theme
					tt := ui.NewTester(func(c *ui.Context) { w.View(c); theme = *c.Theme() }, 1100, 800)
					tt.SetDark(dark)
					tt.SetPreferences(ui.Preferences{TextScale: scale, HighContrast: contrast, ReduceMotion: true})
					for _, attention := range []bool{false, true} {
						if attention {
							w.resourceView = 1
							tt.Frame()
						}
						img := tt.Image()
						for _, row := range w.rows {
							label := row.Status
							if attention {
								label = string(row.Health)
							}
							r, ok := tt.Find(label)
							if !ok || r.X < 0 || r.X+r.W > 1100 || r.Y+r.H > 800 {
								t.Fatalf("status text missing or clipped: %s %+v", label, r)
							}
							pixels := 0
							for y := int(r.Y); y < int(r.Y+r.H); y++ {
								for x := int(r.X); x < int(r.X+r.W); x++ {
									p := img.RGBAAt(x, y)
									if p.R == theme.Danger.R && p.G == theme.Danger.G && p.B == theme.Danger.B {
										pixels++
									}
								}
							}
							failure := row.Health == health.Degraded || row.Health == health.Failed
							if (pixels > 0) != failure {
								t.Errorf("status %s: error emphasis=%v, want %v", label, pixels > 0, failure)
							}
						}
						if !attention {
							saveNativeScreenshot(t, tt)
						}
					}
				})
			}
		}
	}
}
