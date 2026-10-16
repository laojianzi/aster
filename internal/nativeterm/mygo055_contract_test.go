package nativeterm

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	upstream "github.com/laojianzi/aster/internal/nativeterm/upstream"
)

func TestTerminalNativeMyGo055RuntimeCopyAndImagePasteStayDenied(t *testing.T) {
	for _, mode := range []string{"", "\x1b[?2004h", "\x1b[?5522h", "\x1b[?2004h\x1b[?5522h"} {
		t.Run(fmt.Sprintf("mode-%x", mode), func(t *testing.T) {
			term, peer := testTerm(t)
			tt := ui.NewTester(func(c *ui.Context) { View(c, term).Fill() }, 800, 500)
			term.Feed([]byte("REMOTE_CANARY\r\n" + mode))
			tt.Frame()
			if err := tt.Click("Remote terminal screen"); err != nil {
				t.Fatal(err)
			}
			tt.SetClipboard("HOST_CANARY")
			// The newly added upstream setter must not bypass Aster's policy,
			// even through the internal adapter rather than its public wrapper.
			term.inner.SetCopyOnSelect(upstream.CopyOnSelectClipboard)
			tt.Command("selectAll")
			if tt.Clipboard() != "HOST_CANARY" {
				t.Fatal("selection silently wrote the host clipboard")
			}
			tt.Command("copy")
			if !strings.Contains(tt.Clipboard(), "REMOTE_CANARY") {
				t.Fatal("explicit user copy stopped working")
			}
			reads := 0
			data := transfer.New(transfer.NewItem(
				transfer.Lazy(transfer.PNG, func() ([]byte, error) { reads++; return []byte("IMAGE_CANARY"), nil }),
				transfer.Lazy(transfer.FileList, func() ([]byte, error) { reads++; return []byte("file:///PRIVATE_CANARY"), nil }),
			))
			tt.SetClipboardData(data)
			reads = 0
			tt.Command("paste")
			if got := terminalBarrier(t, term, peer); len(got) != 0 || reads != 0 {
				t.Fatalf("non-text paste emitted bytes or read providers: %q reads=%d", got, reads)
			}
			// A later protocol read is not authorized by the refused paste.
			term.Feed([]byte("\x1b]52;c;?\a\x1b]5522;type=read;\x1b\\"))
			tt.Frame()
			got := terminalBarrier(t, term, peer)
			if reads != 0 || strings.Contains(string(got), "CANARY") || term.InputError() != nil {
				t.Fatal("refused image paste retained an offer or expanded clipboard access")
			}
		})
	}
}
