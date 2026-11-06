package uiworkbench

import (
	"github.com/egoist/mygo/ui"
	"slices"
	"testing"
)

func TestNativeObjectTabsCompactNamesAndCollisions(t *testing.T) {
	w := New()
	t.Cleanup(w.Close)
	kind := catalog()[0]
	w.objectTabs = []objectTab{
		{kind: kind, row: resourceRow{Name: "checkout-api", Namespace: "commerce"}},
		{kind: kind, row: resourceRow{Name: "worker", Namespace: "commerce"}},
		{kind: kind, row: resourceRow{Name: "worker", Namespace: "billing"}},
	}
	tt := ui.NewTester(w.View, 1100, 700)
	for _, scale := range []float32{1, 2} {
		tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
		if !slices.Contains(tt.Texts(), "checkout-api") {
			t.Error("unique object name is not shown without repeated identity")
		}
		for _, label := range []string{"Pod v1 / commerce/worker", "Pod v1 / billing/worker"} {
			if !slices.Contains(tt.Texts(), label) {
				t.Errorf("same-name objects cannot be distinguished: %s", label)
			}
		}
		if _, ok := tt.Find("Pod v1 / commerce/checkout-api"); !ok {
			t.Error("full accessible object identity missing")
		}
		saveNativeScreenshot(t, tt)
	}
}
