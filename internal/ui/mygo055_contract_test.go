package uiworkbench

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The toolkit clock is separate from operation/credential deadlines. Only UI
// fixtures use Advance; real network/authorization clocks are not replaced.
func TestNativeMyGo055ExplicitClockAndCurrentFrameShortcut(t *testing.T) {
	var started, seen time.Time
	armed, ready, handled := false, false, 0
	tt := ui.NewTester(func(c *ui.Context) {
		seen = c.Now()
		if started.IsZero() {
			started = seen
		}
		if seen.Sub(started) < 300*time.Millisecond {
			c.After(300*time.Millisecond - seen.Sub(started))
		} else {
			ready = true
		}
		if armed && c.Shortcut(0, ui.KeyEscape) {
			handled++
		}
	}, 800, 500)
	initial := tt.Now()
	tt.Advance(-time.Second)
	if !tt.Now().Equal(initial) {
		t.Fatal("negative advance moved UI time backwards")
	}
	for range 3 {
		tt.Frame()
	}
	if ready || !seen.Equal(initial) {
		t.Fatal("ordinary frames advanced the deterministic UI clock")
	}
	tt.Advance(299 * time.Millisecond)
	if ready {
		t.Fatal("scheduled UI work ran before its deadline")
	}
	tt.Advance(time.Millisecond)
	if !ready || tt.Now().Sub(initial) != 300*time.Millisecond {
		t.Fatal("explicit advance did not deliver the scheduled UI frame")
	}
	armed = true // A new shortcut exists before a frame has painted it.
	tt.Key(0, ui.KeyEscape)
	if handled != 1 {
		t.Fatalf("current-frame shortcut not handled exactly once: %d", handled)
	}
	for range 3 {
		tt.Frame()
	}
	armed = false // Removing the business condition must revoke the shortcut.
	tt.Key(0, ui.KeyEscape)
	if handled != 1 {
		t.Fatal("stale shortcut or unrelated frame replayed its side effect")
	}
}

// A dependency fixture, not adoption of SplitPercent in Aster. The existing
// workbench continues using its bounded navigation and explicit layout policy.
func TestNativeMyGo055SplitListReportsCurrentSelectionOnce(t *testing.T) {
	for _, direction := range []ui.Direction{ui.LeftToRight, ui.RightToLeft} {
		t.Run(map[ui.Direction]string{ui.LeftToRight: "ltr", ui.RightToLeft: "rtl"}[direction], func(t *testing.T) {
			pane := float32(300)
			choice := -1
			state := ui.ListState{Selected: &choice}
			changes, selected := 0, -1
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Split(c.Key("patch-split"), &pane, func() {
					if ui.List(c.Key("patch-list"), &state, 2, func(i int) {
						ui.Text(c, []string{"First candidate", "Second candidate"}[i]).Height(30)
					}).Fill().Changed() {
						changes++
						selected = choice
					}
				}, func() { ui.Text(c, "Other pane").Label("Other pane") }).Fill()
			}, 800, 300)
			tt.SetDirection(direction)
			if err := tt.Click("Second candidate"); err != nil {
				t.Fatal(err)
			}
			if changes != 1 || selected != 1 || choice != 1 {
				t.Fatalf("split list delayed/lost/replayed selection: changes=%d selected=%d state=%d", changes, selected, choice)
			}
			tt.SetSize(240, 300)
			tt.Frame()
			left, okLeft := tt.Find("Second candidate")
			right, okRight := tt.Find("Other pane")
			if !okLeft || !okRight || left.X < 0 || right.X < 0 || right.X >= 240 || left.X >= 240 || pane != 300 || changes != 1 {
				t.Fatal("small split lost a pane, mutated remembered size, or replayed selection")
			}
		})
	}
}
