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

func setupOIDC(t *testing.T, h *relationshipHarness, p *testoidc.Provider, cfg *rest.Config) {
	t.Helper()
	h.w.path = testvault.ConfigFile(t, cfg, "team")
	h.w.currentContext = "vault-test"
	h.w.contexts = []string{"vault-test"}
	h.w.oidcIssuer = p.Server.URL
	h.w.oidcClient = "aster-test"
	h.w.oidcCA = string(p.CA)
	h.w.oidcBrowser = p.Open
	h.tt.Frame()
	h.click("Connections")
	h.click("Browser sign-in")
	h.click("Review OIDC target")
	h.pump(func() bool { return !h.w.oidcPending })
	if h.w.oidcReview == nil {
		t.Fatal(h.w.oidcStatus)
	}
}
func TestNativeOIDCReviewConfirmationAndMinimumWindow(t *testing.T) {
	p := testoidc.New(t)
	h := newRelationshipHarness(t, 8*time.Second)
	setupOIDC(t, h, p, &rest.Config{Host: "https://cluster.invalid"})
	h.tt.SetSize(1100, 700)
	h.tt.Frame()
	for _, label := range []string{"OIDC issuer", "OIDC client ID", "OIDC callback port", "OIDC issuer CA PEM", "Review OIDC target", "OIDC reviewed target", "OIDC token and key endpoints", "Confirm OIDC context", "Sign in with browser", "Connect verified identity", "OIDC sign-in status"} {
		b, ok := h.tt.Find(label)
		if !ok || b.H < 8 || b.X < 0 || b.X+b.W > 1100 || b.Y+b.H > 700 {
			t.Fatal("OIDC control clipped", label, b)
		}
	}
	if !h.tt.HasText("Confirm context") {
		t.Error("missing persistent confirmation label")
	}
	for _, label := range []string{"OIDC issuer", "OIDC client ID", "Confirm OIDC context"} {
		if b, ok := h.tt.Find(label); !ok || b.W > 760 {
			t.Errorf("field exceeds readable form width: %s %+v", label, b)
		}
	}
	h.w.loginOIDC(ui.Services{})
	if h.w.oidcPending || p.TokenRequests.Load() != 0 {
		t.Fatal("unconfirmed browser dispatch")
	}
	h.click("Confirm OIDC context")
	h.tt.Type("vault-test")
	h.click("Sign in with browser")
	h.pump(func() bool { return !h.w.oidcPending })
	if h.w.oidcIdentity == nil || h.w.backend != nil || h.w.oidcConfirmation != "" {
		t.Fatal("verification implicitly connected or failed", h.w.oidcStatus)
	}
	if _, ok := h.tt.Find("OIDC verified identity"); !ok {
		t.Fatal("missing identity review")
	}
	h.click("Connect verified identity")
	if h.w.connectionPending {
		t.Fatal("connection without second confirmation")
	}
	saveNativeScreenshot(t, h.tt)
}
func TestNativeOIDCConnectExpiryAndKubeconfigUnchanged(t *testing.T) {
	p := testoidc.New(t)
	p.EditClaims = func(c map[string]any) { c["exp"] = time.Now().Add(5 * time.Second).Unix() }
	var requests atomic.Int32
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
			http.Error(w, "denied", 401)
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
			json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "1"}, "items": []any{modelObject("oidc-pod", "oidc-pod")}})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(func() { api.CloseClientConnections(); api.Close() })
	cfg := &rest.Config{Host: api.URL, TLSClientConfig: rest.TLSClientConfig{CAData: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: api.Certificate().Raw})}}
	h := newRelationshipHarness(t, 12*time.Second)
	setupOIDC(t, h, p, cfg)
	before, _ := os.ReadFile(h.w.path)
	h.w.oidcConfirmation = "vault-test"
	h.tt.Frame()
	h.click("Sign in with browser")
	h.pump(func() bool { return h.w.oidcIdentity != nil })
	if requests.Load() != 0 {
		t.Fatal("cluster request before confirmation")
	}
	h.w.oidcConfirmation = "vault-test"
	h.tt.Frame()
	h.click("Connect verified identity")
	h.pump(func() bool { return h.w.status == "Live" })
	oldCtx, oldCfg := h.w.connectionCtx, h.w.backend.Config()
	h.pump(func() bool { return h.w.status == "Credentials expired" })
	if oldCtx.Err() == nil || h.w.backend != nil || h.w.oidcIdentity != nil || len(h.w.rows) != 0 {
		t.Fatal("expired identity retained")
	}
	oldHTTP, e := rest.HTTPClientFor(oldCfg)
	if e != nil {
		t.Fatal(e)
	}
	n := requests.Load()
	_, e = oldHTTP.Get(cfg.Host)
	if e == nil || n != requests.Load() {
		t.Fatal("old config used expired identity")
	}
	after, _ := os.ReadFile(h.w.path)
	if string(before) != string(after) {
		t.Fatal("OIDC rewrote kubeconfig")
	}
}
func TestNativeOIDCCancelAndTargetChangeInvalidateCallbacks(t *testing.T) {
	p := testoidc.New(t)
	h := newRelationshipHarness(t, 8*time.Second)
	setupOIDC(t, h, p, &rest.Config{Host: "https://cluster.invalid"})
	opened := make(chan struct{})
	h.w.oidcBrowser = func(string) error { close(opened); return nil }
	h.w.oidcConfirmation = "vault-test"
	h.tt.Frame()
	h.click("Sign in with browser")
	select {
	case <-opened:
	case <-h.ctx.Done():
		t.Fatal("browser not dispatched")
	}
	h.click("Cancel sign-in")
	h.w.reviewOIDC()
	if h.w.oidcReview != nil {
		t.Fatal("new worker admitted before old join")
	}
	h.pump(func() bool { return !h.w.oidcPending })
	if h.w.oidcIdentity != nil || p.TokenRequests.Load() != 0 {
		t.Fatal("cancel connected")
	}
	h.click("Review OIDC target")
	h.pump(func() bool { return !h.w.oidcPending })
	h.click("OIDC client ID")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("changed-client")
	h.tt.Frame()
	if h.w.oidcReview != nil || h.w.oidcIdentity != nil {
		t.Fatal("input change retained review")
	}
}
func TestNativeOIDCChangedKubeconfigCannotRetargetIdentity(t *testing.T) {
	p := testoidc.New(t)
	h := newRelationshipHarness(t, 8*time.Second)
	setupOIDC(t, h, p, &rest.Config{Host: "https://cluster.invalid"})
	h.w.oidcConfirmation = "vault-test"
	h.tt.Frame()
	h.click("Sign in with browser")
	h.pump(func() bool { return h.w.oidcIdentity != nil })
	other := testvault.ConfigFile(t, &rest.Config{Host: "https://changed.invalid"}, "team")
	b, _ := os.ReadFile(other)
	if e := os.WriteFile(h.w.path, b, 0600); e != nil {
		t.Fatal(e)
	}
	h.w.oidcConfirmation = "vault-test"
	h.tt.Frame()
	h.click("Connect verified identity")
	h.pump(func() bool { return !h.w.connectionPending })
	if h.w.backend != nil || h.w.status != "Connection failed" {
		t.Fatal("retargeted verified identity")
	}
}
func TestNativeOIDCWindowCloseCancelsPendingFlow(t *testing.T) {
	p := testoidc.New(t)
	h := newRelationshipHarness(t, 8*time.Second)
	setupOIDC(t, h, p, &rest.Config{Host: "https://cluster.invalid"})
	opened := make(chan struct{})
	h.w.oidcBrowser = func(string) error { close(opened); return nil }
	h.w.oidcConfirmation = "vault-test"
	h.w.loginOIDC(ui.Services{})
	select {
	case <-opened:
	case <-h.ctx.Done():
		t.Fatal("not opened")
	}
	h.w.Close()
	if h.w.ctx.Err() == nil || h.w.oidcIdentity != nil {
		t.Fatal("close retained login")
	}
}

func TestNativeOIDCBrowserDispatchUsesPersistentServicesOnce(t *testing.T) {
	p := testoidc.New(t)
	h := newRelationshipHarness(t, 8*time.Second)
	setupOIDC(t, h, p, &rest.Config{Host: "https://cluster.invalid"})
	h.w.oidcBrowser = nil
	h.w.oidcConfirmation = "vault-test"
	h.tt.Frame()
	h.click("Sign in with browser")
	h.pump(func() bool { return len(h.tt.OpenedURLs()) > 0 })
	link := h.tt.OpenedURLs()[0]
	if len(h.tt.OpenedURLs()) != 1 || strings.Contains(link, "code_verifier") || strings.Contains(link, "access_token") || !strings.Contains(link, "code_challenge_method=S256") {
		t.Fatal("unsafe/multiple browser dispatch")
	}
	h.click("Cancel sign-in")
	h.pump(func() bool { return !h.w.oidcPending })
	if p.TokenRequests.Load() != 0 {
		t.Fatal("cancel sent an exchange")
	}
}

func TestNativeOIDCScaledReviewRemainsReachable(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{{"minimum", 900, 480}, {"tiled", 936, 499}, {"desktop", 1100, 700}} {
		t.Run(size.name, func(t *testing.T) {
			p := testoidc.New(t)
			h := newRelationshipHarness(t, 15*time.Second)
			setupOIDC(t, h, p, &rest.Config{Host: "https://cluster.invalid"})
			h.tt.SetSize(size.width, size.height)
			title, _ := h.tt.Find("Browser sign-in (OpenID Connect)")
			h.tt.SetPreferences(ui.Preferences{TextScale: 2, ReduceMotion: true})
			large, _ := h.tt.Find("Browser sign-in (OpenID Connect)")
			if large.H < title.H*1.8 {
				t.Fatal("authentication heading ignores font preference")
			}
			viewport, ok := h.tt.Find("Authentication task")
			if !ok {
				t.Fatal("authentication task has no scroll viewport")
			}
			for _, label := range []string{"OIDC issuer", "OIDC client ID", "OIDC callback port", "Confirm OIDC context", "Sign in with browser", "Connect verified identity", "OIDC sign-in status"} {
				h.tt.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, -10000)
				reached := false
				for range 30 {
					r, ok := h.tt.Find(label)
					if ok && r.W > 0 && r.H > 0 && r.X >= viewport.X && r.X+r.W <= viewport.X+viewport.W && r.Y >= viewport.Y && r.Y+r.H <= viewport.Y+viewport.H {
						reached = true
						if label == "Confirm OIDC context" {
							h.click(label)
							h.tt.Type("vault-test")
						}
						break
					}
					h.tt.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, 40)
				}
				if !reached {
					saveNativeScreenshot(t, h.tt)
					t.Fatalf("scaled authentication control unreachable: %s", label)
				}
			}
			if h.w.oidcConfirmation != "vault-test" || p.TokenRequests.Load() != 0 || h.w.backend != nil {
				t.Fatal("scrolling lost confirmation or implicitly authenticated")
			}
			saveNativeScreenshot(t, h.tt)

		})
	}
}

func TestNativeOIDCFormLabelsAndDetails(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(string(rune('0'+int(scale))), func(t *testing.T) {
			w := New()
			t.Cleanup(w.Close)
			w.oidcOpen = true
			tt := ui.NewTester(w.View, 1100, 1000)
			tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			for _, label := range []string{"Issuer URL", "Client ID", "Callback port", "Issuer CA"} {
				if !tt.HasText(label) {
					t.Fatalf("missing persistent field label: %s", label)
				}
			}
			for _, label := range []string{"OIDC issuer", "OIDC client ID", "OIDC callback port", "OIDC issuer CA PEM"} {
				r, ok := tt.Find(label)
				if !ok || r.X < 0 || r.X+r.W > 1100 || r.W < 160 {
					t.Fatalf("field clipped: %s %+v", label, r)
				}
			}
			details := "Public client + S256 PKCE; same-origin HTTPS endpoints; single-use exchange; opt-in rotation only; no persistence or automatic refresh; issuer sign-out is a separate reviewed browser handoff"
			if tt.HasText(details) {
				t.Fatal("technical details crowd the default form")
			}
			if err := tt.Click("Sign-in details"); err != nil {
				t.Fatal(err)
			}
			if !tt.HasText(details) {
				t.Fatal("sign-in details lost")
			}
			if err := tt.Click("Sign-in details"); err != nil {
				t.Fatal(err)
			}
			if tt.HasText(details) {
				t.Fatal("details did not collapse")
			}
			saveNativeScreenshot(t, tt)
		})
	}
}
