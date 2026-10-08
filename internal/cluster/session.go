package cluster

import (
	"context"
	"errors"
	"sync"
)

var ErrClosed = errors.New("cluster session closed")

// Session owns workers for one immutable cluster identity. Workers must not call
// Close themselves: Close joins every accepted worker, including the caller.
type Session struct {
	id string
	ctx context.Context
	cancel context.CancelFunc
	mu sync.Mutex
	closed bool
	wg sync.WaitGroup
}

func NewSession(parent context.Context, id string) *Session {
	ctx, cancel := context.WithCancel(parent)
	return &Session{id: id, ctx: ctx, cancel: cancel}
}
func (s *Session) ID() string { return s.id }
func (s *Session) Context() context.Context { return s.ctx }
func (s *Session) Go(fn func(context.Context)) error {
	if fn == nil { return errors.New("cluster session: nil worker") }
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil { return ErrClosed }
	s.wg.Add(1)
	go func() { defer s.wg.Done(); fn(s.ctx) }()
	return nil
}

// All concurrent callers wait for the same workers; no caller returns early.
func (s *Session) Close() {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
}
