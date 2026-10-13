package nativeterm

import (
	"bufio"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The 0.4.8 terminal splits wheel routing into helpers and adds an owner hook.
// Aster supplies no hook: normal wheel input stays local, mouse reporting wins
// over alternate-screen arrows, and disabling alternate scroll sends nothing.
func TestTerminalNativeMyGo048WheelRoutingAndClose(t *testing.T) {
	mouse := regexp.MustCompile(`^(\x1b\[<64;[0-9]+;[0-9]+M){3}$`)
	for _, tc := range []struct {
		name, modes, want string
		mouse             bool
	}{
		{name: "local-scrollback"},
		{name: "mouse", modes: "\x1b[?1002h\x1b[?1006h", mouse: true},
		{name: "alternate-arrows", modes: "\x1b[?1049h", want: strings.Repeat("\x1b[A", 3)},
		{name: "mouse-before-arrows", modes: "\x1b[?1049h\x1b[?1002h\x1b[?1006h", mouse: true},
		{name: "alternate-scroll-disabled", modes: "\x1b[?1049h\x1b[?1007l"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term, peer := testTerm(t)
			tt := ui.NewTester(func(c *ui.Context) { View(c, term).Fill() }, 800, 500)
			term.Feed([]byte(strings.Repeat("history\r\n", 100) + tc.modes))
			tt.Frame()
			tt.Scroll(100, 100, 0, -40)
			// A queued marker is an ordering barrier, not a timed assertion that
			// no input happened. The bounded reader observes all preceding bytes.
			term.Send([]byte{0})
			if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			reader := bufio.NewReaderSize(peer, 256)
			got, err := reader.ReadSlice(0)
			if err != nil {
				t.Fatal("wheel input did not reach the bounded ordering barrier:", err)
			}
			text := string(got[:len(got)-1])
			if (tc.mouse && !mouse.MatchString(text)) || (!tc.mouse && text != tc.want) {
				t.Fatalf("unexpected native wheel routing: %q", text)
			}
			if term.InputError() != nil || len(tt.OpenedURLs()) != 0 {
				t.Fatal("wheel input overflowed or opened a local application")
			}
			term.Close()
			term.IOFinished()
			tt.Frame()
			tt.Scroll(100, 100, 0, -40)
		})
	}
}
