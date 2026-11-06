package uiworkbench

import (
	"bytes"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNativeMyGo073ToolbarOverflowOpensMenu(t *testing.T) {
	chosen := 0
	disabled := false
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			ui.Toolbar(c.Key("actions"), func() {
				ui.Button(c, "Primary action").Width(165)
				ui.MenuButton(c, "Diagnostics", func(m *ui.Menu) {
					if m.Item("Open logs").Disabled(disabled).Chosen() {
						chosen++
					}
				})
			}).Width(220)
		})
	}, 500, 250)
	if err := tt.Click("More"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Diagnostics"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Open logs"); err != nil {
		t.Fatal(err)
	}
	if chosen != 1 {
		t.Fatalf("nested overflow action ran %d times", chosen)
	}
	disabled = true
	if err := tt.Click("More"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Diagnostics"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Open logs"); err == nil {
		t.Fatal("disabled nested action was selectable")
	}
	if chosen != 1 {
		t.Fatal("disabled action ran")
	}
	saveNativeScreenshot(t, tt)
}

func TestNativeMyGo073SearchPlaceholderAndEditing(t *testing.T) {
	value, placeholder := "", "Find resources"
	tt := ui.NewTester(func(c *ui.Context) {
		ui.SearchField(c.Key("search"), &value).Placeholder(placeholder).Label("Resource search").Width(300)
	}, 400, 100)
	before := bytes.Clone(tt.Image().Pix)
	placeholder = "Search workloads"
	tt.Frame()
	if bytes.Equal(before, tt.Image().Pix) {
		t.Fatal("custom SearchField placeholder did not change rendered output")
	}
	if err := tt.Click("Resource search"); err != nil {
		t.Fatal(err)
	}
	tt.Type("工作负载")
	if value != "工作负载" {
		t.Fatal("native search input was lost")
	}
	tt.Key(0, ui.KeyEscape)
	if value != "" || !tt.Focused("Resource search") {
		t.Fatal("Escape did not clear search and retain focus")
	}
	saveNativeScreenshot(t, tt)
}
