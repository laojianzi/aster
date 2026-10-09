package nativeterm

import (
	"errors"
	"github.com/egoist/mygo/ui"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadTest(t *testing.T) {
	t.Helper()
	p := os.Getenv("ASTER_TEST_LIBRARY")
	if p == "" {
		t.Fatal("ASTER_TEST_LIBRARY is required; run verified terminal preparation")
	}
	if err := LoadFile(p); err != nil {
		t.Fatal(err)
	}
}
func testTerm(t *testing.T) (*Terminal, net.Conn) {
	t.Helper()
	loadTest(t)
	client, server := net.Pipe()
	term, err := New(client)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { term.Close(); server.Close(); term.IOFinished() })
	return term, server
}
func until(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for !fn() {
		select {
		case <-deadline:
			t.Fatal("terminal condition timeout")
		case <-time.After(time.Millisecond):
		}
	}
}
func TestTerminalLibraryRejectsTamperedCodeAndImplicitLocalShell(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("implicit local shell accepted")
	}
	p := filepath.Join(t.TempDir(), "library")
	if err := os.WriteFile(p, []byte("not executable code"), 0600); err != nil {
		t.Fatal(err)
	}
	if ValidateLibrary(p) == nil || ValidateLibrary(filepath.Dir(p)) == nil || ValidateLibrary(p+"-missing") == nil {
		t.Fatal("unverified library accepted")
	}
	loadTest(t)
}
func TestTerminalInputBudgetClosesRatherThanSilentlyDropping(t *testing.T) {
	term, _ := testTerm(t) // The peer deliberately does not read.
	for i := 0; i < 32; i++ {
		term.Send([]byte(strings.Repeat("x", 8192)))
	}
	until(t, func() bool { return errors.Is(term.InputError(), ErrInputLimit) })
	select {
	case <-term.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("overflow did not disconnect")
	}
	term.IOFinished()
}
func TestTerminalHugePasteRejectedBeforeEncoding(t *testing.T) {
	term, _ := testTerm(t)
	term.Paste(strings.Repeat("x", (16<<10)+1))
	until(t, func() bool { return errors.Is(term.InputError(), ErrInputLimit) })
}
func TestTerminalNativeUnicodeInputAndOSC52CannotWriteClipboard(t *testing.T) {
	term, peer := testTerm(t)
	var clipboard string
	tt := ui.NewTester(func(c *ui.Context) {
		if clipboard == "" {
			c.WriteClipboard("local-canary")
		}
		View(c, term).Fill()
		clipboard = c.ReadClipboard()
	}, 800, 500)
	term.Feed([]byte("\x1b[31mremote-red\x1b[0m\r\n\x1b]52;c;c3RvbGVu\a"))
	tt.Frame()
	if clipboard != "local-canary" || !strings.Contains(term.Text(), "remote-red") {
		t.Fatal("clipboard changed or native text not rendered", clipboard)
	}
	received := make(chan []byte, 1)
	go func() {
		b := make([]byte, len("你好"))
		_, e := io.ReadFull(peer, b)
		if e != nil {
			received <- nil
		} else {
			received <- b
		}
	}()
	if err := tt.Click("Remote terminal screen"); err != nil {
		t.Fatal(err)
	}
	tt.Type("你好")
	select {
	case b := <-received:
		if string(b) != "你好" {
			t.Fatal(string(b))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("native input did not reach remote transport")
	}
	term.Close()
	tt.Frame() // Draw after native resources are freed must remain safe.
}
func TestTerminalResizeIsBounded(t *testing.T) {
	term, _ := testTerm(t)
	term.Resize(1000000, 1000000)
	c, r := term.Size()
	if c != 400 || r != 200 {
		t.Fatal(c, r)
	}
}
