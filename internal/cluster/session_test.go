package cluster

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionCloseCancelsAndWaits(t *testing.T) {
	s := NewSession(context.Background(), "test")
	var stopped atomic.Bool
	if err := s.Go(func(ctx context.Context) {
		<-ctx.Done()
		stopped.Store(true)
	}); err != nil {
		t.Fatal(err)
	}

	s.Close()
	if !stopped.Load() {
		t.Fatal("worker was not stopped before Close returned")
	}
	if err := s.Go(func(context.Context) {}); err != ErrClosed {
		t.Fatalf("Go after close = %v, want ErrClosed", err)
	}
}

func TestSessionCloseIdempotent(t *testing.T) {
	s := NewSession(context.Background(), "test")
	done := make(chan struct{})
	go func() {
		s.Close()
		s.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close deadlocked")
	}
}

func TestSessionConcurrentGoAndClose(t *testing.T) {
	s := NewSession(context.Background(), "test")
	var callers sync.WaitGroup
	for range 100 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			_ = s.Go(func(ctx context.Context) {
				select {
				case <-ctx.Done():
				case <-time.After(time.Millisecond):
				}
			})
		}()
	}
	s.Close()
	callers.Wait()
	if err := s.Go(func(context.Context) {}); err != ErrClosed {
		t.Fatalf("Go after concurrent close = %v, want ErrClosed", err)
	}
}

func TestSessionRejectsNilWorker(t *testing.T) {
	s := NewSession(context.Background(), "test")
	defer s.Close()
	if err := s.Go(nil); err == nil {
		t.Fatal("expected nil worker error")
	}
}
