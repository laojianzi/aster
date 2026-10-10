package uiworkbench

import (
	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kube"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeEditorUndoCannotCrossResourceIdentity(t *testing.T) {
	w := New()
	defer w.Close()
	w.detail, w.detailKind, w.detailMode = modelObject("uid-a", "pod-a"), catalog()[0], "Edit"
	w.editor = "original-resource-a"
	tt := ui.NewTester(w.View, 1440, 1000)
	if err := tt.Click("Manifest editor"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("edited-resource-a")
	// Publish the next resource before a frame: the old widget must not carry
	// its selection, composition, or history into a new detail identity.
	w.clearDetail()
	w.detail, w.detailKind, w.detailMode = modelObject("uid-b", "pod-b"), catalog()[0], "Edit"
	w.editor = "original-resource-b"
	tt.Frame()
	if err := tt.Click("Manifest editor"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	if w.editor != "original-resource-b" {
		t.Fatalf("Undo crossed resource identity: %q", w.editor)
	}
}

func TestNativeCommandUndoCannotCrossResourceIdentity(t *testing.T) {
	w := New()
	defer w.Close()
	w.detail, w.detailKind, w.detailMode = modelObject("uid-a", "pod-a"), catalog()[0], "Command"
	w.containers = []string{"app"}
	w.commandArgv = `["id"]`
	tt := ui.NewTester(w.View, 1440, 1000)
	if err := tt.Click("Command argv JSON"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(`["echo","private-argument-a"]`)
	w.clearDetail()
	w.detail, w.detailKind, w.detailMode = modelObject("uid-b", "pod-b"), catalog()[0], "Command"
	w.commandArgv = `["id"]`
	tt.Frame()
	if err := tt.Click("Command argv JSON"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	if w.commandArgv != `["id"]` {
		t.Fatalf("Undo retained another Pod's argv: %q", w.commandArgv)
	}
}

func TestNativeMigrationTextFieldsDoNotShareHistory(t *testing.T) {
	w := New()
	defer w.Close()
	w.detail, w.detailKind, w.detailMode = modelObject("uid", "pod"), catalog()[0], "Edit"
	w.editor = "original-manifest"
	tt := ui.NewTester(w.View, 1440, 1000)
	if err := tt.Click("Manifest editor"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("edited-manifest")
	if err := tt.Click("Command"); err != nil {
		t.Fatal(err)
	}
	w.commandArgv = `["id"]`
	tt.Frame()
	if err := tt.Click("Command argv JSON"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if w.commandArgv != `["id"]` {
		t.Fatalf("manifest history entered command: %q", w.commandArgv)
	}
	if err := tt.Click("Terminal"); err != nil {
		t.Fatal(err)
	}
	w.terminalArgv = `["/bin/sh"]`
	tt.Frame()
	if err := tt.Click("Terminal argv JSON"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if w.terminalArgv != `["/bin/sh"]` {
		t.Fatalf("unrelated history entered terminal: %q", w.terminalArgv)
	}
}

func TestNativeMigrationReadOnlyRejectsTypingAndUndo(t *testing.T) {
	w := New()
	defer w.Close()
	w.detail, w.detailKind, w.detailMode = modelObject("uid", "pod"), catalog()[0], "Edit"
	w.editor = "original-manifest"
	w.detailText = "read-only-live-document"
	tt := ui.NewTester(w.View, 1440, 1000)
	if err := tt.Click("Manifest editor"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("edited-manifest")
	if err := tt.Click("YAML"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Resource document"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("must-not-change")
	tt.SetClipboard("must-not-paste")
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Key(ui.Cmd, ui.KeyZ)
	if w.detailText != "read-only-live-document" || w.editor != "edited-manifest" {
		t.Fatal("read-only input changed document/model")
	}
}

func TestNativeMigrationDisabledCommandControlsIgnoreInput(t *testing.T) {
	w := New()
	defer w.Close()
	w.detail, w.detailKind, w.detailMode = modelObject("uid", "pod"), catalog()[0], "Command"
	w.containers = []string{"app", "other"}
	w.commandContainer = "app"
	w.commandArgv = `["id"]`
	tt := ui.NewTester(w.View, 1440, 1000)
	if err := tt.Click("Command argv JSON"); err != nil {
		t.Fatal(err)
	}
	w.commandActive = true
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("must-not-change")
	if w.commandArgv != `["id"]` {
		t.Fatal("active command accepted readonly input")
	}
	if err := tt.Click("Confirm Pod for command"); err != nil {
		t.Fatal(err)
	}
	tt.Type("pod")
	if w.commandConfirmation != "" {
		t.Fatal("disabled confirmation accepted input")
	}
}

func TestNativeMigrationResourceSwitchDropsComposition(t *testing.T) {
	w := New()
	defer w.Close()
	w.detail, w.detailKind, w.detailMode = modelObject("uid-a", "same-name"), catalog()[0], "Edit"
	w.editor = "resource-a"
	tt := ui.NewTester(w.View, 1440, 1000)
	if err := tt.Click("Manifest editor"); err != nil {
		t.Fatal(err)
	}
	tt.Compose("未提交输入", 3)
	w.clearDetail()
	w.detail, w.detailKind, w.detailMode = modelObject("uid-b", "same-name"), catalog()[0], "Edit"
	w.editor = "resource-b"
	tt.Frame()
	if tt.Focused("Manifest editor") {
		t.Fatal("focus crossed the replaced resource UID")
	}
	tt.Key(0, ui.KeyEnter)
	if w.editor != "resource-b" {
		t.Fatal("old composition committed to new resource")
	}
}

func TestNativeMigrationRowReorderDoesNotRetargetClick(t *testing.T) {
	w := New()
	defer w.Close()
	w.backend = &kube.Backend{}
	w.rows = []resourceRow{{UID: "a", Name: "pod-a", Namespace: "team"}, {UID: "b", Name: "pod-b", Namespace: "team"}}
	tt := ui.NewTester(w.View, 1440, 1000)
	r, ok := tt.Find("pod-a")
	if !ok {
		t.Fatal("row not rendered")
	}
	x, y := r.X+r.W/2, r.Y+r.H/2
	before := w.detailEpoch
	tt.Press(x, y)
	w.rows[0], w.rows[1] = w.rows[1], w.rows[0]
	tt.Frame()
	tt.Release(x, y)
	if w.detailEpoch != before {
		t.Fatal("release at old position opened a different resource")
	}
	if err := tt.Click("pod-a"); err != nil {
		t.Fatal(err)
	}
	if w.detailEpoch != before+1 {
		t.Fatal("explicit click after reorder not routed once")
	}
}

func TestNativeMigrationActionsRunAfterConstructionOnce(t *testing.T) {
	w := New()
	defer w.Close()
	building, calls := false, 0
	w.newWorkspace = func() error {
		if building {
			t.Error("structural action ran during view construction")
		}
		calls++
		return nil
	}
	tt := ui.NewTester(func(c *ui.Context) { building = true; defer func() { building = false }(); w.View(c) }, 1440, 1000)
	if err := tt.Click("New workspace"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		tt.Frame()
	}
	if calls != 1 {
		t.Fatalf("action ran %d times", calls)
	}
}

func TestNativeMigrationTransientMessagePreservesEditorHistory(t *testing.T) {
	w := New()
	defer w.Close()
	w.detail, w.detailKind, w.detailMode = modelObject("uid", "pod"), catalog()[0], "Edit"
	w.editor = "before"
	tt := ui.NewTester(w.View, 1440, 1000)
	if err := tt.Click("Manifest editor"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("after")
	w.detailMessage = "A status row appeared above the editor"
	tt.Frame()
	if !tt.Focused("Manifest editor") {
		t.Fatal("status insertion lost stable field focus")
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	if w.editor != "before" {
		t.Fatalf("status insertion broke history: %q", w.editor)
	}
	saveNativeScreenshot(t, tt)
}

func TestNativeMigrationConstructorsAreKeyed(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("missing source location")
	}
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(here), "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		fs := token.NewFileSet()
		f, err := parser.ParseFile(fs, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "ui" {
				return true
			}
			switch sel.Sel.Name {
			case "TextInput", "TextArea", "Select", "Checkbox", "Table", "List":
			default:
				return true
			}
			checked++
			key, ok := call.Args[0].(*ast.CallExpr)
			if !ok {
				t.Errorf("%s: stateful %s requires a construction key", fs.Position(call.Pos()), sel.Sel.Name)
				return true
			}
			method, ok := key.Fun.(*ast.SelectorExpr)
			if !ok || method.Sel.Name != "Key" {
				t.Errorf("%s: not a Context.Key binding", fs.Position(call.Pos()))
			}
			return true
		})
	}
	if checked < 20 {
		t.Fatalf("incomplete source scan: %d constructors", checked)
	}
}
