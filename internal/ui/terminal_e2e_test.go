//go:build e2e

package uiworkbench

import (
	"context"
	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/testcluster"
	"strings"
	"testing"
	"time"
)

func TestNativeTTYInputAndLifecycleAgainstRealCluster(t *testing.T) {
	terminalTestLibrary(t)
	f := testcluster.NewHTTPPod(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	_, current, err := kubeconfig.Contexts("")
	if err != nil {
		t.Fatal(err)
	}
	updates := make(chan func(), 128)
	w := New()
	w.currentContext = current
	w.contexts = []string{current}
	w.namespace = f.Pod.Namespace
	w.attach(ctx, func(fn func()) {
		select {
		case updates <- fn:
		case <-ctx.Done():
		}
	})
	t.Cleanup(func() { cancel(); w.Close() })
	tt := ui.NewTester(w.View, 1440, 1000)
	pump := func(predicate func() bool) {
		t.Helper()
		for {
			select {
			case fn := <-updates:
				fn()
			default:
			}
			tt.Frame()
			if predicate() {
				return
			}
			select {
			case <-ctx.Done():
				saveNativeScreenshot(t, tt)
				t.Fatalf("terminal UI timeout: %s / %s", w.errText, w.terminalStatus)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			saveNativeScreenshot(t, tt)
			t.Fatal(err)
		}
	}
	set := func(label, value string) { t.Helper(); click(label); tt.Key(ui.Cmd, ui.KeyA); tt.Type(value) }
	click("Connect")
	pump(func() bool { return containsRowUID(w.rows, string(f.Pod.UID)) })
	click(f.Pod.Name)
	pump(func() bool { return w.detail != nil })
	click("Terminal")
	set("Terminal argv JSON", `["/bin/sh"]`)
	set("Confirm Pod for terminal", f.Pod.Name)
	click("Open terminal")
	pump(func() bool { return w.terminal != nil })
	if w.terminalConfirmation != "" {
		t.Fatal("confirmation was not consumed")
	}
	click("Remote terminal screen")
	// The expected marker is not contiguous in the command, so TTY echo cannot
	// satisfy this assertion without the remote program actually evaluating it.
	tt.Type("printf 'ASTER_%s\\n' tty-native-ok\r")
	pump(func() bool { return strings.Contains(w.terminal.Text(), "ASTER_tty-native-ok") })
	tt.Type("test -t 0 && printf 'NATIVE_%s\\n' is-tty\r")
	pump(func() bool { return strings.Contains(w.terminal.Text(), "NATIVE_is-tty") })
	tt.SetSize(1100, 700)
	if r, ok := tt.Find("Remote terminal screen"); !ok || r.H < 50 || r.X+r.W > 1088 || r.Y+r.H > 700 {
		t.Fatal("terminal did not resize inside workbench", r)
	}
	saveNativeScreenshot(t, tt)
	tt.Type("exit 7\r")
	pump(func() bool { return !w.terminalActive && strings.Contains(w.terminalStatus, "exit code 7") })
	// Screen remains available after the real exit; close frees capacity and IO.
	click("Close terminal")
	pump(func() bool { return !w.terminalJoining })
	if w.terminal != nil {
		t.Fatal("closed screen retained")
	}
	set("Confirm Pod for terminal", f.Pod.Name)
	click("Open terminal")
	pump(func() bool { return w.terminal != nil })
	click("YAML")
	pump(func() bool { return !w.terminalJoining })
	if w.terminalActive || w.terminal != nil || !strings.Contains(w.terminalStatus, "not confirmed") {
		t.Fatal("hidden terminal did not disconnect")
	}
	// Cancel while an asynchronous preflight/publication is outstanding. Old
	// callbacks must not resurrect the terminal or keep a workspace joining.
	click("Terminal")
	set("Confirm Pod for terminal", f.Pod.Name)
	click("Open terminal")
	click("Close detail")
	pump(func() bool { return !w.terminalJoining })
	if w.detail != nil || w.terminal != nil || w.terminalArgv != "" {
		t.Fatal("late terminal publication survived detail closure")
	}
}
