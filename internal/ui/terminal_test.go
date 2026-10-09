package uiworkbench

import (
	"context"
	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/nativeterm"
	"net"
	"os"
	"strings"
	"testing"
)

func terminalTestLibrary(t *testing.T) {
	t.Helper()
	p := os.Getenv("ASTER_TEST_LIBRARY")
	if p == "" {
		t.Fatal("verified terminal library required")
	}
	if err := nativeterm.LoadFile(p); err != nil {
		t.Fatal(err)
	}
}
func TestNativeTerminalMinimumWindowAndConfirmation(t *testing.T) {
	terminalTestLibrary(t)
	w := New()
	w.detail = modelObject("one", "pod")
	w.detailKind = catalog()[0]
	w.detailMode = "Terminal"
	w.containers = []string{"http"}
	w.terminalArgv = `["/bin/sh"]`
	client, peer := net.Pipe()
	term, err := nativeterm.New(client)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.terminalCancel = cancel
	w.terminal = term
	t.Cleanup(func() { cancel(); term.Close(); peer.Close(); term.IOFinished() })
	term.Feed([]byte("\x1b[32mASTER NATIVE TTY\x1b[0m\r\n"))
	tt := ui.NewTester(w.View, 1100, 700)
	for _, label := range []string{"Terminal container", "Terminal argv JSON", "Confirm Pod for terminal", "Open terminal", "Close terminal", "Remote terminal screen"} {
		box, ok := tt.Find(label)
		if !ok || box.X < 0 || box.Y < 0 || box.X+box.W > 1088 || box.Y+box.H > 700 || box.W <= 0 || box.H <= 0 {
			t.Fatalf("terminal control outside minimum window: %s %+v", label, box)
		}
	}
	if err := tt.Click("Confirm Pod for terminal"); err != nil {
		t.Fatal(err)
	}
	tt.Type("pod")
	if w.terminalConfirmation != "pod" {
		t.Fatal("missing confirmation")
	}
	if err := tt.Click("Terminal argv JSON"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(`["sh"]`)
	if w.terminalConfirmation != "" {
		t.Fatal("argv edit retained confirmation")
	}
	saveNativeScreenshot(t, tt)
	if err := tt.Click("YAML"); err != nil {
		t.Fatal(err)
	}
	if w.terminal != nil || ctx.Err() == nil {
		t.Fatal("hidden interactive terminal remained attached")
	}
}
func TestNativeTerminalCloseClearsScreenAndPreventsPendingReopen(t *testing.T) {
	terminalTestLibrary(t)
	w := New()
	w.detail = modelObject("one", "pod")
	w.detailKind = catalog()[0]
	w.detailMode = "Terminal"
	client, peer := net.Pipe()
	term, err := nativeterm.New(client)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	defer term.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.terminal = term
	w.terminalCancel = cancel
	w.terminalActive = true
	w.terminalJoining = true
	w.terminalArgv = "sensitive-command"
	w.terminalConfirmation = "pod"
	term.Feed([]byte("sensitive-output"))
	before := w.terminalEpoch
	w.clearDetail()
	term.IOFinished()
	if ctx.Err() == nil || w.terminal != nil || w.terminalActive || w.terminalEpoch <= before || w.terminalArgv != "" || w.terminalConfirmation != "" || term.Text() != "" {
		t.Fatal("terminal data/lifecycle survived detail close")
	}
	if !w.terminalJoining {
		t.Fatal("capacity was freed before the worker joined")
	}
	w.terminalActive = true
	w.stopTerminal()
	if !strings.Contains(w.terminalStatus, "not confirmed") {
		t.Fatal("stop falsely claimed process termination")
	}
}
