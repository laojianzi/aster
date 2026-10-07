package eventqueue

import "testing"

func TestCoalescesLatestValue(t *testing.T) {
	q := New[string, int](3)
	_ = q.Put("a", 1)
	_ = q.Put("a", 2)
	got := q.Drain(0)
	if len(got) != 1 || got[0] != 2 {
		t.Fatalf("got %v, want [2]", got)
	}
}

func TestEvictsOldestDistinctKey(t *testing.T) {
	q := New[string, int](2)
	_ = q.Put("a", 1)
	_ = q.Put("b", 2)
	_ = q.Put("c", 3)
	got := q.Drain(0)
	if len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("got %v, want [2 3]", got)
	}
}

func TestClosedRejectsPut(t *testing.T) {
	q := New[string, int](1)
	q.Close()
	if err := q.Put("a", 1); err != ErrClosed {
		t.Fatalf("got %v, want ErrClosed", err)
	}
}
