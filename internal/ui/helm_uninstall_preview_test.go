package uiworkbench

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/helmfixture"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/operation"
)

func startUninstallPreviewUI(h *relationshipHarness) {
	h.click(helmConsentLabel)
	h.click("Preview uninstall scope")
}
func previewServer(t *testing.T, manifest string, reads *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("non-read request")
			w.WriteHeader(400)
			return
		}
		if r.URL.Path == "/api/v1/namespaces/team/secrets" {
			writeHelmMetadata(w)
			return
		}
		if r.URL.Path != "/api/v1/namespaces/team/secrets/sh.helm.release.v1.example.v2" {
			t.Error("unexpected read path")
			w.WriteHeader(400)
			return
		}
		reads.Add(1)
		_ = json.NewEncoder(w).Encode(helmfixture.WithManifest("team", "example", "deployed", 2, manifest))
	}))
	t.Cleanup(srv.Close)
	return srv
}
func TestNativeHelmUninstallPreviewConsentAndExpiry(t *testing.T) {
	var reads atomic.Int32
	srv := previewServer(t, helmfixture.PreviewManifest("team"), &reads)
	h := helmMetadataHarness(t, srv.URL)
	h.tt = ui.NewTester(h.w.View, 1100, 700)
	original := ui.NewTextBuffer("raw\r\n中文 🚢\n")
	h.w.editor = original
	h.w.editorObserved = original.Snapshot()
	prepared := &operation.Prepared{}
	h.w.plan = prepared
	delivery := ui.NewTextBuffer("delivery\r\n")
	h.w.delivery.editor = delivery
	h.w.delivery.observed = delivery.Snapshot()
	selectInspectionUI(h)
	h.w.previewHelmUninstall()
	if reads.Load() != 0 || h.w.helmReleases.inspecting {
		t.Fatal("unconsented read")
	}
	startUninstallPreviewUI(h)
	h.pump(func() bool { return !h.w.helmReleases.inspecting })
	r := &h.w.helmReleases
	p := r.preview
	if p == nil || len(p.Items()) != 2 || r.inspection != nil || r.acknowledged || reads.Load() != 1 {
		t.Fatal("preview flow", r.contentStatus)
	}
	if !h.tt.HasText("Uninstall scope preview — NOT executable") || strings.Contains(r.contentStatus, "PRIVATE") {
		t.Fatal("missing trust warning")
	}
	saveNativeScreenshot(t, h.tt)
	h.w.expireHelmUninstall(p.ExpiresAt())
	h.tt.Frame()
	if r.preview != nil || r.acknowledged || reads.Load() != 1 {
		t.Fatal("expiry retained preview or refreshed network")
	}
	h.w.previewHelmUninstall()
	if reads.Load() != 1 {
		t.Fatal("expiry restored consent")
	}
	startUninstallPreviewUI(h)
	h.pump(func() bool { return !h.w.helmReleases.inspecting })
	if r.preview == nil || r.preview.ID() == p.ID() {
		t.Fatal("explicit reread not new identity")
	}
	h.click("Discard inspection")
	if r.preview != nil || r.selected.StorageName != "" || r.acknowledged || h.w.editor != original || original.Snapshot().String() != "raw\r\n中文 🚢\n" || h.w.plan != prepared || h.w.delivery.editor != delivery || h.w.pendingWrites != 0 {
		t.Fatal("preview modified existing editor/plan or retained state")
	}
}

func TestNativeHelmUninstallPreviewStaleCompletionAndBusyConsent(t *testing.T) {
	for _, scenario := range []string{"selection", "refresh", "discard", "close", "namespace", "context", "backend", "session", "disconnect", "teardown"} {
		t.Run(scenario, func(t *testing.T) {
			var reads atomic.Int32
			srv := previewServer(t, helmfixture.PreviewManifest("team"), &reads)
			h := helmMetadataHarness(t, srv.URL)
			selectInspectionUI(h)
			startUninstallPreviewUI(h)
			var completion func()
			select {
			case completion = <-h.updates:
			case <-h.ctx.Done():
				t.Fatal("no completion")
			}
			switch scenario {
			case "selection":
				h.click("Select sh.helm.release.v1.example.v1")
			case "refresh":
				h.click("Refresh Helm metadata")
			case "discard":
				h.click("Discard inspection")
			case "close":
				h.click("Close Helm metadata")
			case "namespace":
				h.w.namespace = "other"
				h.tt.Frame()
			case "context":
				h.w.contextEpoch++
				h.tt.Frame()
			case "backend":
				h.w.backend = nil
				h.tt.Frame()
			case "session":
				h.w.sessionID = "other"
				h.tt.Frame()
			case "disconnect":
				h.click("Disconnect")
			case "teardown":
				h.w.Close()
			}
			completion()
			h.tt.Frame()
			if h.w.helmReleases.preview != nil || h.w.helmReleases.acknowledged {
				t.Fatal("stale callback resurrected preview/consent")
			}
		})
	}
	// The last painted checkbox is enabled; current business state becomes busy
	// before queued input is consumed. This is the actual constructor-time race.
	var reads atomic.Int32
	srv := previewServer(t, helmfixture.PreviewManifest("team"), &reads)
	h := helmMetadataHarness(t, srv.URL)
	selectInspectionUI(h)
	h.w.helmReleases.inspecting = true
	h.click(helmConsentLabel)
	if h.w.helmReleases.acknowledged {
		t.Fatal("busy input restored consent")
	}
	h.w.helmReleases.inspecting = false
	h.tt.Frame()
	h.tt.Frame()
	if h.w.helmReleases.acknowledged || reads.Load() != 0 {
		t.Fatal("unlock replayed consent")
	}
}

func TestNativeHelmUninstallPreviewFailuresAndCancellation(t *testing.T) {
	var mode atomic.Int32
	started, stopped := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/secrets") {
			writeHelmMetadata(w)
			return
		}
		switch mode.Load() {
		case 1:
			http.Error(w, "PRIVATE_ERROR", 403)
			return
		case 2:
			close(started)
			<-r.Context().Done()
			close(stopped)
			return
		}
		s := helmfixture.WithManifest("team", "example", "deployed", 2, helmfixture.PreviewManifest("team"))
		if mode.Load() == 3 {
			s.ResourceVersion = "changed"
		}
		if mode.Load() == 4 {
			s = helmfixture.WithManifest("team", "example", "deployed", 2, "PRIVATE_BAD: [")
		}
		_ = json.NewEncoder(w).Encode(s)
	}))
	defer srv.Close()
	h := helmMetadataHarness(t, srv.URL)
	selectInspectionUI(h)
	startUninstallPreviewUI(h)
	h.pump(func() bool { return !h.w.helmReleases.inspecting })
	if h.w.helmReleases.preview == nil {
		t.Fatal("missing initial preview")
	}
	for _, value := range []int32{1, 4} {
		mode.Store(value)
		startUninstallPreviewUI(h)
		h.pump(func() bool { return !h.w.helmReleases.inspecting })
		r := &h.w.helmReleases
		if r.preview != nil || r.acknowledged || strings.Contains(r.contentStatus, "PRIVATE") {
			t.Fatal("failure retained preview/consent or leaked")
		}
	}
	mode.Store(2)
	startUninstallPreviewUI(h)
	select {
	case <-started:
	case <-h.ctx.Done():
		t.Fatal("not started")
	}
	h.click("Stop content inspection")
	select {
	case <-stopped:
	case <-h.ctx.Done():
		t.Fatal("not cancelled")
	}
	if h.w.helmReleases.preview != nil || h.w.helmReleases.selected.StorageName != "" {
		t.Fatal("cancel retained state")
	}
	mode.Store(3)
	h.click("Select sh.helm.release.v1.example.v2")
	startUninstallPreviewUI(h)
	h.pump(func() bool { return !h.w.helmReleases.inspecting })
	if h.w.helmReleases.preview != nil || h.w.helmReleases.snapshot.ResourceVersion != "" || h.w.helmReleases.status != helmrelease.ErrInspectionStale.Error() {
		t.Fatal("stale object not invalidated")
	}
}

func TestNativeHelmUninstallPreviewScaledInventory(t *testing.T) {
	for _, scale := range []float32{1, 1.25, 1.5} {
		for _, rtl := range []bool{false, true} {
			t.Run(fmt.Sprintf("scale-%g-rtl-%t", scale, rtl), func(t *testing.T) {
				var text strings.Builder
				for i := 0; i < helmrelease.MaxPreviewObjects; i++ {
					fmt.Fprintf(&text, "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: preview-%d\n  namespace: team\n", i)
				}
				var reads atomic.Int32
				srv := previewServer(t, text.String(), &reads)
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
				if h.w.helmReleases.preview == nil {
					t.Fatal(h.w.helmReleases.contentStatus)
				}
				status, _ := h.tt.Find("Connection status")
				for _, label := range []string{"Preview uninstall scope", "Discard inspection", "Close Helm metadata", "Uninstall scope preview — NOT executable", "Helm uninstall expiry", helmConsentLabel} {
					b, ok := h.tt.Find(label)
					if !ok || b.X < 0 || b.Y < 0 || b.W <= 0 || b.H <= 0 || b.X+b.W > 1100 || b.Y+b.H > status.Y {
						t.Fatalf("clipped %s %+v status=%+v", label, b, status)
					}
				}
				viewport, ok := h.tt.Find("Helm uninstall inventory")
				if !ok || viewport.H <= 0 || viewport.Y+viewport.H > status.Y {
					t.Fatal("invalid viewport", viewport)
				}
				h.tt.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, 4000)
				h.tt.Frame()
				last, ok := h.tt.Find("Helm uninstall object 32")
				if !ok || last.Y < viewport.Y || last.Y+last.H > viewport.Y+viewport.H {
					t.Fatal("last stored object unreachable", last, viewport)
				}
				saveNativeScreenshot(t, h.tt)
				if reads.Load() != 1 {
					t.Fatal("scrolling performed extra read")
				}
			})
		}
	}
}
