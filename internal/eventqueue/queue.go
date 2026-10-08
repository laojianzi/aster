package eventqueue

import (
	"errors"
	"sync"
)

var (
	ErrClosed = errors.New("queue closed")
	ErrOverflow = errors.New("resource queue full: subscriber must resynchronize")
)

// Queue coalesces pending values without silently discarding distinct keys.
// On ErrOverflow, the caller must abandon its incremental view and resync.
// Values become owned by the queue after Put and must not subsequently mutate.
type Queue[K comparable, V any] struct {
	mu sync.Mutex
	capacity int
	closed bool
	order []K
	values map[K]V
}
func New[K comparable, V any](capacity int) *Queue[K, V] {
	if capacity <= 0 { panic("eventqueue: capacity must be positive") }
	return &Queue[K, V]{capacity: capacity, values: make(map[K]V)}
}
func (q *Queue[K, V]) Put(key K, value V) error {
	q.mu.Lock(); defer q.mu.Unlock()
	if q.closed { return ErrClosed }
	if _, ok := q.values[key]; ok { q.values[key] = value; return nil }
	if len(q.order) >= q.capacity { return ErrOverflow }
	q.order = append(q.order, key); q.values[key] = value
	return nil
}
func (q *Queue[K, V]) Drain(max int) []V {
	q.mu.Lock(); defer q.mu.Unlock()
	if max <= 0 || max > len(q.order) { max = len(q.order) }
	out := make([]V, 0, max)
	for _, key := range q.order[:max] { out = append(out, q.values[key]); delete(q.values, key) }
	copy(q.order, q.order[max:])
	var zero K
	for i := len(q.order)-max; i < len(q.order); i++ { q.order[i] = zero }
	q.order = q.order[:len(q.order)-max]
	return out
}
func (q *Queue[K, V]) Len() int { q.mu.Lock(); defer q.mu.Unlock(); return len(q.order) }
func (q *Queue[K, V]) Close() { q.mu.Lock(); defer q.mu.Unlock(); q.closed = true; q.order = nil; clear(q.values) }
