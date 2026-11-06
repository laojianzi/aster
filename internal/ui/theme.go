package uiworkbench

import "github.com/egoist/mygo/ui"

func workbenchTheme(c *ui.Context) *ui.Theme {
	t := *c.Theme()
	t.Background = ui.Hex("#fcfcfd")
	t.Surface = ui.Hex("#f0f0f3")
	t.SurfaceHover = ui.Hex("#e8e8ec")
	t.SurfacePressed = ui.Hex("#e0e1e6")
	t.Border = ui.Hex("#cdced6")
	t.Text = ui.Hex("#1c2024")
	t.TextMuted = ui.Hex("#60646c")
	t.Danger = ui.Hex("#ce2c31")
	t.Warning = ui.Hex("#4f3422")
	t.Success = ui.Hex("#1d3b31")
	t.Selection = ui.Hex("#d2deff")
	t.Focus = ui.Hex("#3a5bc7")
	t.Scrollbar = ui.Hex("#8b8d98")
	if t.Dark {
		t.Background = ui.Hex("#111113")
		t.Surface = ui.Hex("#212225")
		t.SurfaceHover = ui.Hex("#272a2d")
		t.SurfacePressed = ui.Hex("#2e3135")
		t.Border = ui.Hex("#43484e")
		t.Text = ui.Hex("#edeef0")
		t.TextMuted = ui.Hex("#b0b4ba")
		t.Danger = ui.Hex("#ff9592")
		t.Warning = ui.Hex("#ffca16")
		t.Success = ui.Hex("#1fd8a4")
		t.Selection = ui.Hex("#253974")
		t.Focus = ui.Hex("#9eb1ff")
		t.Scrollbar = ui.Hex("#696e77")
	}
	// Radix light indigo fills keep white button text above 4.5:1 in both modes.
	t.Accent = ui.Hex("#3e63dd")
	t.AccentHover = ui.Hex("#3358d4")
	t.AccentPressed = ui.Hex("#1f2d5c")
	t.AccentText = ui.Hex("#ffffff")
	t.Inverse, t.InverseText = t.Text, t.Background
	if c.Preferences().HighContrast {
		t.Border, t.TextMuted, t.Focus = t.Text, t.Text, t.Text
	}
	return &t
}
