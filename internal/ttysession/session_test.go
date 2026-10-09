package ttysession

import (
	"bytes"
	"context"
	"github.com/laojianzi/aster/internal/execsession"
	"io"
	"sync"
	"testing"
	"time"
)

func wait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("session did not join")
	}
}
func TestTTYOrderedRoundTripAndRemoteExit(t *testing.T) {
	message := []byte("中文\x1b[A\r\n")
	s, err := New(context.Background(), time.Second, func(ctx context.Context, in io.Reader, out io.Writer, next func() *Size) (execsession.Result, error) {
		data := make([]byte, len(message))
		_, e := io.ReadFull(in, data)
		if e != nil {
			return execsession.Result{}, e
		}
		_, e = out.Write(data)
		return execsession.Result{State: execsession.Failed, ExitKnown: true, ExitCode: 7}, e
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sent := make(chan struct{})
	go func() { defer close(sent); _, _ = s.Write(message) }()
	got, err := io.ReadAll(s)
	if err != nil || !bytes.Equal(got, message) {
		t.Fatal(got, err)
	}
	wait(t, sent)
	wait(t, s.Done())
	if result := s.Outcome().Result; !result.ExitKnown || result.ExitCode != 7 {
		t.Fatal(result)
	}
}
func TestTTYCloseUnblocksBothDirectionsAndJoins(t *testing.T) {
	s, err := New(context.Background(), time.Second, func(ctx context.Context, in io.Reader, out io.Writer, next func() *Size) (execsession.Result, error) {
		<-ctx.Done()
		return execsession.Result{State: execsession.Interrupted, ExitCode: -1}, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = s.Write([]byte("blocked")) }()
	go func() { defer wg.Done(); _, _ = s.Read(make([]byte, 64)) }()
	s.Close()
	joined := make(chan struct{})
	go func() { wg.Wait(); close(joined) }()
	wait(t, joined)
	wait(t, s.Done())
	if r := s.Outcome().Result; r.ExitKnown || r.State != execsession.Interrupted {
		t.Fatal(r)
	}
}
func TestTTYResizeCoalescesAndClamps(t *testing.T) {
	ready := make(chan struct{})
	sizes := make(chan Size, 1)
	s, err := New(context.Background(), time.Second, func(ctx context.Context, in io.Reader, out io.Writer, next func() *Size) (execsession.Result, error) {
		<-ready
		sizes <- *next()
		return execsession.Result{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 1; i < 1000; i++ {
		if err := s.Resize(i, i); err != nil {
			t.Fatal(err)
		}
	}
	close(ready)
	select {
	case v := <-sizes:
		if v != (Size{400, 200}) {
			t.Fatal(v)
		}
	case <-time.After(time.Second):
		t.Fatal("resize blocked")
	}
	wait(t, s.Done())
}
func TestTTYLeaseCancelsExecutorWithoutInventingExit(t *testing.T) {
	s, err := New(context.Background(), 20*time.Millisecond, func(ctx context.Context, in io.Reader, out io.Writer, next func() *Size) (execsession.Result, error) {
		<-ctx.Done()
		return execsession.Result{State: execsession.Interrupted, ExitCode: -1}, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	wait(t, s.Done())
	if o := s.Outcome(); o.Err != context.DeadlineExceeded || o.Result.ExitKnown {
		t.Fatal(o)
	}
	if _, err := New(context.Background(), MaxLease+time.Second, func(context.Context, io.Reader, io.Writer, func() *Size) (execsession.Result, error) {
		panic("must not run")
	}); err == nil {
		t.Fatal("unbounded lease accepted")
	}
}
