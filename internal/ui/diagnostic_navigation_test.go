package uiworkbench

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNativeDiagnosticNavigationKeepsSession(t *testing.T) {
	for _, mode := range []string{"Logs", "Terminal", "Command", "Forward"} {
		t.Run(mode, func(t *testing.T) {
			next := modelObject("next-uid", "next-pod")
			next.Object["spec"] = map[string]interface{}{"containers": []interface{}{map[string]interface{}{"name": "next", "image": "fixture"}}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/namespaces/team/pods/next-pod" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
					http.Error(w, "unexpected", 400)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(next)
			}))
			defer server.Close()
			h := newRelationshipHarness(t, 10*time.Second)
			attachRelationshipBackend(t, h, server.URL)
			h.w.detailMode = mode
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "Logs":
				h.w.logCancel = cancel
			case "Terminal":
				h.w.terminalCancel, h.w.terminalActive = cancel, true
			case "Command":
				h.w.commandCancel, h.w.commandActive = cancel, true
			case "Forward":
				h.w.forwardCancel, h.w.forwardActive = cancel, true
			}
			h.tt.Frame()
			h.click("Expand diagnostic")
			h.w.openResourceKind(catalog()[0], project(next))
			h.pump(func() bool { return h.w.detail != nil })
			if h.w.detail.GetUID() != "next-uid" {
				t.Fatal("navigation did not open the verified new object")
			}
			if h.w.diagnostic == nil || h.w.diagnostic.target.UID != "pod-uid" {
				t.Fatal("navigation retargeted diagnostic identity")
			}
			if _, ok := h.tt.Find("Diagnostic target"); !ok {
				t.Fatal("retained diagnostic has no visible source")
			}
			if _, ok := h.tt.Find("Restore split"); !ok {
				t.Fatal("navigation discarded expanded diagnostic state")
			}
			if ctx.Err() != nil {
				t.Fatal("opening another object canceled the existing diagnostic session")
			}
			editor := h.w.editor
			closeLabel := "Close diagnostic"
			if mode == "Logs" {
				closeLabel = "Close logs"
			}
			h.click(closeLabel)
			if ctx.Err() == nil || h.w.diagnostic != nil || h.w.detail.GetUID() != "next-uid" || h.w.editor != editor {
				t.Fatal("closing old diagnostic lost the current object or retained session")
			}
			h.click("Diagnostics")
			if err := h.tt.Click("Logs"); err != nil {
				t.Fatal(err)
			}
			if h.w.diagnostic.target.UID != "next-uid" || h.w.container != "next" {
				t.Fatal("new diagnostic did not select its own Pod/container")
			}

		})
	}
}

func TestNativeDiagnosticLogsContinueAfterObjectNavigation(t *testing.T) {
	original, next := modelObject("pod-uid", "pod"), modelObject("next-uid", "next-pod")
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/namespaces/team/pods/pod":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(original)
		case "/api/v1/namespaces/team/pods/next-pod":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(next)
		case "/api/v1/namespaces/team/pods/pod/log":
			if r.URL.Query().Get("container") != "app" {
				t.Error("log container retargeted")
			}
			fmt.Fprintln(w, "before navigation")
			w.(http.Flusher).Flush()
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			fmt.Fprintln(w, "after navigation")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			t.Errorf("unexpected request %s", r.URL)
			http.Error(w, "unexpected", 400)
		}
	}))
	t.Cleanup(server.Close)
	h := newRelationshipHarness(t, 10*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.w.detail = original
	h.w.containers, h.w.container = []string{"app"}, "app"
	h.click("Diagnostics")
	if err := h.tt.Click("Logs"); err != nil {
		t.Fatal(err)
	}
	h.click("Follow logs")
	h.pump(func() bool { return len(h.w.logRows) == 1 })
	h.w.openResourceKind(catalog()[0], project(next))
	h.pump(func() bool { return h.w.detail != nil })
	close(release)
	h.pump(func() bool { return len(h.w.logRows) == 2 })
	if h.w.logRows[1] != "after navigation" || h.w.diagnostic.target.UID != "pod-uid" {
		t.Fatal("late log output lost its original target")
	}
	saveNativeScreenshot(t, h.tt)
	h.click("Close logs")
	if h.w.logCancel != nil || h.w.detail.GetUID() != "next-uid" {
		t.Fatal("close changed current object or retained stream")
	}
}

func TestNativeDiagnosticResourceKindKeepsSessionButNamespaceCloses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || (r.URL.Path != "/api/v1/namespaces/team/configmaps" && r.URL.Path != "/api/v1/namespaces/other/configmaps") {
			t.Errorf("unexpected scope request %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("watch") == "true" {
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		fmt.Fprint(w, `{"apiVersion":"v1","kind":"ConfigMapList","metadata":{"resourceVersion":"1"},"items":[]}`)
	}))
	t.Cleanup(server.Close)
	h := newRelationshipHarness(t, 10*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.w.namespace = "team"
	h.w.detailMode = "Logs"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.w.logCancel = cancel
	h.tt.Frame()
	h.selectSidebarResource("ConfigMaps")
	h.pump(func() bool { return h.w.status == "Live" })
	if ctx.Err() != nil || h.w.diagnostic == nil || h.w.detail != nil {
		t.Fatal("resource kind switch canceled diagnostics or kept old detail")
	}
	if _, ok := h.tt.Find("Diagnostic target"); !ok {
		t.Fatal("resource list hid active diagnostics")
	}
	saveNativeScreenshot(t, h.tt)
	h.w.namespace = "other"
	h.w.startScope()
	if ctx.Err() == nil || h.w.diagnostic != nil || h.w.logCancel != nil {
		t.Fatal("namespace switch retained diagnostic session")
	}
}
