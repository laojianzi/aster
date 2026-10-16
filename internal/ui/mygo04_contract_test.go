package uiworkbench

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// These fixtures qualify the dependency API; Aster's reviewed operations use
// inline confirmation controls, not AlertDialog. Keep that distinction explicit.
func TestNativeMyGo04EscapeDismissesWithoutChoosingButton(t *testing.T) {
	for _, buttons := range [][]string{{"Cancel", "Apply"}, {"取消", "应用"}, {"Delete"}} {
		t.Run(strings.Join(buttons, "-"), func(t *testing.T) {
			open := true
			var responses []int
			tt := ui.NewTester(func(c *ui.Context) {
				response := ui.AlertDialog(c.Key("mygo04-alert"), &open, "Review", "No write is allowed on Escape", buttons...)
				if response != -1 {
					responses = append(responses, response)
				}
			}, 800, 500)
			tt.Key(0, ui.KeyEscape)
			tt.Frame()
			if open || len(responses) != 1 || responses[0] != ui.AlertDismissed {
				t.Fatalf("Escape chose a button, failed to close, or repeated: open=%v responses=%v", open, responses)
			}
			for range 3 {
				tt.Frame()
			}
			if len(responses) != 1 {
				t.Fatalf("dismissal replayed on unrelated frames: %v", responses)
			}
		})
	}
}

func TestNativeMyGo04ToastExpiresAfterClosingDialog(t *testing.T) {
	for _, delayed := range []bool{false, true} {
		name := "same-frame"
		if delayed {
			name = "later-frame"
		}
		t.Run(name, func(t *testing.T) {
			open, publish, published, closed := true, false, 0, 0
			tt := ui.NewTester(func(c *ui.Context) {
				if ui.AlertDialog(c.Key("mygo04-toast-dialog"), &open, "Dependency contract", "Close this dialog", "Cancel", "Done") == 1 && !delayed {
					publish = true
				}
				if publish {
					publish = false
					published++
					c.AddToast(ui.Toast{ID: "mygo04-contract-toast", Title: "Contract complete", Timeout: 300 * time.Millisecond, OnClose: func() { closed++ }})
				}
			}, 800, 500)
			button, ok := tt.Find("Done")
			if !ok {
				t.Fatal("dialog button absent")
			}
			if err := tt.Click("Done"); err != nil {
				t.Fatal(err)
			}
			if open {
				t.Fatal("dialog did not close")
			}
			if delayed {
				tt.Frame()
				publish = true
				tt.Frame()
			}
			if published != 1 {
				t.Fatalf("toast not published once: %d", published)
			}
			if toast, visible := tt.Find("Contract complete"); visible && toast.Contains(button.X+button.W/2, button.Y+button.H/2) {
				t.Fatal("test pointer would rest on the toast")
			}
			// Only the public tester: do not move the pointer to hide the old bug.
			// Since MyGo 0.5.5, only Advance moves the tester's UI clock.
			// Keep the old deadline and close-once contract, not a real sleep.
			tt.Advance(299 * time.Millisecond)
			if closed != 0 {
				t.Fatal("toast expired before its configured deadline")
			}
			tt.Advance(1701 * time.Millisecond)
			if closed != 1 || tt.HasText("Contract complete") {
				t.Fatalf("toast inherited closed dialog hover/focus: closes=%d", closed)
			}
			tt.Frame()
			if closed != 1 {
				t.Fatal("toast close callback replayed")
			}
		})
	}
}

func TestNativeMyGo04SpinnerUsesExplicitTextColor(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Spinner(c.Key("mygo04-spinner")).Size(40, 40).TextColor(ui.RGB(0, 255, 0)).Label("Dependency spinner")
	}, 100, 100)
	r, ok := tt.Find("Dependency spinner")
	if !ok {
		t.Fatal("spinner absent")
	}
	image := tt.Image()
	green := 0
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			pixel := image.RGBAAt(x, y)
			if int(pixel.G) > int(pixel.R)+30 && int(pixel.G) > int(pixel.B)+30 {
				green++
			}
		}
	}
	if green < 5 {
		t.Fatalf("Spinner ignored explicit TextColor: %d green pixels", green)
	}
}

// New production usage of these breaking surfaces requires a new review.
// Generated adapter and fixtures are excluded; terminal hooks have separate tests.
func TestNativeMyGo04ProductionSurfaceInventory(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("source location unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(here), "../.."))
	restricted := map[string]bool{"AlertDialog": true, "NewWebView": true, "WebView": true, "CallerPage": true, "EmitPage": true, "Bounds": true, "ContentBounds": true}
	files := 0
	for _, directory := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() && path == filepath.Join(root, "internal", "nativeterm", "upstream") {
				return filepath.SkipDir
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			files++
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if ok && restricted[selector.Sel.Name] {
					t.Errorf("new API surface requires reviewed migration: %s (%s)", selector.Sel.Name, fset.Position(selector.Pos()))
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if files == 0 {
		t.Fatal("no production source audited")
	}
	t.Logf("audited %d production Go files; no AlertDialog/WebView/frame-bounds integration", files)
}
