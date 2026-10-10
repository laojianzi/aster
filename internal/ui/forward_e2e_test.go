//go:build e2e

package uiworkbench

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/testcluster"
)

func TestNativeLogsAndPortForwardLifecycle(t *testing.T) {
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
				t.Fatalf("UI timeout: %s %s %s", w.status, w.errText, w.forwardStatus)
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
	click("Connect")
	pump(func() bool { return containsRowUID(w.rows, string(f.Pod.UID)) })
	click(f.Pod.Name)
	pump(func() bool { return w.detail != nil })
	click("Logs")
	click("Follow logs")
	pump(func() bool { return strings.Contains(strings.Join(w.logRows, "\n"), "aster-log-ready") })
	click("Port forward")
	click("Start port forward")
	pump(func() bool { return w.forwardLocal != 0 || !w.forwardActive })
	if w.forwardLocal == 0 {
		t.Fatal(w.forwardStatus)
	}
	address := fmt.Sprintf("127.0.0.1:%d", w.forwardLocal)
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get("http://" + address + "/")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	response.Body.Close()
	if err != nil || string(data) != testcluster.Message {
		t.Fatalf("forwarded response %q %v", data, err)
	}
	saveNativeScreenshot(t, tt)
	click("Close detail")
	if w.forwardActive || len(w.logRows) != 0 || w.detail != nil {
		t.Fatal("detail closure retained resource sessions or logs")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if err != nil {
			break
		}
		conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("native close did not close the listener")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
