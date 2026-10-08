package uiworkbench

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDetailActionsStayInsideMinimumWindow(t *testing.T) {
	w := New()
	w.detail = modelObject("layout-pod", "pod-with-a-long-name-that-must-not-push-actions-outside-the-panel")
	w.detailKind = catalog()[0]
	w.activeContext, w.activeNamespace = "production-cluster", "team"
	tt := ui.NewTester(w.View, 1100, 700)
	for _, label := range []string{"Close detail", "YAML", "Edit", "Events", "Related", "Logs", "Port forward", "Refresh detail", "Replica count", "Preview scale", "Preview restart", "Preview delete"} {
		r, ok := tt.Find(label)
		if !ok {
			t.Errorf("control %q not found", label)
			continue
		}
		if r.X < 0 || r.Y < 0 || r.X+r.W > 1088 || r.Y+r.H > 700 || r.W <= 0 || r.H <= 0 {
			t.Errorf("%q outside the 1100x700 window: %+v", label, r)
		}
	}
	saveNativeScreenshot(t, tt)
}
