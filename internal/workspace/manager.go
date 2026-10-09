// Package workspace owns bounded independent desktop workspace lifetimes.
package workspace

import (
	"context"
	"errors"
)

var (
	ErrLimit  = errors.New("workspace limit reached; close a window and wait for its sessions to stop")
	ErrClosed = errors.New("workspace manager is stopping")
)

type Resource interface{ Close() }
type Factory func(ctx context.Context, slot int, closed func()) (Resource, error)

type entry struct {
	cancel context.CancelFunc
	ready  chan Resource
	done   chan struct{}
	closed bool
}

// Manager is owned by the UI thread. Open, callbacks supplied to Factory and
// Active run on that thread; Shutdown runs after the event loop has returned.
// Resource.Close runs off the UI thread and must join all of its workers.
// Closing resources retain a slot until joining finishes, bounding rapid churn.
// A Manager is an in-process isolation/lifecycle boundary, not a security sandbox.
type Manager struct {
	parent   context.Context
	limit    int
	factory  Factory
	onEmpty  func()
	entries  map[int]*entry
	stopping bool
}

func New(parent context.Context, limit int, factory Factory, onEmpty func()) *Manager {
	if parent == nil || limit < 1 || factory == nil {
		panic("workspace: invalid manager configuration")
	}
	return &Manager{parent: parent, limit: limit, factory: factory, onEmpty: onEmpty, entries: make(map[int]*entry)}
}
func (m *Manager) Active() int {
	n := 0
	for _, e := range m.entries {
		if !e.closed {
			n++
		}
	}
	return n
}
func (m *Manager) Open() error {
	if m.stopping || m.parent.Err() != nil {
		return ErrClosed
	}
	for slot, e := range m.entries {
		if e.closed {
			select {
			case <-e.done:
				delete(m.entries, slot)
			default:
			}
		}
	}
	if len(m.entries) >= m.limit {
		return ErrLimit
	}
	slot := 1
	for m.entries[slot] != nil {
		slot++
	}
	ctx, cancel := context.WithCancel(m.parent)
	e := &entry{cancel: cancel, ready: make(chan Resource, 1), done: make(chan struct{})}
	m.entries[slot] = e
	go func() {
		defer close(e.done)
		<-ctx.Done()
		// An early closed callback cannot race resource construction or lose it.
		r := <-e.ready
		if r != nil {
			r.Close()
		}
	}()
	r, err := m.factory(ctx, slot, func() { m.retire(slot, e, true) })
	e.ready <- r
	if err != nil {
		m.retire(slot, e, false)
	}
	return err
}
func (m *Manager) retire(slot int, e *entry, notify bool) {
	if m.entries[slot] != e || e.closed {
		return
	}
	e.closed = true
	e.cancel()
	if notify && !m.stopping && m.Active() == 0 {
		m.stopping = true
		if m.onEmpty != nil {
			m.onEmpty()
		}
	}
}

// Shutdown cancels everyone before joining anyone; it must not block the event
// loop that workers rely on. It is idempotent once the UI loop has stopped.
func (m *Manager) Shutdown() {
	m.stopping = true
	for _, e := range m.entries {
		e.closed = true
		e.cancel()
	}
	for _, e := range m.entries {
		<-e.done
	}
	clear(m.entries)
}
