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

func TestNativePodCommandLifecycleAgainstRealCluster(t *testing.T) {
	f := testcluster.NewHTTPPod(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	_, current, err := kubeconfig.Contexts("")
	if err != nil {
		cancel()
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
				t.Fatalf("UI timeout: %s %s", w.errText, w.commandStatus)
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
	click("Command")
	set("Command argv JSON", `["/bin/sh","-c","printf 'native-command-out'; printf 'native-command-err' >&2; exit 7"]`)
	if w.commandConfirmation != "" {
		t.Fatal("command already confirmed")
	}
	set("Confirm Pod for command", f.Pod.Name)
	click("Run command")
	pump(func() bool { return !w.commandActive && strings.Contains(w.commandStatus, "exit code 7") })
	if !strings.Contains(w.commandOutput, "native-command-out") || !strings.Contains(w.commandOutput, "native-command-err") {
		t.Fatal("output streams missing")
	}
	if w.commandConfirmation != "" {
		t.Fatal("confirmation was not consumed")
	}
	saveNativeScreenshot(t, tt)
	set("Command argv JSON", `["/bin/sh","-c","echo native-command-started; sleep 30"]`)
	set("Confirm Pod for command", f.Pod.Name)
	click("Run command")
	pump(func() bool { return strings.Contains(w.commandOutput, "native-command-started") })
	click("Stop command")
	if w.commandActive || !strings.Contains(w.commandStatus, "not confirmed") {
		t.Fatal("stop pretended to terminate remote command")
	}
	click("Close detail")
	if w.commandOutput != "" || w.commandArgv != "" || w.detail != nil {
		t.Fatal("sensitive command data survived closing detail")
	}
}
