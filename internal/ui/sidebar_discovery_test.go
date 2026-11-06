package uiworkbench

import (
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kube"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestNativeSidebarOpensSelectedGroupAndKeepsManualCollapse(t *testing.T) {
	h := newRelationshipHarness(t, 10*time.Second)
	epoch := h.w.scopeEpoch
	h.click("Pods")
	if h.w.scopeEpoch != epoch {
		t.Fatal("activating the selected resource restarted its scope")
	}
	if _, ok := h.tt.Find("Services"); ok {
		t.Fatal("unselected resource groups start expanded")
	}
	h.click("Network")
	if _, ok := h.tt.Find("Services"); !ok {
		t.Fatal("resource group did not expand")
	}
	h.click("Network")
	h.w.chooseKind(catalog()[6])
	h.tt.Frame()
	if _, ok := h.tt.Find("Services"); !ok {
		t.Fatal("programmatic navigation left selected resource hidden")
	}
	h.click("Network")
	h.tt.Frame()
	if _, ok := h.tt.Find("Services"); ok {
		t.Fatal("render reopened a manually collapsed selected group")
	}
	h.w.chooseKind(catalog()[0])
	h.tt.Frame()
	if _, ok := h.tt.Find("Pods"); !ok {
		t.Fatal("returning to workloads lost navigation")
	}
	saveNativeScreenshot(t, h.tt)
}

func TestNativeSidebarDiscoveryKeepsTaskOrder(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(string(rune('0'+int(scale))), func(t *testing.T) {
			h := newRelationshipHarness(t, 10*time.Second)
			slices.Reverse(h.w.kinds)
			h.tt.SetSize(1100, 700)
			h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			h.selectSidebarResource("Pods")
			for _, resource := range []string{"deployments", "statefulsets", "daemonsets", "replicasets", "jobs", "cronjobs"} {
				h.tt.Key(0, ui.KeyDown)
				h.tt.Key(0, ui.KeyEnter)
				if h.w.currentKind.GVR.Resource != resource {
					t.Fatalf("next resource = %s, want %s", h.w.currentKind.GVR.Resource, resource)
				}
			}
			saveNativeScreenshot(t, h.tt)
		})
	}
}

func TestNativeSidebarCustomResourceKeepsOwnName(t *testing.T) {
	h := newRelationshipHarness(t, 10*time.Second)
	custom := kube.ResourceKind{GVR: schema.GroupVersionResource{Group: "tenant.example", Version: "v1", Resource: "pods"}, Kind: "TenantPod", Namespaced: true}
	h.w.kinds = append(h.w.kinds, custom)
	h.tt.Frame()
	h.click("Custom resources")
	if _, ok := h.tt.Find("TenantPod"); !ok {
		t.Fatal("custom pods resource was relabeled as built-in Pods")
	}
	h.selectSidebarResource("TenantPod")
	if h.w.currentKind.GVR != custom.GVR {
		t.Fatal("custom resource navigation selected a different API")
	}
	saveNativeScreenshot(t, h.tt)
}

func TestNativeSidebarDistinguishesSameNamedAPIGroups(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(string(rune('0'+int(scale))), func(t *testing.T) {
			h := newRelationshipHarness(t, 10*time.Second)
			first := kube.ResourceKind{GVR: schema.GroupVersionResource{Group: "a.io", Version: "v1", Resource: "backups"}, Kind: "Backup", Namespaced: true}
			second := first
			second.GVR.Group = "b.io"
			h.w.kinds = append(h.w.kinds, first, second)
			h.tt.SetSize(1100, 800)
			h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			h.selectSidebarResource("Custom resources")
			for _, kind := range []kube.ResourceKind{first, second} {
				if !h.tt.HasText(kind.Label()) {
					saveNativeScreenshot(t, h.tt)
					t.Fatalf("same-named API has no visible group: %s", kind.Label())
				}
				h.selectSidebarResource(kind.Label())
				if h.w.currentKind.GVR != kind.GVR {
					t.Fatalf("selected API = %v, want %v", h.w.currentKind.GVR, kind.GVR)
				}
			}
			saveNativeScreenshot(t, h.tt)
		})
	}
}

func TestNativeSidebarLongAPIIdentityTooltip(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(string(rune('0'+int(scale))), func(t *testing.T) {
			h := newRelationshipHarness(t, 10*time.Second)
			first := kube.ResourceKind{GVR: schema.GroupVersionResource{Group: "backup.platform.team-a.example.com", Version: "v1", Resource: "backups"}, Kind: "Backup", Namespaced: true}
			second := first
			second.GVR.Group = "backup.platform.team-b.example.com"
			h.w.kinds = append(h.w.kinds, first, second)
			h.tt.SetSize(1100, 800)
			h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			h.selectSidebarResource(first.Label())
			h.tt.Move(1090, 790)
			h.tt.Advance(time.Second)
			for _, keyboard := range []bool{false, true} {
				label := first.Label()
				if keyboard {
					h.tt.Move(1090, 790)
					h.tt.Key(0, ui.KeyDown)
					label = second.Label()
				} else {
					r, _ := h.tt.Find(label)
					h.tt.Move(r.X+r.W/2, r.Y+r.H/2)
				}
				h.tt.Advance(time.Second)
				matches := 0
				for _, text := range h.tt.Texts() {
					if text == label {
						matches++
					}
				}
				if matches < 2 {
					saveNativeScreenshot(t, h.tt)
					t.Fatalf("full identity tooltip missing, keyboard=%v: %v", keyboard, h.tt.Texts())
				}
				if keyboard {
					saveNativeScreenshot(t, h.tt)
					h.tt.Key(0, ui.KeyEnter)
					if h.w.currentKind.GVR != second.GVR {
						t.Fatal("keyboard selected wrong API group")
					}
				}
			}
		})
	}
}
