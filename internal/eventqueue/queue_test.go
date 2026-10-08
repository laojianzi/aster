package eventqueue

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)
func TestCoalescesLatestValue(t *testing.T) {
	q := New[string, int](2)
	_ = q.Put("a", 1); _ = q.Put("a", 2)
	if got := q.Drain(0); !reflect.DeepEqual(got, []int{2}) { t.Fatal(got) }
}
func TestOverflowIsExplicitAndDoesNotLoseDelete(t *testing.T) {
	q := New[string, string](2)
	_ = q.Put("a", "deleted-a"); _ = q.Put("b", "b")
	if err := q.Put("c", "c"); !errors.Is(err, ErrOverflow) { t.Fatal(err) }
	if got := q.Drain(0); !reflect.DeepEqual(got, []string{"deleted-a", "b"}) { t.Fatal(got) }
}
func TestClosedRejectsPut(t *testing.T) {
	q := New[string, int](1); q.Close()
	if err := q.Put("a", 1); !errors.Is(err, ErrClosed) { t.Fatal(err) }
}
func TestConcurrentPutDrainClose(t *testing.T) {
	q := New[int, int](32)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ { wg.Add(1); go func(i int) { defer wg.Done(); for j := 0; j < 1000; j++ { _ = q.Put(i, j); q.Drain(2) } }(i) }
	wg.Wait(); q.Close()
	if q.Len() != 0 { t.Fatal("closed queue retains entries") }
}
