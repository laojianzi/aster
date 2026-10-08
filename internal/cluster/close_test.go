package cluster

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestEveryCloserJoinsWorkers(t *testing.T) {
	s := NewSession(context.Background(), "one")
	entered, release := make(chan struct{}), make(chan struct{})
	if err := s.Go(func(ctx context.Context) { <-ctx.Done(); close(entered); <-release }); err != nil { t.Fatal(err) }
	var closers sync.WaitGroup
	returned := make(chan struct{}, 8)
	for i := 0; i < 8; i++ { closers.Add(1); go func() { defer closers.Done(); s.Close(); returned <- struct{}{} }() }
	<-entered
	select { case <-returned: t.Fatal("Close returned before worker finished"); default: }
	close(release)
	closers.Wait()
	if len(returned) != 8 { t.Fatal("not all closers returned") }
}

func TestCancelledParentRejectsWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background()); cancel()
	s := NewSession(ctx, "cancelled")
	if err := s.Go(func(context.Context) { t.Error("worker started") }); !errors.Is(err, ErrClosed) { t.Fatal(err) }
	s.Close()
}
