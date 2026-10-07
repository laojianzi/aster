package cluster

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

var ErrClosed = errors.New("cluster session closed")

type Session struct {
	id     string
	ctx    context.Context
	cancel context.CancelFunc
	closed atomic.Bool
	wg     sync.WaitGroup
}

func NewSession(parent context.Context, id string) *Session {
	ctx, cancel := context.WithCancel(parent)
	return &Session{id: id, ctx: ctx, cancel: cancel}
}

func (s *Session) ID() string { return s.id }
func (s *Session) Context() context.Context { return s.ctx }

func (s *Session) Go(fn func(context.Context)) error {
	if s.closed.Load() {
		return ErrClosed
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		fn(s.ctx)
	}()
	return nil
}

func (s *Session) Close() {
	if s.closed.Swap(true) {
		return
	}
	s.cancel()
	s.wg.Wait()
}
