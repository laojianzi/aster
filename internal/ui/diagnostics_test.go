package uiworkbench

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNativeLogStatusOnlyShowsObservedInformation(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		for _, tc := range []struct {
			name, status string
			dropped      uint64
			want         string
		}{
			{name: "initial"},
			{name: "stopped", status: "Stopped", want: "Stopped"},
			{name: "truncated", status: "Streaming", dropped: 3, want: "Streaming / Truncated lines: 3"},
			{name: "truncated-without-status", dropped: 1, want: "Truncated lines: 1"},
			{name: "error", status: "Stream ended: access denied", want: "Stream ended: access denied"},
		} {
			t.Run(fmt.Sprintf("%s-scale-%g", tc.name, scale), func(t *testing.T) {
				w := New()
				t.Cleanup(w.Close)
				w.detail, w.detailKind, w.detailMode = modelObject("status-uid", "payments"), catalog()[0], "Overview"
				w.containers = []string{"app"}
				w.container = "app"
				tt := ui.NewTester(w.View, 1100, 800)
				tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
				for _, action := range []string{"Diagnostics", "Logs", "Expand diagnostic"} {
					if err := tt.Click(action); err != nil {
						t.Fatal(err)
					}
				}
				w.logStatus, w.logDropped = tc.status, tc.dropped
				tt.Frame()
				for _, text := range tt.Texts() {
					if strings.Contains(text, "0 truncated") || (strings.HasPrefix(text, " / ") && strings.Contains(text, "truncated")) {
						saveNativeScreenshot(t, tt)
						t.Fatalf("log status contains empty information: %q", text)
					}
				}
				if tc.want != "" && !tt.HasText(tc.want) {
					saveNativeScreenshot(t, tt)
					t.Fatalf("missing observed status %q", tc.want)
				}
				saveNativeScreenshot(t, tt)
				if err := tt.Click("Stop logs"); err != nil {
					t.Fatal(err)
				}
				if w.logStatus != "Stopped" {
					t.Fatal("status ablation broke stop action")
				}
			})
		}
	}
}
