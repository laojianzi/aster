package uiworkbench

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/helmfixture"
	"github.com/laojianzi/aster/internal/operation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func liveUIServer(t *testing.T, text string, reads *atomic.Int32, mode *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("write attempted")
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case "/api/v1/namespaces/team/secrets":
			writeHelmMetadata(w)
		case "/api/v1/namespaces/team/secrets/sh.helm.release.v1.example.v2":
			_ = json.NewEncoder(w).Encode(helmfixture.WithManifest("team", "example", "deployed", 2, text))
		case "/api/v1":
			_ = json.NewEncoder(w).Encode(metav1.APIResourceList{TypeMeta: metav1.TypeMeta{Kind: "APIResourceList", APIVersion: "v1"}, GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "configmaps", Kind: "ConfigMap", Namespaced: true, Verbs: metav1.Verbs{"get"}}}})
		default:
			if !strings.HasPrefix(r.URL.Path, "/api/v1/namespaces/team/configmaps/") {
				t.Error("unexpected path")
				w.WriteHeader(400)
				return
			}
			reads.Add(1)
			if mode.Load() == 1 {
				http.Error(w, "PRIVATE", 403)
				return
			}
			name := strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces/team/configmaps/")
			_ = json.NewEncoder(w).Encode(metav1.PartialObjectMetadata{TypeMeta: metav1.TypeMeta{Kind: "PartialObjectMetadata", APIVersion: "meta.k8s.io/v1"}, ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "team", UID: types.UID("live-" + name), ResourceVersion: "rv-a", Labels: map[string]string{"app.kubernetes.io/managed-by": "Helm"}, Annotations: map[string]string{"meta.helm.sh/release-name": "example", "meta.helm.sh/release-namespace": "team", "private": "PRIVATE"}}})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const liveUIManifest = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: sample\n"

func startLiveUI(h *relationshipHarness) {
	h.tt.Frame()
	viewport, _ := h.tt.Find("Helm uninstall inventory")
	h.tt.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, -10000)
	h.tt.Frame()
	// The explicit live controls are in the shared scroll region, not below it.
	for i := 0; i < 12; i++ {
		b, ok := h.tt.Find("Helm live check action")

		consent, cok := h.tt.Find("Helm live consent control")
		if ok && cok && b.H >= 32 && consent.H >= 28 && consent.Y >= viewport.Y && b.Y >= viewport.Y && b.Y+b.H <= viewport.Y+viewport.H {
			break
		}
		h.tt.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, 50)
		h.tt.Frame()
	}
	h.click(helmLiveConsentLabel)
	if !h.w.helmReleases.liveAcknowledged {
		saveNativeScreenshot(h.t, h.tt)
		h.t.Fatal("explicit live consent not committed")
	}
	h.click("Check live metadata")
}
func TestNativeHelmLiveObservationConsentAndExpiry(t *testing.T) {
	var reads, mode atomic.Int32
	srv := liveUIServer(t, liveUIManifest, &reads, &mode)
	h := helmMetadataHarness(t, srv.URL)
	h.tt = ui.NewTester(h.w.View, 1100, 700)
	original := ui.NewTextBuffer("raw\r\n中文\n")
	h.w.editor = original
	h.w.editorObserved = original.Snapshot()
	plan := &operation.Prepared{}
	h.w.plan = plan
	selectInspectionUI(h)
	startUninstallPreviewUI(h)
	h.pump(func() bool { return !h.w.helmReleases.inspecting })
	p := h.w.helmReleases.preview
	h.w.observeHelmPreview()
	if reads.Load() != 0 || h.w.helmReleases.inspecting {
		t.Fatal("unconsented metadata")
	}
	startLiveUI(h)
	h.pump(func() bool { return !h.w.helmReleases.inspecting })
	r := &h.w.helmReleases
	if r.live == nil || r.live.Items()[0].Markers != "match" || r.liveAcknowledged || r.acknowledged || reads.Load() != 1 {
		t.Fatal("observation", r.liveStatus)
	}
	saveNativeScreenshot(t, h.tt)
	h.w.expireHelmUninstall(r.live.ExpiresAt())
	h.tt.Frame()
	if r.live != nil || r.liveAcknowledged || r.preview != p || reads.Load() != 1 {
		t.Fatal("live expiry invalidation")
	}
	startLiveUI(h)
	h.pump(func() bool { return !r.inspecting })
	if r.live == nil || reads.Load() != 2 {
		t.Fatal("explicit recheck", r.liveStatus)
	}
	h.click("Discard inspection")
	if r.live != nil || r.preview != nil || r.liveAcknowledged || h.w.editor != original || original.Snapshot().String() != "raw\r\n中文\n" || h.w.plan != plan || h.w.pendingWrites != 0 {
		t.Fatal("discard mutated draft/plan")
	}
}
func TestNativeHelmLiveObservationStaleResults(t *testing.T) {
	for _, scenario := range []string{"selection", "refresh", "discard", "close", "namespace", "context", "backend", "session", "disconnect", "teardown", "expiry"} {
		t.Run(scenario, func(t *testing.T) {
			var reads, mode atomic.Int32
			srv := liveUIServer(t, liveUIManifest, &reads, &mode)
			h := helmMetadataHarness(t, srv.URL)
			selectInspectionUI(h)
			startUninstallPreviewUI(h)
			h.pump(func() bool { return !h.w.helmReleases.inspecting })
			p := h.w.helmReleases.preview
			startLiveUI(h)
			var completion func()
			select {
			case completion = <-h.updates:
			case <-h.ctx.Done():
				t.Fatal("no completion")
			}
			switch scenario {
			case "selection":
				h.w.selectHelmRevision(h.w.helmReleases.selected)
			case "refresh":
				h.click("Refresh Helm metadata")
			case "discard":
				h.click("Discard inspection")
			case "close":
				h.click("Close Helm metadata")
			case "namespace":
				h.w.namespace = "elsewhere"
			case "context":
				h.w.contextEpoch++
			case "backend":
				h.w.backend = nil
			case "session":
				h.w.sessionID = "other"
			case "disconnect":
				h.click("Disconnect")
			case "teardown":
				h.w.Close()
			case "expiry":
				h.w.expireHelmUninstall(p.ExpiresAt())
			}
			completion()
			h.tt.Frame()
			if h.w.helmReleases.live != nil || h.w.helmReleases.liveAcknowledged {
				t.Fatal("stale result/consent restored")
			}
		})
	}
	// Click queued while currently busy must not revive consent after unlock.
	var reads, mode atomic.Int32
	srv := liveUIServer(t, liveUIManifest, &reads, &mode)
	h := helmMetadataHarness(t, srv.URL)
	selectInspectionUI(h)
	startUninstallPreviewUI(h)
	h.pump(func() bool { return !h.w.helmReleases.inspecting })
	r := &h.w.helmReleases
	r.inspecting = true
	h.click(helmLiveConsentLabel)
	if r.liveAcknowledged {
		t.Fatal("busy consent accepted")
	}
	r.inspecting = false
	h.tt.Frame()
	h.tt.Frame()
	h.w.observeHelmPreview()
	if r.liveAcknowledged || reads.Load() != 0 {
		t.Fatal("consent replayed")
	}
}
func TestNativeHelmLiveObservationFailureClearsResult(t *testing.T) {
	var reads, mode atomic.Int32
	srv := liveUIServer(t, liveUIManifest, &reads, &mode)
	h := helmMetadataHarness(t, srv.URL)
	selectInspectionUI(h)
	startUninstallPreviewUI(h)
	h.pump(func() bool { return !h.w.helmReleases.inspecting })
	startLiveUI(h)
	h.pump(func() bool { return !h.w.helmReleases.inspecting })
	if h.w.helmReleases.live == nil {
		t.Fatal(h.w.helmReleases.liveStatus)
	}
	mode.Store(1)
	startLiveUI(h)
	h.pump(func() bool { return !h.w.helmReleases.inspecting })
	r := &h.w.helmReleases
	if r.live != nil || r.liveAcknowledged || strings.Contains(r.liveStatus, "PRIVATE") || r.preview == nil {
		t.Fatal("failed live result retained or wrong source revoked", r.liveStatus)
	}
}
func TestNativeHelmLiveObservationScaledInventory(t *testing.T) {
	for _, scale := range []float32{1, 1.25, 1.5} {
		for _, rtl := range []bool{false, true} {
			t.Run(fmt.Sprintf("scale-%g-rtl-%t", scale, rtl), func(t *testing.T) {
				var text strings.Builder
				for i := 0; i < 32; i++ {
					fmt.Fprintf(&text, "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: sample-%d\n", i)
				}
				var reads, mode atomic.Int32
				srv := liveUIServer(t, text.String(), &reads, &mode)
				h := helmMetadataHarness(t, srv.URL)
				h.tt = ui.NewTester(h.w.View, 1100, 700)
				if rtl {
					h.tt.SetLocale("ar")
					h.tt.SetDirection(ui.RightToLeft)
				}
				h.tt.SetPreferences(ui.Preferences{TextScale: scale, HighContrast: true, ReduceMotion: true})
				selectInspectionUI(h)
				startUninstallPreviewUI(h)
				h.pump(func() bool { return !h.w.helmReleases.inspecting })
				startLiveUI(h)
				h.pump(func() bool { return !h.w.helmReleases.inspecting })
				if h.w.helmReleases.live == nil {
					t.Fatal(h.w.helmReleases.liveStatus)
				}
				viewport, ok := h.tt.Find("Helm uninstall inventory")
				if !ok || viewport.H < 96 {
					t.Fatal("collapsed viewport", viewport)
				}
				h.tt.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, 10000)
				h.tt.Frame()
				last, ok := h.tt.Find("Helm live object 32")
				if !ok || last.H < 10 || last.Y < viewport.Y || last.Y+last.H > viewport.Y+viewport.H {
					t.Fatal("last live row clipped", last, viewport)
				}
				status, _ := h.tt.Find("Connection status")
				for _, label := range []string{"Uninstall scope preview — NOT executable", "Discard inspection", "Close Helm metadata"} {
					b, ok := h.tt.Find(label)
					if !ok || b.H <= 0 || b.Y+b.H > status.Y {
						t.Fatal("lost control", label, b)
					}
				}
				saveNativeScreenshot(t, h.tt)
				// No additional HTTP on scrolling; all 32 requested only once.
				if reads.Load() != 32 {
					t.Fatal("unexpected reads", reads.Load())
				}
				h.w.expireHelmUninstall(time.Now().Add(3 * time.Minute))
				h.tt.Frame()
				if h.w.helmReleases.live != nil || h.w.helmReleases.preview != nil {
					t.Fatal("source expiry")
				}
			})
		}
	}
}
