package uiworkbench

import (
	"encoding/json"
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

const helmConsentLabel = "Allow reading this revision's sensitive Secret content"

func selectInspectionUI(h *relationshipHarness) {
	h.click("Helm releases")
	h.click("Refresh Helm metadata")
	h.pump(func() bool { return !h.w.helmReleases.busy })
	h.click("Select sh.helm.release.v1.example.v2")
}
func startInspectionUI(h *relationshipHarness) {
	h.click(helmConsentLabel)
	h.click("Inspect selected revision")
}

func TestNativeHelmInspectionExplicitConsentAndPrivateSummary(t *testing.T) {
	var reads, bad atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			bad.Add(1)
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case "/api/v1/namespaces/team/secrets":
			if r.Header.Get("Accept") != helmrelease.Accept {
				bad.Add(1)
			}
			writeHelmMetadata(w)
		case "/api/v1/namespaces/team/secrets/sh.helm.release.v1.example.v2":
			reads.Add(1)
			_ = json.NewEncoder(w).Encode(helmfixture.Secret("team", "example", "deployed", 2))
		default:
			bad.Add(1)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	h := helmMetadataHarness(t, server.URL)
	h.tt = ui.NewTester(h.w.View, 1100, 700)
	original := ui.NewTextBuffer("raw\r\n中文 🚢\n")
	h.w.editor = original
	h.w.editorObserved = original.Snapshot()
	plan := &operation.Prepared{}
	h.w.plan = plan
	delivery := ui.NewTextBuffer("original delivery")
	h.w.delivery.editor = delivery
	h.w.delivery.observed = delivery.Snapshot()
	selectInspectionUI(h)
	h.w.inspectHelmRevision()
	h.tt.Frame()
	if reads.Load() != 0 || h.w.helmReleases.inspecting {
		t.Fatal("selection or missing acknowledgement fetched content")
	}
	startInspectionUI(h)
	h.pump(func() bool { return !h.w.helmReleases.inspecting })
	r := &h.w.helmReleases
	if r.inspection == nil || r.inspection.ChartName != "example-chart" || reads.Load() != 1 || bad.Load() != 0 || r.acknowledged {
		t.Fatal("inspection flow", r.contentStatus)
	}
	projected, _ := json.Marshal(r.inspection)
	if strings.Contains(string(projected), "PRIVATE") || strings.Contains(r.contentStatus, "PRIVATE") {
		t.Fatal("private payload entered UI")
	}
	for _, label := range []string{helmConsentLabel, "Inspect selected revision", "Stop content inspection", "Discard inspection", "Helm inspection chart", "Helm inspection summary", "Close Helm metadata"} {
		bounds, ok := h.tt.Find(label)
		if !ok || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > 1100 || bounds.Y+bounds.H > 700 {
			t.Fatalf("minimum-window control %s %+v", label, bounds)
		}
	}
	saveNativeScreenshot(t, h.tt)
	h.click("Discard inspection")
	if r.inspection != nil || r.selected.StorageName != "" || r.acknowledged || h.w.editor != original || original.Snapshot().String() != "raw\r\n中文 🚢\n" || h.w.delivery.editor != delivery || h.w.plan != plan || h.w.pendingWrites != 0 {
		t.Fatal("inspection changed draft/approval or retained content")
	}
}
func TestNativeHelmInspectionStaleCompletionAndTeardown(t *testing.T) {
	for _, scenario := range []string{"selection", "refresh", "discard", "close", "namespace", "context", "backend", "session", "disconnect", "teardown"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/secrets") {
					writeHelmMetadata(w)
					return
				}
				_ = json.NewEncoder(w).Encode(helmfixture.Secret("team", "example", "deployed", 2))
			}))
			defer server.Close()
			h := helmMetadataHarness(t, server.URL)
			selectInspectionUI(h)
			startInspectionUI(h)
			var completion func()
			select {
			case completion = <-h.updates:
			case <-h.ctx.Done():
				t.Fatal("missing content completion")
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
			if h.w.helmReleases.inspection != nil || h.w.helmReleases.acknowledged || h.w.helmReleases.inspecting {
				t.Fatal("queued content resurrected stale scope")
			}
		})
	}
}
func TestNativeHelmInspectionFailureCancellationAndStaleObject(t *testing.T) {
	var mode atomic.Int32
	started, cancelled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			close(cancelled)
			return
		}
		s := helmfixture.Secret("team", "example", "deployed", 2)
		if mode.Load() == 3 {
			s.ResourceVersion = "changed"
		}
		_ = json.NewEncoder(w).Encode(s)
	}))
	defer server.Close()
	h := helmMetadataHarness(t, server.URL)
	selectInspectionUI(h)
	startInspectionUI(h)
	h.pump(func() bool { return !h.w.helmReleases.inspecting })
	if h.w.helmReleases.inspection == nil {
		t.Fatal("initial summary missing")
	}
	mode.Store(1)
	startInspectionUI(h)
	h.pump(func() bool { return !h.w.helmReleases.inspecting })
	if h.w.helmReleases.inspection != nil || h.w.helmReleases.acknowledged || h.w.helmReleases.contentStatus != helmrelease.ErrInspectionForbidden.Error() {
		t.Fatal("error retained summary/consent or raw text")
	}
	mode.Store(2)
	startInspectionUI(h)
	select {
	case <-started:
	case <-h.ctx.Done():
		t.Fatal("read not started")
	}
	h.click("Stop content inspection")
	select {
	case <-cancelled:
	case <-h.ctx.Done():
		t.Fatal("transport not cancelled")
	}
	if h.w.helmReleases.inspection != nil || h.w.helmReleases.selected.StorageName != "" {
		t.Fatal("stop retained state")
	}
	mode.Store(3)
	h.click("Select sh.helm.release.v1.example.v2")
	startInspectionUI(h)
	h.pump(func() bool { return !h.w.helmReleases.inspecting })
	if h.w.helmReleases.inspection != nil || h.w.helmReleases.snapshot.ResourceVersion != "" || h.w.helmReleases.status != helmrelease.ErrInspectionStale.Error() {
		t.Fatal("stale object allowed old selection to be reused")
	}
}
