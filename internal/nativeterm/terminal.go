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
type Terminal struct{ inner *upstream.Terminal }

var ErrInputLimit = upstream.ErrInputLimit
var Load = upstream.Load
var LoadFile = upstream.LoadFile
var ValidateLibrary = upstream.ValidateLibrary

func New(conn io.ReadWriteCloser) (*Terminal, error) {
	inner, err := upstream.New(upstream.Options{Conn: conn, NoBlink: true, Scrollback: 2 << 20})
	if err != nil {
		return nil, err
	}
	return &Terminal{inner}, nil
}
func View(c *ui.Context, t *Terminal) ui.Element {
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
