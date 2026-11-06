package uiworkbench

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestNativeObjectRouterPreservesEditorHistory(t *testing.T) {
	router := ui.NewRouter("/first")
	first, second := ui.NewTextBuffer("first"), ui.NewTextBuffer("second")
	tt := ui.NewTester(func(c *ui.Context) {
		router.View(c, func(r *ui.Route) {
			if r.Match("/first") {
				r.Title("First object")
				ui.TextAreaBuffer(c.Key("editor"), first).Label("First draft")
			} else {
				r.Title("Second object")
				ui.TextAreaBuffer(c.Key("editor"), second).Label("Second draft")
			}
		})
	}, 800, 600)
	if err := tt.Click("First draft"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("first edited")
	router.Push("/second")
	tt.Frame()
	if err := tt.Click("Second draft"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("second edited")
	router.Back()
	tt.Frame()
	if !tt.Focused("First draft") {
		t.Fatal("router did not restore editor focus")
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	if first.String() != "first" || second.String() != "second edited" {
		t.Fatal("router lost Undo or crossed editor histories")
	}
	router.Forward()
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyZ)
	if second.String() != "second" || first.String() != "first" {
		t.Fatal("forward navigation lost the second history")
	}
}

func TestNativeHiddenObjectEditorsPreserveSeparateUndo(t *testing.T) {
	first, second := ui.NewTextBuffer("first"), ui.NewTextBuffer("second")
	active := 0
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			ui.TextAreaBuffer(c.Key("first"), first).Label("First draft").Hidden(active != 0).Grow(1)
			ui.TextAreaBuffer(c.Key("second"), second).Label("Second draft").Hidden(active != 1).Grow(1)
		})
	}, 800, 600)
	if err := tt.Click("First draft"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("first edited")
	active = 1
	tt.Frame()
	if err := tt.Click("Second draft"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("second edited")
	active = 0
	tt.Frame()
	if err := tt.Click("First draft"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	if first.String() != "first" || second.String() != "second edited" {
		t.Fatal("hidden editor lost Undo or crossed histories")
	}
	active = 1
	tt.Frame()
	if err := tt.Click("Second draft"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	if second.String() != "second" {
		t.Fatal("second history was not retained")
	}
}
