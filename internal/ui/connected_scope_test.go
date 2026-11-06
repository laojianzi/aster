package uiworkbench

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kube"
)

func TestNativeConnectedClusterIdentity(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprintf("scale-%g", scale), func(t *testing.T) {
			h := newRelationshipHarness(t, 10*time.Second)
			cluster := "production-eu-platform-commerce-long-cluster-name"
			h.w.backend = &kube.Backend{}
			h.w.contexts = []string{"unconnected-draft"}
			h.w.activeContext = cluster
			h.w.status = "Live"
			h.w.activeNamespace, h.w.namespace = "commerce", "commerce"
			h.w.currentContext = "unconnected-draft"
			h.tt.SetSize(1100, 700)
			h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			identity, ok := h.tt.Find("Connected cluster")
			if !ok || !slices.Contains(h.tt.Texts(), cluster) {
				t.Fatal("active cluster identity missing from connected header")
			}
			namespace, ok := h.tt.Find("Namespace")
			if !ok || identity.Y >= namespace.Y+namespace.H || identity.X+identity.W > namespace.X || namespace.X+namespace.W > 1100 {
				t.Fatalf("cluster and namespace are not adjacent: %+v, %+v", identity, namespace)
			}
			for _, label := range []string{"Disconnect", "Active connection"} {
				r, ok := h.tt.Find(label)
				if !ok || r.X+r.W > 1100 || r.Y+r.H > 700 {
					t.Fatalf("scope control outside viewport: %s %+v", label, r)
				}
			}
			for _, keyboard := range []bool{false, true} {
				if keyboard {
					h.tt.Move(1090, 690)
					for range 20 {
						if h.tt.Focused("Connected cluster") {
							break
						}
						h.tt.Key(0, ui.KeyTab)
					}
					if !h.tt.Focused("Connected cluster") {
						t.Fatal("cluster identity cannot be inspected with keyboard")
					}
				} else {
					h.tt.Move(identity.X+identity.W/2, identity.Y+identity.H/2)
				}
				h.tt.Advance(time.Second)
				count := 0
				for _, text := range h.tt.Texts() {
					if text == cluster {
						count++
					}
				}
				if count < 2 {
					t.Fatalf("full cluster tooltip missing, keyboard=%v", keyboard)
				}
			}
			saveNativeScreenshot(t, h.tt)
			if err := h.tt.Click("Disconnect"); err != nil {
				t.Fatal(err)
			}
			if _, ok := h.tt.Find("Connected cluster"); ok {
				t.Fatal("disconnected header retains connected cluster")
			}
			if _, ok := h.tt.Find("Cluster context"); !ok {
				t.Fatal("disconnect did not restore context selection")
			}
		})
	}
}

func TestNativeEmptyConnectionFooter(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprintf("scale-%g", scale), func(t *testing.T) {
			h := newRelationshipHarness(t, 10*time.Second)
			h.tt.SetSize(1100, 700)
			h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			saveNativeScreenshot(t, h.tt)
			if _, ok := h.tt.Find("Active connection"); ok {
				t.Fatal("empty connection exposes a scope placeholder")
			}
			if _, ok := h.tt.Find("Connection status"); !ok {
				t.Fatal("connection status is missing")
			}
		})
	}
}
