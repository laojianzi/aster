package nativeterm

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestTerminalNativeClearMenuPreservesPromptAndAlternateScreen(t *testing.T) {
	for _, alternate := range []bool{false, true} {
		t.Run(map[bool]string{false: "prompt", true: "alternate"}[alternate], func(t *testing.T) {
			term, peer := testTerm(t)
			tt := ui.NewTester(func(c *ui.Context) { View(c, term).Fill() }, 640, 280)
			tt.Frame()
			if alternate {
				term.Feed([]byte("\x1b[?1049h"))
			}
			term.Feed([]byte(strings.Repeat("OLD_OUTPUT\r\n", 30) + "PROMPT> "))
			tt.Frame()
			before := term.Text()
			if !strings.Contains(before, "OLD_OUTPUT") || !strings.Contains(before, "PROMPT>") {
				t.Fatalf("fixture missing output: %q", before)
			}
			if err := tt.RightClick("Remote terminal screen"); err != nil {
				t.Fatal(err)
			}
			err := tt.ChooseMenuItem("Clear terminal")
			if alternate {
				if err == nil || !strings.Contains(err.Error(), "is disabled") {
					t.Fatalf("alternate screen clear must be disabled: %v", err)
				}
				tt.CloseMenu()
			} else if err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			got := term.Text()
			if alternate {
				if got != before {
					t.Fatalf("clear changed alternate screen: %q", got)
				}
			} else if strings.TrimSpace(got) != "PROMPT>" {
				t.Fatalf("clear did not retain only prompt: %q", got)
			}
			if input := terminalBarrier(t, term, peer); len(input) != 0 {
				t.Fatalf("clear menu sent remote input: %q", input)
			}
			term.Feed([]byte("NEXT_OUTPUT"))
			tt.Frame()
			if !strings.Contains(term.Text(), "PROMPT> NEXT_OUTPUT") {
				t.Fatalf("output lost cursor position: %q", term.Text())
			}
			if dir := os.Getenv("ASTER_TEST_ARTIFACTS"); dir != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				f, err := os.Create(filepath.Join(dir, strings.ReplaceAll(t.Name(), "/", "_")+".png"))
				if err != nil {
					t.Fatal(err)
				}
				err = png.Encode(f, tt.Image())
				closeErr := f.Close()
				if err != nil || closeErr != nil {
					t.Fatalf("save screenshot: %v, %v", err, closeErr)
				}
			}
		})
	}
}

func TestTerminalNativeRemoteImagesStayDisabled(t *testing.T) {
	term, peer := testTerm(t)
	tt := ui.NewTester(func(c *ui.Context) { View(c, term).Fill() }, 400, 200)
	term.Feed([]byte("\x1b[?25lTEXT_ONLY\r\n"))
	tt.Frame()
	before := bytes.Clone(tt.Image().Pix)
	pixels := bytes.Repeat([]byte{255, 0, 0, 255}, 64*32)
	term.Feed([]byte("\x1b_Ga=T,f=32,s=64,v=32,i=7,q=2,C=1;" + base64.StdEncoding.EncodeToString(pixels) + "\x1b\\"))
	tt.Frame()
	if !bytes.Equal(before, tt.Image().Pix) {
		t.Fatal("remote graphics changed the text-only terminal rendering")
	}
	if got := terminalBarrier(t, term, peer); len(got) != 0 {
		t.Fatalf("silent remote graphics request emitted input: %q", got)
	}
}

func TestTerminalNativeFileDropDoesNotSendLocalPaths(t *testing.T) {
	term, peer := testTerm(t)
	tt := ui.NewTester(func(c *ui.Context) { View(c, term).Fill() }, 400, 200)
	if tt.DropFiles(100, 100, "/private/LOCAL_FILE_CANARY") {
		t.Fatal("remote terminal accepted a local file drop")
	}
	if got := terminalBarrier(t, term, peer); len(got) != 0 {
		t.Fatalf("file drop emitted remote input: %q", got)
	}
}

func TestTerminalNativePinchChangesFontWithoutRemoteInput(t *testing.T) {
	for _, tc := range []struct {
		name      string
		scales    []float32
		direction int
	}{
		{"larger", []float32{2}, -1},
		{"smaller", []float32{0.5}, 1},
		{"reverse", []float32{2, 0.5}, 0},
		{"small-moves", []float32{1.05, 1.05, 1.05, 1.05}, -1},
		{"identity", []float32{1}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term, peer := testTerm(t)
			tt := ui.NewTester(func(c *ui.Context) { View(c, term).Fill() }, 800, 500)
			tt.ClickAt(100, 100)
			term.Feed([]byte("\x1b[?1049h\x1b[?1002h\x1b[?1006h"))
			tt.Frame()
			cols, rows := term.Size()
			for _, scale := range tc.scales {
				tt.Pinch(100, 100, scale)
			}
			tt.Frame()
			gotCols, gotRows := term.Size()
			switch tc.direction {
			case -1:
				if gotCols > cols || gotRows > rows || (gotCols == cols && gotRows == rows) {
					t.Fatalf("zoom in: %dx%d -> %dx%d", cols, rows, gotCols, gotRows)
				}
			case 1:
				if gotCols < cols || gotRows < rows || (gotCols == cols && gotRows == rows) {
					t.Fatalf("zoom out: %dx%d -> %dx%d", cols, rows, gotCols, gotRows)
				}
			default:
				if gotCols != cols || gotRows != rows {
					t.Fatalf("zoom restore: %dx%d -> %dx%d", cols, rows, gotCols, gotRows)
				}
			}
			if got := terminalBarrier(t, term, peer); len(got) != 0 {
				t.Fatalf("pinch sent remote input: %q", got)
			}
			term.Close()
			term.IOFinished()
			tt.Frame()
			tt.Pinch(100, 100, 2)
		})
	}
}
