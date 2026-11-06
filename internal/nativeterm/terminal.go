// Package nativeterm owns Aster's pinned and explicitly hardened MyGo native
// terminal adaptation. Run scripts/prepare_terminal.py before Go build/test.
package nativeterm

import (
	"github.com/egoist/mygo/ui"
	upstream "github.com/laojianzi/aster/internal/nativeterm/upstream"
	"io"
)

// Terminal deliberately does not expose the emulator's local ExitCode method:
// a remote command's exit status is available only from the Kubernetes session.
type Terminal struct {
	inner     *upstream.Terminal
	textScale float32
}

var ErrInputLimit = upstream.ErrInputLimit
var Load = upstream.Load
var LoadFile = upstream.LoadFile
var ValidateLibrary = upstream.ValidateLibrary

func New(conn io.ReadWriteCloser) (*Terminal, error) {
	light := &upstream.Theme{
		Foreground: ui.Hex("#1c2024"), Background: ui.Hex("#fcfcfd"),
		Selection: ui.Hex("#d2deff"), SelectionText: ui.Hex("#1c2024"),
		Search: ui.Hex("#fff7c2"), SearchText: ui.Hex("#4f3422"),
		SearchSelected: ui.Hex("#ffee9c"), SearchSelectedText: ui.Hex("#4f3422"),
		Palette: [16]ui.Color{
			ui.Hex("#1c2024"), ui.Hex("#ce2c31"), ui.Hex("#208368"), ui.Hex("#ab6400"),
			ui.Hex("#3a5bc7"), ui.Hex("#8145b5"), ui.Hex("#107d98"), ui.Hex("#cdced6"),
			ui.Hex("#60646c"), ui.Hex("#641723"), ui.Hex("#1d3b31"), ui.Hex("#4f3422"),
			ui.Hex("#1f2d5c"), ui.Hex("#402060"), ui.Hex("#0d3c48"), ui.Hex("#fcfcfd"),
		},
	}
	dark := &upstream.Theme{
		Foreground: ui.Hex("#edeef0"), Background: ui.Hex("#111113"),
		Selection: ui.Hex("#253974"), SelectionText: ui.Hex("#edeef0"),
		Search: ui.Hex("#4d3000"), SearchText: ui.Hex("#ffe7b3"),
		SearchSelected: ui.Hex("#5c3d05"), SearchSelectedText: ui.Hex("#ffe7b3"),
		Palette: [16]ui.Color{
			ui.Hex("#111113"), ui.Hex("#ff9592"), ui.Hex("#1fd8a4"), ui.Hex("#ffca16"),
			ui.Hex("#9eb1ff"), ui.Hex("#d19dff"), ui.Hex("#4ccce6"), ui.Hex("#b0b4ba"),
			ui.Hex("#696e77"), ui.Hex("#ffd1d9"), ui.Hex("#adf0d4"), ui.Hex("#ffe7b3"),
			ui.Hex("#d6e1ff"), ui.Hex("#ecd9fa"), ui.Hex("#b6ecf7"), ui.Hex("#edeef0"),
		},
	}
	inner, err := upstream.New(upstream.Options{Conn: conn, NoBlink: true, Scrollback: 2 << 20, Theme: light, DarkTheme: dark})
	if err != nil {
		return nil, err
	}
	return &Terminal{inner: inner}, nil
}
func View(c *ui.Context, t *Terminal) ui.Element {
	if scale := c.Preferences().TextScale; t.textScale != scale {
		t.inner.SetFont(upstream.Font{Size: 13 * scale})
		t.textScale = scale
	}
	return upstream.View(c, t.inner).Label("Remote terminal screen")
}
func (t *Terminal) Close() error          { return t.inner.Close() }
func (t *Terminal) IOFinished()           { t.inner.IOFinished() }
func (t *Terminal) Done() <-chan struct{} { return t.inner.Done() }
func (t *Terminal) InputError() error     { return t.inner.InputError() }
func (t *Terminal) Send(p []byte)         { t.inner.Send(p) }
func (t *Terminal) Paste(text string)     { t.inner.Paste(text) }
func (t *Terminal) Feed(p []byte)         { t.inner.Feed(p) }
func (t *Terminal) Text() string          { return t.inner.Text() }
func (t *Terminal) Resize(cols, rows int) { t.inner.Resize(cols, rows) }
func (t *Terminal) Size() (int, int)      { return t.inner.Size() }
