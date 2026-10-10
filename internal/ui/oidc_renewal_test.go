package uiworkbench

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/testoidc"
	"github.com/laojianzi/aster/internal/testvault"
	"k8s.io/client-go/rest"
)

func renewalAPIServer(t *testing.T) (*rest.Config, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
			http.Error(w, "denied", 403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("watch") == "true" {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		if r.URL.Path == "/api/v1/namespaces/team/pods" {
			_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "1"}, "items": []any{modelObject("renewal-pod", "renewal-pod")}})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(func() { server.CloseClientConnections(); server.Close() })
	return &rest.Config{Host: server.URL, TLSClientConfig: rest.TLSClientConfig{CAData: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})}}, calls
}
func loginRenewable(t *testing.T, h *relationshipHarness, p *testoidc.Provider, cfg *rest.Config) {
	t.Helper()
	p.AllowRenewal = true
	setupOIDC(t, h, p, cfg)
	// Changed opt-in must invalidate the old review; only a fresh review may request it.
	h.click("Allow memory-only renewal (requests offline access and consent)")
	if h.w.oidcReview != nil || !h.w.oidcAllowRenewal {
		t.Fatal("opt-in did not invalidate review")
	}
	h.click("Review OIDC target")
	h.pump(func() bool { return !h.w.oidcPending })
	if h.w.oidcReview == nil {
		t.Fatal(h.w.oidcStatus)
	}
	h.click("Confirm OIDC context")
	h.tt.Type("vault-test")
	h.click("Sign in with browser")
	h.pump(func() bool { return !h.w.oidcPending })
	if h.w.oidcIdentity == nil || h.w.oidcRenewal != nil {
		t.Fatal("missing verified ID or capability before resolve", h.w.oidcStatus)
	}
	h.click("Confirm OIDC context")
	h.tt.Type("vault-test")
	h.click("Connect verified identity")
	h.pump(func() bool { return h.w.status == "Live" })
	if h.w.oidcRenewal == nil {
		t.Fatal("capability not transferred to exact connection")
	}
}
func TestNativeOIDCRenewalReviewReplacementAndMinimumWindow(t *testing.T) {
	p := testoidc.New(t)
	cfg, calls := renewalAPIServer(t)
	h := newRelationshipHarness(t, 15*time.Second)
	loginRenewable(t, h, p, cfg)
	before, _ := os.ReadFile(h.w.path)
	old, oldCtx, oldID := h.w.backend, h.w.connectionCtx, h.w.sessionID
	h.click("Browser sign-in")
	h.click("Renew identity once")
	h.pump(func() bool { return !h.w.oidcPending })
	if h.w.oidcIdentity == nil || h.w.backend != old || oldCtx.Err() != nil || h.w.oidcRenewal != nil || h.w.oidcConfirmation != "" {
		t.Fatal("implicit replacement or failed renewal", h.w.oidcStatus)
	}
	h.tt.SetSize(1100, 700)
	h.tt.Frame()
	for _, label := range []string{"OIDC renewal target", "OIDC verified identity", "Confirm OIDC replacement context", "Replace verified connection", "Forget renewal", "OIDC renewal status"} {
		b, ok := h.tt.Find(label)
		if !ok || b.H < 8 || b.X < 0 || b.X+b.W > 1100 || b.Y+b.H > 700 {
			t.Fatal("renewal control clipped", label, b)
		}
	}
	h.click("Replace verified connection")
	if h.w.backend != old || h.w.connectionPending || oldCtx.Err() != nil {
		t.Fatal("replacement lacked exact-context review")
	}
	saveNativeScreenshot(t, h.tt)
	h.click("Confirm OIDC replacement context")
	h.tt.Type("vault-test")
	h.click("Replace verified connection")
	h.pump(func() bool { return !h.w.connectionPending && h.w.status == "Live" })
	if h.w.backend == old || oldCtx.Err() == nil || h.w.sessionID == oldID || h.w.oidcRenewal == nil {
		t.Fatal("old session survived/new rotation lost")
	}
	client, e := rest.HTTPClientFor(old.Config())
	if e != nil {
		t.Fatal(e)
	}
	n := calls.Load()
	_, e = client.Get(cfg.Host)
	// Any concurrent discovery may increment calls; the closed guard is checked
	// by its error here and by the dedicated protocol guard tests.
	if e == nil {
		t.Fatal("old config made a request", n)
	}
	after, _ := os.ReadFile(h.w.path)
	if string(after) != string(before) {
		t.Fatal("renewal rewrote kubeconfig")
	}
	h.click("Browser sign-in")
	h.click("Forget renewal")
	if h.w.backend == nil || h.w.oidcRenewal != nil || h.w.oidcIdentity != nil {
		t.Fatal("forget changed connection or retained capability")
	}
}
func TestNativeOIDCRenewalCancellationDisconnectAndWindowIsolation(t *testing.T) {
	for _, action := range []string{"forget", "disconnect", "close"} {
		t.Run(action, func(t *testing.T) {
			p := testoidc.New(t)
			p.RefreshStarted = make(chan struct{}, 1)
			release := make(chan struct{})
			p.RefreshRelease = release
			cfg, _ := renewalAPIServer(t)
			h := newRelationshipHarness(t, 12*time.Second)
			loginRenewable(t, h, p, cfg)
			other := New()
			if other.oidcRenewal != nil {
				t.Fatal("new workspace inherited renewal")
			}
			oldCtx := h.w.connectionCtx
			h.click("Browser sign-in")
			h.click("Renew identity once")
			select {
			case <-p.RefreshStarted:
			case <-h.ctx.Done():
				t.Fatal("refresh not started")
			}
			switch action {
			case "forget":
				h.click("Forget renewal")
			case "disconnect":
				h.click("Disconnect")
			case "close":
				h.w.Close()
			}
			close(release)
			if action != "close" {
				h.pump(func() bool { return !h.w.oidcPending })
			}
			if h.w.oidcIdentity != nil || h.w.oidcRenewal != nil {
				t.Fatal("late refresh published private identity")
			}
			if action == "forget" && (oldCtx.Err() != nil || h.w.backend == nil) {
				t.Fatal("forget altered active session")
			}
			if action != "forget" && oldCtx.Err() == nil {
				t.Fatal("old session not canceled")
			}
			if p.RefreshRequests.Load() != 1 {
				t.Fatal("replayed refresh")
			}
		})
	}
}
func TestNativeOIDCRenewalChangedTargetFailsBeforePost(t *testing.T) {
	p := testoidc.New(t)
	cfg, _ := renewalAPIServer(t)
	h := newRelationshipHarness(t, 12*time.Second)
	loginRenewable(t, h, p, cfg)
	old, oldCtx := h.w.backend, h.w.connectionCtx
	file := testvault.ConfigFile(t, &rest.Config{Host: "https://retarget.invalid"}, "team")
	raw, _ := os.ReadFile(file)
	if e := os.WriteFile(h.w.path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	h.click("Browser sign-in")
	h.click("Renew identity once")
	h.pump(func() bool { return !h.w.oidcPending })
	if p.RefreshRequests.Load() != 0 || h.w.oidcIdentity != nil || h.w.oidcRenewal != nil || h.w.backend != old || oldCtx.Err() != nil {
		t.Fatal("changed target reached issuer/connection")
	}
}
func TestNativeOIDCRenewalExpiryClearsPendingAndCapability(t *testing.T) {
	p := testoidc.New(t)
	p.EditClaims = func(c map[string]any) { c["exp"] = time.Now().Add(5 * time.Second).Unix() }
	cfg, _ := renewalAPIServer(t)
	h := newRelationshipHarness(t, 12*time.Second)
	loginRenewable(t, h, p, cfg)
	cap := h.w.oidcRenewal
	h.pump(func() bool { return h.w.status == "Credentials expired" })
	if h.w.backend != nil || h.w.oidcIdentity != nil || h.w.oidcRenewal != nil {
		t.Fatal("expiry retained renewal")
	}
	// Closing the window/connection actually closes the capability, not merely its UI pointer.
	if _, e := cap.Renew(h.ctx, cfg, "vault-test", "vault-user"); e == nil || p.RefreshRequests.Load() != 0 {
		t.Fatal("expired capability remained usable")
	}
}
func TestNativeOIDCRenewalRetargetBeforeConfirmationCannotConnect(t *testing.T) {
	p := testoidc.New(t)
	cfg, _ := renewalAPIServer(t)
	h := newRelationshipHarness(t, 12*time.Second)
	loginRenewable(t, h, p, cfg)
	h.click("Browser sign-in")
	h.click("Renew identity once")
	h.pump(func() bool { return !h.w.oidcPending })
	if h.w.oidcIdentity == nil {
		t.Fatal(h.w.oidcStatus)
	}
	other := testvault.ConfigFile(t, &rest.Config{Host: "https://changed.invalid"}, "team")
	b, _ := os.ReadFile(other)
	if e := os.WriteFile(h.w.path, b, 0600); e != nil {
		t.Fatal(e)
	}
	h.click("Confirm OIDC replacement context")
	h.tt.Type("vault-test")
	h.click("Replace verified connection")
	h.pump(func() bool { return !h.w.connectionPending })
	if h.w.backend != nil || h.w.oidcRenewal != nil || h.w.status != "Connection failed" {
		t.Fatal("reviewed identity retargeted")
	}
}

// Keep an explicit input event test for stable MyGo constructor identity.
func TestNativeOIDCRenewalConfirmationSurvivesUnrelatedFrame(t *testing.T) {
	p := testoidc.New(t)
	cfg, _ := renewalAPIServer(t)
	h := newRelationshipHarness(t, 12*time.Second)
	loginRenewable(t, h, p, cfg)
	h.click("Browser sign-in")
	h.click("Renew identity once")
	h.pump(func() bool { return !h.w.oidcPending })
	h.click("Confirm OIDC replacement context")
	h.tt.Type("vault-")
	h.w.oidcStatus = "Unrelated status change"
	h.tt.Frame()
	h.tt.Type("test")
	if h.w.oidcConfirmation != "vault-test" {
		t.Fatal("confirmation widget identity changed")
	}
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("wrong")
	h.click("Replace verified connection")
	if h.w.connectionPending {
		t.Fatal("wrong confirmation replaced connection")
	}
	// A new operation epoch must reset both the text and the native undo history.
	h.w.oidcEpoch++
	h.w.oidcConfirmation = ""
	h.tt.Frame()
	h.click("Confirm OIDC replacement context")
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.oidcConfirmation != "" {
		t.Fatal("confirmation history crossed operation epochs")
	}
}
