package uiworkbench

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kube"
	"testing"
)

func TestNativeConnectionSections(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("scale-%g-dark-%t", scale, dark), func(t *testing.T) {
				w := New()
				t.Cleanup(w.Close)
				w.connectionsOpen = true
				w.path = "/isolated/config"
				tt := ui.NewTester(w.View, 1100, 700)
				tt.SetDark(dark)
				tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
				for _, label := range []string{"Configuration", "Credentials", "Browser sign-in", "Configuration"} {
					if err := tt.Click(label); err != nil {
						t.Fatal(err)
					}
					if _, ok := tt.Find("Connection sections"); !ok {
						t.Fatal("connection task has no section navigation")
					}
					if w.vaultOpen != (label == "Credentials") || w.oidcOpen != (label == "Browser sign-in") {
						t.Fatalf("wrong connection section: %s", label)
					}
					if err := tt.Click(label); err != nil {
						t.Fatal(err)
					}
					if w.vaultOpen != (label == "Credentials") || w.oidcOpen != (label == "Browser sign-in") {
						t.Fatal("selecting the current section closed it")
					}
				}
				if w.path != "/isolated/config" {
					t.Fatal("section navigation changed the configuration path")
				}
				saveNativeScreenshot(t, tt)
			})
		}
	}
}

func TestNativeConnectionControlsFollowTaskAndPendingState(t *testing.T) {
	w := New()
	t.Cleanup(w.Close)
	w.backend = &kube.Backend{}
	w.activeContext = "fixture-context"
	w.activeNamespace = "commerce"
	w.namespace = "commerce"
	w.contexts = []string{"fixture-context"}
	w.currentContext = "fixture-context"
	tt := ui.NewTester(w.View, 1100, 700)
	for _, scale := range []float32{1, 2} {
		tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
		for _, label := range []string{"Cluster context", "Connect"} {
			if _, ok := tt.Find(label); ok {
				t.Errorf("connected resource task still displays %s", label)
			}
		}
		for _, label := range []string{"Namespace", "Disconnect"} {
			if _, ok := tt.Find(label); !ok {
				t.Errorf("global scope control missing: %s", label)
			}
		}
		if _, ok := tt.Find("Active connection"); !ok {
			t.Fatal("active scope is hidden")
		}
		if err := tt.Click("Connections"); err != nil {
			t.Fatal(err)
		}
		for _, label := range []string{"Cluster context", "Namespace", "Connect", "Disconnect"} {
			if _, ok := tt.Find(label); !ok {
				t.Errorf("connection task cannot reach %s", label)
			}
		}
		if err := tt.Click("Resources"); err != nil {
			t.Fatal(err)
		}
		if w.activeContext != "fixture-context" || w.namespace != "commerce" || w.backend == nil {
			t.Fatal("task navigation changed connection")
		}
	}
	for _, pending := range []*bool{&w.connectionPending, &w.vaultPending, &w.oidcPending, &w.trustRequired} {
		*pending = true
		tt.Frame()
		if _, ok := tt.Find("Cluster context"); !ok {
			t.Error("pending connection controls hidden")
		}
		*pending = false
	}
	w.backend = nil
	tt.Frame()
	if _, ok := tt.Find("Connect"); !ok {
		t.Error("disconnected connection entry hidden")
	}
}

func TestNativeScopeNoticesStayVisibleWithoutExtraSummaryRow(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		for _, notice := range []string{
			"63 resource types; 1 unavailable API group(s)",
			"Discovery unavailable; built-in resources remain accessible",
			"Delivery retained in this workspace. Reopen Deliver manifests to continue; accepted objects are not rolled back.",
		} {
			t.Run(notice+string(rune('0'+int(scale))), func(t *testing.T) {
				w := New()
				t.Cleanup(w.Close)
				w.backend = &kube.Backend{}
				w.namespace, w.notice = "commerce", notice
				tt := ui.NewTester(w.View, 1100, 700)
				tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
				n, ok := tt.Find(notice)
				disconnect, _ := tt.Find("Disconnect")
				if !ok || n.X < 0 || n.X+n.W > 1100 || n.Y+n.H > 700 || n.W < 200 {
					saveNativeScreenshot(t, tt)
					t.Fatalf("notice is missing or clipped: %+v", n)
				}
				if scale == 1 && notice == "63 resource types; 1 unavailable API group(s)" && n.Y >= disconnect.Y+disconnect.H {
					saveNativeScreenshot(t, tt)
					t.Fatalf("partial discovery warning adds a separate row: %+v, disconnect %+v", n, disconnect)
				}
				saveNativeScreenshot(t, tt)
			})
		}
	}
}

func TestNativeTaskNavigationShowsCurrentArea(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			t.Run(fmt.Sprintf("dark-%t-scale-%g", dark, scale), func(t *testing.T) {
				w := New()
				t.Cleanup(w.Close)
				w.backend = &kube.Backend{}
				w.applications = applicationState{open: true, names: []string{"checkout"}, selected: "checkout"}
				tt := ui.NewTester(w.View, 1100, 800)
				tt.SetDark(dark)
				tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
				for _, current := range []string{"Applications", "Connections", "Resources"} {
					if current != "Applications" {
						if err := tt.Click(current); err != nil {
							t.Fatal(err)
						}
					}
					img := tt.Image()
					for _, label := range []string{"Applications", "Resources", "Connections"} {
						r, ok := tt.Find(label)
						if !ok {
							t.Fatalf("missing navigation: %s", label)
						}
						accentPixels, total := 0, 0
						for y := int(r.Y); y < int(r.Y+r.H); y++ {
							for x := int(r.X); x < int(r.X+r.W); x++ {
								pixel := img.RGBAAt(x, y)
								total++
								if int(pixel.B)-int(pixel.R) > 40 {
									accentPixels++
								}
							}
						}
						accented := accentPixels > total/4
						if accented != (label == current) {
							saveNativeScreenshot(t, tt)
							t.Fatalf("current %s, button %s lacks exclusive selection: %v", current, label, accentPixels)
						}
					}
					saveNativeScreenshot(t, tt)
				}
			})
		}
	}
}

func TestNativeContextSelectorEmptyThenLoaded(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprintf("scale-%g", scale), func(t *testing.T) {
			w := New()
			t.Cleanup(w.Close)
			tt := ui.NewTester(w.View, 1100, 700)
			tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			if !tt.HasText("No contexts") {
				t.Fatal("empty context selector has no visible explanation")
			}
			if w.currentContext != "" {
				t.Fatal("empty label became a context value")
			}
			w.contexts = []string{"fixture-one", "fixture-two"}
			w.currentContext = "fixture-one"
			tt.Frame()
			if tt.HasText("No contexts") {
				t.Fatal("empty label survived context loading")
			}
			if err := tt.Click("Cluster context"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click("fixture-two"); err != nil {
				t.Fatal(err)
			}
			if w.currentContext != "fixture-two" {
				t.Fatal("loaded context selector did not select the second context")
			}
			saveNativeScreenshot(t, tt)
		})
	}
}
