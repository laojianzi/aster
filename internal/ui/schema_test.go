package uiworkbench

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/testschema"
)

const schemaDraft = "apiVersion: v1\nkind: Pod\nmetadata:\n  name: related-pod\n  namespace: team\nspec:\n  containers:\n  - name: app\n"

func schemaServer(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/openapi/v3":
		fmt.Fprint(w, testschema.Index)
	case "/openapi/v3/api/v1":
		fmt.Fprint(w, testschema.Document)
	default:
		http.NotFound(w, r)
	}
}
func schemaHarness(t *testing.T, host string) *relationshipHarness {
	t.Helper()
	h := newRelationshipHarness(t, 15*time.Second)
	attachRelationshipBackend(t, h, host)
	h.w.editor = schemaDraft
	h.w.schemaPointer = "/spec/containers/0/image"
	h.tt.Frame()
	h.click("Edit")
	return h
}
func TestNativeSchemaReadOnlyDraftAndTabLifecycle(t *testing.T) {
	var writes, calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" {
			writes.Add(1)
		}
		schemaServer(w, r)
	}))
	defer server.Close()
	h := schemaHarness(t, server.URL)
	h.click("Field help")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "Container image reference") || h.w.editor != schemaDraft {
		t.Fatalf("%s", h.w.schemaStatus)
	}
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaText, "/spec/containers/0/image · required") || h.w.editor != schemaDraft || writes.Load() != 0 {
		t.Fatalf("%s", h.w.schemaText)
	}
	saveNativeScreenshot(t, h.tt)
	h.click("Manifest editor")
	h.tt.Type("\n")
	if h.w.schemaText != "" || h.w.schemaStatus != "" {
		t.Fatal("diagnostics survived draft edit")
	}
	h.click("Field help")
	h.pump(func() bool { return !h.w.schemaActive })
	h.click("YAML")
	if h.w.schemaText != "" || h.w.schemaCancel != nil {
		t.Fatal("schema retained on hidden tab")
	}
	count := calls.Load()
	h.click("Edit")
	h.tt.Frame()
	if calls.Load() != count {
		t.Fatal("schema polled without explicit action")
	}
	h.click("Close detail")
	if h.w.schemaText != "" {
		t.Fatal("schema survived close")
	}
}
func TestNativeSchemaQueuedResultsCannotCrossIdentityOrDraft(t *testing.T) {
	for _, change := range []string{"draft", "detail", "expiry", "pointer", "tab"} {
		t.Run(change, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(schemaServer))
			defer server.Close()
			h := schemaHarness(t, server.URL)
			h.click("Field help")
			var complete func()
			select {
			case complete = <-h.updates:
			case <-h.ctx.Done():
				t.Fatal("no completion")
			}
			switch change {
			case "draft":
				h.click("Manifest editor")
				h.tt.Type("\n")
			case "detail":
				h.click("Close detail")
			case "expiry":
				h.w.expireConnection(h.w.contextEpoch)
				h.tt.Frame()
			case "pointer":
				h.click("Schema field pointer")
				h.tt.Type("x")
			case "tab":
				h.click("YAML")
			}
			complete()
			h.tt.Frame()
			if h.w.schemaText != "" || h.w.schemaActive {
				t.Fatal("stale schema result leaked")
			}
		})
	}
}
func TestNativeSchemaTabChangeCancelsRequest(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(canceled) }))
	defer server.Close()
	h := schemaHarness(t, server.URL)
	h.click("Field help")
	select {
	case <-started:
	case <-h.ctx.Done():
		t.Fatal("request not started")
	}
	h.click("YAML")
	select {
	case <-canceled:
	case <-h.ctx.Done():
		t.Fatal("hidden schema did not cancel")
	}
}
func TestNativeSchemaInvalidDraftDoesNotSendOrEchoContent(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); schemaServer(w, r) }))
	defer server.Close()
	h := schemaHarness(t, server.URL)
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("SECRET INVALID CONTENT")
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	if calls.Load() != 0 || h.w.schemaStatus == "" || strings.Contains(h.w.schemaStatus, "SECRET") {
		t.Fatal("invalid draft sent or echoed")
	}
}
func TestNativeSchemaControlsAtMinimumWindow(t *testing.T) {
	w := New()
	w.detail = relatedPod()
	w.detailKind = catalog()[0]
	w.detailMode = "Edit"
	w.editor = schemaDraft
	w.schemaPointer = "/spec/containers/0/image"
	w.schemaStatus = "Read-only hints · not server validation"
	w.schemaText = "/spec/containers/0/image · required: Required field is absent.\nThe draft was not changed. Server dry-run remains required."
	tt := ui.NewTester(w.View, 1100, 700)
	for _, label := range []string{"Manifest editor", "Schema field pointer", "Field help", "Check draft", "Clear schema", "Schema assistance", "Preview edit", "Apply", "Close detail", "Preview delete"} {
		r, ok := tt.Find(label)
		if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 1088 || r.Y+r.H > 700 {
			t.Errorf("%s out of bounds: %+v found=%v", label, r, ok)
		}
	}
	saveNativeScreenshot(t, tt)
}

func TestNativeSchemaForbiddenRemainsPermissionError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "PRIVATE", http.StatusForbidden) }))
	defer server.Close()
	h := schemaHarness(t, server.URL)
	h.click("Field help")
	h.pump(func() bool { return !h.w.schemaActive })
	if !strings.Contains(h.w.schemaStatus, "access denied") || strings.Contains(h.w.schemaStatus, "PRIVATE") {
		t.Fatal(h.w.schemaStatus)
	}
}
