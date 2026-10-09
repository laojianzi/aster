package app

import "testing"

func TestWindowProbeRequiresSurvivingWorkspaceFrames(t *testing.T) {
	p := newWindowProbe()
	a, b := 0, 0
	p.Frame(1, 1, func() { a++ })
	if a != 0 {
		t.Fatal("first window closed before second painted")
	}
	p.Frame(2, 1, func() { b++ })
	p.Frame(1, 2, func() { a++ })
	if a != 1 || b != 0 {
		t.Fatal("incorrect close order")
	}
	p.Closed(1)
	p.Frame(2, 2, func() { b++ })
	if b != 0 || p.Complete() {
		t.Fatal("old frame counted as surviving activity")
	}
	p.Frame(2, 3, func() { b++ })
	if b != 1 {
		t.Fatal("second window did not close")
	}
	p.Closed(2)
	if !p.Complete() {
		t.Fatal("complete smoke not recorded")
	}
	p.Frame(2, 4, func() { b++ })
	if b != 1 {
		t.Fatal("duplicate close")
	}
}
