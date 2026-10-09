// Package ttysession provides an ordered, cancellable remote terminal transport.
// It has no process creation, local shell, reconnection or replay behavior.
package ttysession

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/laojianzi/aster/internal/execsession"
)

const DefaultLease = 15 * time.Minute
const MaxLease = 30 * time.Minute

type Size struct{ Cols, Rows uint16 }
type Outcome struct {
	Result execsession.Result
	Err    error
}
type Run func(context.Context, io.Reader, io.Writer, func() *Size) (execsession.Result, error)

// Session is safe for a reader, a writer, Resize and Close concurrently. Pipes
// apply backpressure without dropping bytes. Close must not be interpreted as
// remote process termination. Done joins the actual remote executor, not merely
// a UI cancellation request.
type Session struct {
	ctx       context.Context
	cancel    context.CancelFunc
	inR       *io.PipeReader
	inW       *io.PipeWriter
	outR      *io.PipeReader
	outW      *io.PipeWriter
	sizes     chan Size
	resizeMu  sync.Mutex
	closeOnce sync.Once
	done      chan struct{}
	outcome   Outcome // read only after done closes
}

func New(parent context.Context, lease time.Duration, run Run) (*Session, error) {
	if parent == nil || run == nil {
		return nil, errors.New("terminal requires a context and executor")
	}
	if lease < 0 || lease > MaxLease {
		return nil, errors.New("terminal lease must be within thirty minutes")
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	if lease == 0 {
		lease = DefaultLease
	}
	ctx, cancel := context.WithTimeout(parent, lease)
	r, w := io.Pipe()
	or, ow := io.Pipe()
	s := &Session{ctx: ctx, cancel: cancel, inR: r, inW: w, outR: or, outW: ow, sizes: make(chan Size, 1), done: make(chan struct{})}
	s.sizes <- Size{80, 24}
	stop := context.AfterFunc(ctx, func() { s.closePipes(ctx.Err()) })
	go func() {
		result, err := run(ctx, r, ow, s.nextSize)
		s.outcome = Outcome{result, err}
		// Preserve all successfully written output before EOF; real status is saved
		// before a terminal reader can observe the end and close its connection.
		_ = ow.Close()
		_ = r.Close()
		close(s.done)
		stop()
		cancel()
	}()
	return s, nil
}
func (s *Session) Read(p []byte) (int, error)  { return s.outR.Read(p) }
func (s *Session) Write(p []byte) (int, error) { return s.inW.Write(p) }
func (s *Session) closePipes(err error) {
	s.closeOnce.Do(func() {
		_ = s.inR.CloseWithError(err)
		_ = s.inW.CloseWithError(err)
		_ = s.outR.CloseWithError(err)
		_ = s.outW.CloseWithError(err)
	})
}
func (s *Session) Close() error          { s.cancel(); s.closePipes(context.Canceled); return nil }
func (s *Session) Done() <-chan struct{} { return s.done }
func (s *Session) Outcome() Outcome      { <-s.done; return s.outcome }
func (s *Session) Resize(cols, rows int) error {
	s.resizeMu.Lock()
	defer s.resizeMu.Unlock()
	if err := s.ctx.Err(); err != nil {
		return err
	}
	value := Size{uint16(min(max(cols, 1), 400)), uint16(min(max(rows, 1), 200))}
	// Serialized producers and a single consumer make this channel an explicitly
	// coalesced latest-size slot. Closing it is unnecessary and would race Resize.
	select {
	case <-s.sizes:
	default:
	}
	select {
	case s.sizes <- value:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}
func (s *Session) nextSize() *Size {
	select {
	case <-s.ctx.Done():
		return nil
	case value := <-s.sizes:
		return &value
	}
}
