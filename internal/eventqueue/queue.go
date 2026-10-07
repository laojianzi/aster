package eventqueue

import (
	"errors"
	"sync"
)

var ErrClosed = errors.New("queue closed")

type Queue[K comparable, V any] struct {
	mu       sync.Mutex
	capacity int
	closed   bool
	order    []K
	present  map[K]struct{}
	values   map[K]V
}

func New[K comparable, V any](capacity int) *Queue[K, V] {
	if capacity <= 0 {
		panic("eventqueue: capacity must be positive")
	}
	return &Queue[K, V]{
		capacity: capacity,
		present:  make(map[K]struct{}, capacity),
		values:   make(map[K]V, capacity),
	}
}

func (q *Queue[K, V]) Put(key K, value V) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrClosed
	}
	if _, ok := q.present[key]; ok {
		q.values[key] = value
		return nil
	}
	if len(q.order) >= q.capacity {
		oldest := q.order[0]
		q.order = q.order[1:]
		delete(q.present, oldest)
		delete(q.values, oldest)
	}
	q.order = append(q.order, key)
	q.present[key] = struct{}{}
	q.values[key] = value
	return nil
}

func (q *Queue[K, V]) Drain(max int) []V {
	q.mu.Lock()
	defer q.mu.Unlock()
	if max <= 0 || max > len(q.order) {
		max = len(q.order)
	}
	out := make([]V, 0, max)
	for _, key := range q.order[:max] {
		out = append(out, q.values[key])
		delete(q.present, key)
		delete(q.values, key)
	}
	q.order = append([]K(nil), q.order[max:]...)
	return out
}

func (q *Queue[K, V]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.order)
}

func (q *Queue[K, V]) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.order = nil
	clear(q.present)
	clear(q.values)
}
