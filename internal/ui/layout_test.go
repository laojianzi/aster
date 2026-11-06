package uiworkbench

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDetailActionsStayInsideMinimumWindow(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{{"minimum", 900, 480}, {"tiled", 936, 499}, {"desktop", 1100, 700}} {
		t.Run(size.name, func(t *testing.T) {
			w := New()
			w.detail = modelObject("layout-pod", "pod-with-a-long-name-that-must-not-push-actions-outside-the-panel")
			w.detailKind = catalog()[0]
			w.activeContext, w.activeNamespace = "production-cluster", "team"
			tt := ui.NewTester(w.View, size.width, size.height)
			for _, label := range []string{"Close detail", "YAML", "Edit", "Events", "Related", "Diagnostics", "Refresh detail", "Actions"} {
				r, ok := tt.Find(label)
				if !ok {
					t.Errorf("control %q not found", label)
					continue
				}
				if r.X < 0 || r.Y < 0 || r.X+r.W > float32(size.width-12) || r.Y+r.H > float32(size.height) || r.W <= 0 || r.H <= 0 {
					t.Errorf("%q outside the window: %+v", label, r)
				}
			}
			for _, label := range []string{"Logs", "Terminal", "Command", "Port forward"} {
				if err := tt.Click("Diagnostics"); err != nil {
					t.Fatal(err)
				}
				if err := tt.Click(label); err != nil {
					t.Fatal(err)
				}
				expected := label
				if label == "Port forward" {
					expected = "Forward"
				}
				if w.detailMode != expected {
					t.Fatalf("diagnostic %s did not open", label)
				}
			}
			dock, found := tt.Find("Diagnostic dock")
			if !found {
				t.Fatal("diagnostic dock is missing")
			}
			for _, label := range []string{"Remote port", "Start port forward", "Stop port forward"} {
				r, ok := tt.Find(label)
				if !ok || r.H <= 0 || r.Y < dock.Y || r.Y+r.H > dock.Y+dock.H {
					saveNativeScreenshot(t, tt)
					t.Fatalf("initial forwarding control requires scrolling: %s (%+v)", label, r)
				}
			}
			if _, ok := tt.Find("Forwarded address"); ok {
				t.Error("empty forwarding address reserves a blank editor")
			}
			w.forwardAddress = "127.0.0.1:43210 → team/layout-pod:8080"
			w.forwardActive = true
			tt.Frame()
			viewport, ok := tt.Find("Diagnostic dock")
			if !ok {
				t.Fatal("diagnostic scroll viewport is missing")
			}
			for _, label := range []string{"Forwarded address", "Stop port forward"} {
				tt.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, -10000)
				reached := false
				for range 30 {
					r, found := tt.Find(label)
					if found && r.W > 0 && r.H > 0 && r.X >= viewport.X && r.X+r.W <= viewport.X+viewport.W && r.Y >= viewport.Y && r.Y+r.H <= viewport.Y+viewport.H {
						reached = true
						break
					}
					tt.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, 20)
				}
				if !reached {
					saveNativeScreenshot(t, tt)
					t.Fatalf("diagnostic control unreachable by scrolling: %s", label)
				}
			}

			if err := tt.Click("Stop port forward"); err != nil {
				t.Fatal(err)
			}
			if _, ok := tt.Find("Forwarded address"); ok {
				t.Error("stopped forwarding retains an empty address editor")
			}
			tt.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, 10000)
			if !tt.HasText("Stopped") {
				t.Error("stopped forwarding status is missing")
			}
			saveNativeScreenshot(t, tt)

		})
	}
}
