package workspace

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type testResource struct {
	closed atomic.Int32
	gate   <-chan struct{}
}

func (r *testResource) Close() {
	if r.gate != nil {
		<-r.gate
	}
	r.closed.Add(1)
}
func waitEntry(t *testing.T, e *entry) {
	t.Helper()
	select {
	case <-e.done:
	case <-time.After(time.Second):
		t.Fatal("workspace did not join")
	}
}
func TestWorkspaceCloseIsIndependentAndChurnIsBounded(t *testing.T) {
	gate := make(chan struct{})
	var callbacks []func()
	var contexts []context.Context
	var resources []*testResource
	emptied := 0
	m := New(context.Background(), 2, func(ctx context.Context, slot int, closed func()) (Resource, error) {
		r := &testResource{}
		if slot == 1 {
			r.gate = gate
		}
		resources = append(resources, r)
		contexts = append(contexts, ctx)
		callbacks = append(callbacks, closed)
		return r, nil
	}, func() { emptied++ })
	defer func() { close(gate); m.Shutdown() }()
	if m.Open() != nil || m.Open() != nil || !errors.Is(m.Open(), ErrLimit) {
		t.Fatal("limit not enforced")
	}
	callbacks[0]()
	callbacks[0]()
	if contexts[0].Err() == nil || contexts[1].Err() != nil || m.Active() != 1 || emptied != 0 {
		t.Fatal("closing one affected another")
	}
	if !errors.Is(m.Open(), ErrLimit) {
		t.Fatal("joining resource must retain its budget slot")
	}
	callbacks[1]()
	waitEntry(t, m.entries[2])
	if emptied != 1 || m.Active() != 0 || !errors.Is(m.Open(), ErrClosed) {
		t.Fatal("last-window shutdown incorrect")
	}
	if resources[1].closed.Load() != 1 {
		t.Fatal("resource not joined exactly once")
	}
}
func TestWorkspaceSlotReuseRejectsOldCallbacks(t *testing.T) {
	var closed []func()
	m := New(context.Background(), 2, func(_ context.Context, _ int, c func()) (Resource, error) {
		closed = append(closed, c)
		return &testResource{}, nil
	}, nil)
	defer m.Shutdown()
	_ = m.Open()
	_ = m.Open()
	old := m.entries[1]
	closed[0]()
	waitEntry(t, old)
	if err := m.Open(); err != nil {
		t.Fatal(err)
	}
	closed[0]()
	if m.Active() != 2 || m.entries[1] == old {
		t.Fatal("old callback closed reused workspace")
	}
}
func TestWorkspaceEarlyCloseAndFactoryFailureDoNotLeak(t *testing.T) {
	r := &testResource{}
	m := New(context.Background(), 1, func(_ context.Context, _ int, c func()) (Resource, error) { c(); return r, nil }, nil)
	_ = m.Open()
	m.Shutdown()
	m.Shutdown()
	if r.closed.Load() != 1 {
		t.Fatal("early close lost the resource")
	}
	errExpected := errors.New("factory failed")
	m = New(context.Background(), 1, func(_ context.Context, _ int, c func()) (Resource, error) { return nil, errExpected }, nil)
	if !errors.Is(m.Open(), errExpected) {
		t.Fatal("factory failure ignored")
	}
	m.Shutdown()
}
func TestWorkspaceShutdownCancelsEveryWorkspaceBeforeJoining(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	var contexts []context.Context
	m := New(parent, 4, func(ctx context.Context, _ int, _ func()) (Resource, error) {
		contexts = append(contexts, ctx)
		return &testResource{}, nil
	}, nil)
	for range 4 {
		if err := m.Open(); err != nil {
			t.Fatal(err)
		}
	}
	m.Shutdown()
	m.Shutdown()
	for _, ctx := range contexts {
		if ctx.Err() == nil {
			t.Fatal("live context after shutdown")
		}
	}
	if !errors.Is(m.Open(), ErrClosed) {
		t.Fatal("opened after shutdown")
	}
	m = New(parent, 1, func(context.Context, int, func()) (Resource, error) {
		t.Fatal("factory called after parent canceled")
		return nil, nil
	}, nil)
	cancel()
	if !errors.Is(m.Open(), ErrClosed) {
		t.Fatal("parent cancellation ignored")
	}
	m.Shutdown()
}
