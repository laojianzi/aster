package uiworkbench

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNativeManifestWordNavigationAcrossScripts(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"Chinese", "中文测试", "中文测|试"},
		{"Japanese", "カタカナ", "カタカ|ナ"},
		{"Latin", "alpha_beta", "|alpha_beta"},
		{"Mixed", "api中文", "api中|文"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := New()
			t.Cleanup(w.Close)
			w.detail, w.detailKind, w.detailMode = modelObject("word-uid", "word-check"), catalog()[0], "Edit"
			w.setEditor(tc.text)
			tt := ui.NewTester(w.View, 1100, 700)
			if err := tt.Click("Manifest editor"); err != nil {
				t.Fatal(err)
			}
			tt.Key(ui.Cmd, ui.KeyA)
			tt.Type(tc.text)
			tt.Key(ui.Ctrl, ui.KeyLeft)
			tt.Type("|")
			if got := w.editorText(); got != tc.want {
				t.Fatalf("word navigation: got %q, want %q", got, tc.want)
			}
			tt.Key(ui.Cmd, ui.KeyZ)
			if got := w.editorText(); got != tc.text {
				t.Fatalf("word edit Undo changed the draft: %q", got)
			}
			saveNativeScreenshot(t, tt)
		})
	}
}

func TestNativeManifestVerticalNavigationKeepsCodeColumn(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(map[float32]string{1: "normal", 2: "large"}[scale], func(t *testing.T) {
			w := New()
			t.Cleanup(w.Close)
			w.detail, w.detailKind, w.detailMode = modelObject("font-uid", "font-check"), catalog()[0], "Edit"
			w.setEditor("iiiiiiii\nWWWWWWWW")
			tt := ui.NewTester(w.View, 1440, 960)
			tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			if err := tt.Click("Manifest editor"); err != nil {
				t.Fatal(err)
			}
			tt.Key(ui.Cmd, ui.KeyA)
			tt.Type("iiiiiiii\nWWWWWWWW")
			tt.Key(0, ui.KeyHome)
			tt.Key(0, ui.KeyUp)
			for range 4 {
				tt.Key(0, ui.KeyRight)
			}
			tt.Key(0, ui.KeyDown)
			tt.Type("|")
			saveNativeScreenshot(t, tt)
			if got := w.editorText(); got != "iiiiiiii\nWWWW|WWWW" {
				t.Fatalf("vertical navigation lost the code column: %q", got)
			}
			tt.Key(ui.Cmd, ui.KeyZ)
			if got := w.editorText(); got != "iiiiiiii\nWWWWWWWW" {
				t.Fatalf("Undo changed the original draft: %q", got)
			}
		})
	}
}
