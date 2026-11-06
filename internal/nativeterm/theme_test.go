package nativeterm

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestTerminalNativeRadixPaletteAndRemoteColors(t *testing.T) {
	term, _ := testTerm(t)
	tt := ui.NewTester(func(c *ui.Context) { View(c, term).Fill() }, 640, 280)
	term.Feed([]byte("\x1b[?25lKEEP_CONTENT\r\n"))
	for _, dark := range []bool{false, true, false} {
		tt.SetDark(dark)
		term.Feed([]byte("\x1b[2;1H\x1b[41m        \x1b[0m\r\n\x1b[48;2;18;52;86m        \x1b[0m\r\n\x1b[48;5;196m        \x1b[0m"))
		tt.Frame()
		bg, red := ui.Hex("#fcfcfd"), ui.Hex("#ce2c31")
		if dark {
			bg, red = ui.Hex("#111113"), ui.Hex("#ff9592")
		}
		for _, want := range []ui.Color{bg, red, ui.Hex("#123456"), ui.Hex("#ff0000")} {
			count := 0
			img := tt.Image()
			for y := 0; y < img.Bounds().Dy(); y++ {
				for x := 0; x < img.Bounds().Dx(); x++ {
					p := img.RGBAAt(x, y)
					if p.R == want.R && p.G == want.G && p.B == want.B {
						count++
					}
				}
			}
			if count < 100 {
				t.Errorf("dark=%t color %+v has only %d rendered pixels", dark, want, count)
			}
		}
		if !strings.Contains(term.Text(), "KEEP_CONTENT") {
			t.Fatal("appearance switch lost terminal content")
		}
		if dir := os.Getenv("ASTER_TEST_ARTIFACTS"); dir != "" {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			f, err := os.Create(filepath.Join(dir, fmt.Sprintf("terminal-dark-%t.png", dark)))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, tt.Image())
			closeErr := f.Close()
			if err != nil {
				t.Fatal(err)
			}
			if closeErr != nil {
				t.Fatal(closeErr)
			}
		}
	}
	term.Feed([]byte("\x1b]4;1;rgb:12/34/56\a\x1b[2;1H\x1b[41m        \x1b[0m"))
	tt.Frame()
	for range 3 {
		tt.Frame()
	}
	count := 0
	img := tt.Image()
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			p := img.RGBAAt(x, y)
			if p.R == 18 && p.G == 52 && p.B == 86 {
				count++
			}
		}
	}
	if count < 1500 {
		t.Fatalf("remote palette override lost on repeated frame: %d pixels", count)
	}
}
