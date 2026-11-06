package uiworkbench

import (
	"fmt"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNativeWorkbenchRadixAppearanceAndButtonContrast(t *testing.T) {
	luminance := func(c ui.Color) float64 {
		channels := []float64{float64(c.R) / 255, float64(c.G) / 255, float64(c.B) / 255}
		for i, v := range channels {
			if v <= 0.04045 {
				channels[i] = v / 12.92
			} else {
				channels[i] = math.Pow((v+0.055)/1.055, 2.4)
			}
		}
		return channels[0]*0.2126 + channels[1]*0.7152 + channels[2]*0.0722
	}
	for _, dark := range []bool{false, true} {
		for _, contrast := range []bool{false, true} {
			for _, scale := range []float32{1, 2} {
				t.Run(fmt.Sprintf("dark-%t-contrast-%t-scale-%g", dark, contrast, scale), func(t *testing.T) {
					w := New()
					t.Cleanup(w.Close)
					w.contexts, w.currentContext = []string{"fixture"}, "fixture"
					var theme ui.Theme
					tt := ui.NewTester(func(c *ui.Context) { w.View(c); theme = *c.Theme() }, 1100, 700)
					baseFont := theme.FontSize
					tt.SetDark(dark)
					tt.SetPreferences(ui.Preferences{TextScale: scale, HighContrast: contrast, ReduceMotion: true})
					if theme.FontSize != baseFont*scale {
						t.Fatalf("text preference lost: %g", theme.FontSize)
					}
					background := ui.Hex("#fcfcfd")
					if dark {
						background = ui.Hex("#111113")
					}
					pixel := tt.Image().RGBAAt(5, 5)
					if pixel.R != background.R || pixel.G != background.G || pixel.B != background.B {
						t.Errorf("root is not Radix slate 1: %+v", pixel)
					}
					button, ok := tt.Find("Connect")
					if !ok {
						t.Fatal("Connect missing")
					}
					for _, state := range []string{"normal", "hover", "pressed"} {
						switch state {
						case "normal":
							tt.Move(1, 1)
						case "hover":
							tt.Move(button.X+8, button.Y+button.H/2)
						case "pressed":
							tt.Press(button.X+8, button.Y+button.H/2)
						}
						tt.Frame()
						p := tt.Image().RGBAAt(int(button.X+8), int(button.Y+button.H/2))
						fg, bg := luminance(theme.AccentText), luminance(ui.Color{R: p.R, G: p.G, B: p.B, A: 255})
						ratio := (max(fg, bg) + 0.05) / (min(fg, bg) + 0.05)
						if ratio < 4.5 {
							t.Errorf("%s button contrast %.3f below 4.5, pixel %+v", state, ratio, p)
						}
					}
					tt.Move(1, 1)
					tt.Release(1, 1)
					if w.backend != nil || w.status != "Not connected" {
						t.Fatal("pointer appearance check started a connection")
					}
					for _, fg := range []ui.Color{theme.Text, theme.TextMuted} {
						for _, bg := range []ui.Color{theme.Background, theme.Surface} {
							a, b := luminance(fg), luminance(bg)
							if (max(a, b)+0.05)/(min(a, b)+0.05) < 4.5 {
								t.Error("body or secondary text contrast below 4.5")
							}
						}
					}
					saveNativeScreenshot(t, tt)
				})
			}
		}
	}
}
