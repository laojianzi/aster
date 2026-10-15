package nativeterm

import (
	"bufio"
	"bytes"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"runtime"
	"testing"
	"time"
)

func terminalBarrier(t *testing.T, term *Terminal, peer interface {
	SetReadDeadline(time.Time) error
	Read([]byte) (int, error)
}) []byte {
	t.Helper()
	term.Send([]byte{0})
	if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	b, err := bufio.NewReaderSize(peer, 32<<10).ReadSlice(0)
	if err != nil {
		t.Fatal(err)
	}
	return b[:len(b)-1]
}

func TestTerminalNativeMyGo05ClipboardReadsNeverEvaluateProviders(t *testing.T) {
	term, peer := testTerm(t)
	reads := 0
	private := transfer.New(transfer.NewItem(
		transfer.Lazy(transfer.Text, func() ([]byte, error) { reads++; return []byte("PRIVATE_TEXT"), nil }),
		transfer.Lazy(transfer.PNG, func() ([]byte, error) { reads++; return []byte("PRIVATE_IMAGE"), nil }),
		transfer.Lazy(transfer.FileList, func() ([]byte, error) { reads++; return []byte("file:///PRIVATE_FILE"), nil }),
	))
	tt := ui.NewTester(func(c *ui.Context) { View(c, term).Fill() }, 800, 500)
	tt.SetClipboardData(private)
	// The public test clipboard primes its text during setup. Count only reads
	// caused by the remote requests below; its retained snapshot is still lazy.
	reads = 0
	// Real native parser callbacks, not a mock permission check. The library may
	// send an empty/denied protocol reply; it must never read the OS clipboard.
	for _, request := range []string{"\x1b]52;c;?\a", "\x1b]5522;type=read;dGV4dC9wbGFpbg==\x1b\\", "\x1b]5522;type=read;\x1b\\"} {
		term.Feed([]byte(request))
		tt.Frame()
		b := terminalBarrier(t, term, peer)
		if reads != 0 || bytes.Contains(b, []byte("PRIVATE")) {
			t.Fatal("remote clipboard request accessed local data")
		}
	}
}

func TestTerminalNativeMyGo05MultiformatPasteIsBoundedTextOnly(t *testing.T) {
	for _, withText := range []bool{false, true} {
		name := "no-text"
		if withText {
			name = "text"
		}
		t.Run(name, func(t *testing.T) {
			term, peer := testTerm(t)
			nontext := 0
			reps := []transfer.Representation{
				transfer.Lazy(transfer.PNG, func() ([]byte, error) { nontext++; return []byte("IMAGE"), nil }),
				transfer.Lazy(transfer.FileList, func() ([]byte, error) { nontext++; return []byte("file:///SECRET"), nil }),
			}
			if withText {
				reps = append(reps, transfer.Bytes(transfer.Text, []byte("explicit text")))
			}
			tt := ui.NewTester(func(c *ui.Context) { View(c, term).Fill() }, 800, 500)
			if err := tt.Click("Remote terminal screen"); err != nil {
				t.Fatal(err)
			}
			tt.SetClipboardData(transfer.New(transfer.NewItem(reps...)))
			term.Feed([]byte("\x1b[?5522h\x1b[?2004h"))
			tt.Frame()
			mods := ui.Ctrl | ui.Shift
			if runtime.GOOS == "darwin" {
				mods = ui.Super
			}
			tt.Key(mods, ui.KeyV)
			got := terminalBarrier(t, term, peer)
			want := ""
			if withText {
				want = "\x1b[200~explicit text\x1b[201~"
			}
			if string(got) != want || nontext != 0 {
				t.Fatalf("paste granted non-text access or altered framing: %q reads=%d", got, nontext)
			}
			if term.InputError() != nil {
				t.Fatal(term.InputError())
			}
		})
	}
}

func TestTerminalNativeMyGo05ControlCVRemainRemote(t *testing.T) {
	term, peer := testTerm(t)
	tt := ui.NewTester(func(c *ui.Context) { View(c, term).Fill() }, 800, 500)
	if err := tt.Click("Remote terminal screen"); err != nil {
		t.Fatal(err)
	}
	tt.SetClipboard("DO_NOT_PASTE")
	tt.Key(ui.Ctrl, ui.KeyC)
	tt.Key(ui.Ctrl, ui.KeyV)
	got := terminalBarrier(t, term, peer)
	if string(got) != "\x03\x16" || tt.Clipboard() != "DO_NOT_PASTE" {
		t.Fatalf("remote Ctrl+C/V intercepted: %q", got)
	}
}
