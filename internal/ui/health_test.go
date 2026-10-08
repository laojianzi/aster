package uiworkbench

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDetailSwitchCancelsHealthAndKeepsHistoryBounded(t *testing.T) {
	w := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.healthCancel = cancel
	w.healthActive = true
	w.healthText = "old resource status"
	old := w.healthEpoch
	w.clearDetail()
	if ctx.Err() == nil || w.healthActive || w.healthText != "" || w.healthEpoch <= old {
		t.Fatal("health observation survived closing the detail")
	}
	for range 130 {
		w.recordHistory("synthetic operation metadata only")
	}
	if len(w.history) != 100 {
		t.Fatal("operation history grew without a bound")
	}
}
func TestHealthControlsAreVisibleAtMinimumWindowAndDoNotRunWrites(t *testing.T) {
	w := New()
	w.detail = modelObject("one", "pod")
	w.detailKind = catalog()[0]
	w.detailMode = "Health"
	w.healthText = "Tracking: Watching"
	w.healthActive = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.healthCancel = cancel
	tt := ui.NewTester(w.View, 1100, 700)
	for _, label := range []string{"Health", "Refresh health", "Track readiness", "Stop tracking", "Workload health"} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("missing native control %s", label)
		}
	}
	if err := tt.Click("Stop tracking"); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil || w.healthActive || w.pendingWrites != 0 {
		t.Fatal("stop observation did not stay read-only")
	}
	saveNativeScreenshot(t, tt)
}
