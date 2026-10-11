package uiworkbench

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/testoidc"
	"k8s.io/client-go/rest"
)

func signOutWorkbench(t *testing.T) (*relationshipHarness, *testoidc.Provider) {
	t.Helper()
	p := testoidc.New(t)
	p.EditDiscovery = func(d map[string]any) { d["end_session_endpoint"] = p.Server.URL + "/logout" }
	cfg, _ := renewalAPIServer(t)
	h := newRelationshipHarness(t, 15*time.Second)
	loginRenewable(t, h, p, cfg)
	if h.w.oidcSignOut == nil {
		t.Fatal("missing sign-out transfer")
	}
	h.click("Browser sign-in")
	return h, p
}
func TestNativeOIDCSignOutReviewTeardownAndMinimumWindow(t *testing.T) {
	h, p := signOutWorkbench(t)
	before, _ := os.ReadFile(h.w.path)
	old, lease := h.w.backend, h.w.connectionCtx
	cap, epoch, reviewEpoch := h.w.oidcSignOut, h.w.contextEpoch, h.w.oidcEpoch
	calls := 0
	h.w.oidcSignOutBrowser = func(link string) error {
		calls++
		if lease.Err() == nil || h.w.backend != nil || h.w.oidcRenewal != nil || h.w.oidcSignOut != nil || h.w.detail != nil || len(h.w.rows) != 0 {
			t.Fatal("browser ran before teardown")
		}
		u, e := url.Parse(link)
		if e != nil || u.Scheme+"://"+u.Host != p.Server.URL || u.Path != "/logout" || len(u.Query()) != 1 || u.Query().Get("client_id") != "aster-test" {
			t.Fatal("destination not original public review")
		}
		return nil
	}
	h.click("Disconnect and open issuer sign-out")
	if calls != 0 || lease.Err() != nil {
		t.Fatal("no-confirmation sign-out")
	}
	h.tt.SetSize(1100, 700)
	h.tt.Frame()
	for _, label := range []string{"OIDC sign-out identity", "OIDC sign-out endpoint", "OIDC sign-out boundary", "Confirm issuer sign-out context", "Disconnect and open issuer sign-out"} {
		b, ok := h.tt.Find(label)
		if !ok || b.H < 8 || b.X < 0 || b.Y < 0 || b.X+b.W > 1100 || b.Y+b.H > 700 {
			t.Fatal("sign-out clipped", label, b)
		}
	}
	saveNativeScreenshot(t, h.tt)
	// The active immutable endpoint, not these editable fields, owns the handoff.
	h.w.oidcIssuer = "https://unreviewed.invalid"
	h.w.oidcClient = "unreviewed"
	h.click("Confirm issuer sign-out context")
	h.tt.Type("vault-test")
	h.click("Disconnect and open issuer sign-out")
	if calls != 1 || !strings.Contains(h.w.oidcStatus, "NOT verified") {
		t.Fatal("incorrect handoff outcome", h.w.oidcStatus)
	}
	h.w.signOutOIDC(ui.Services{}, epoch, reviewEpoch, cap)
	if calls != 1 {
		t.Fatal("stale callback replay")
	}
	client, e := rest.HTTPClientFor(old.Config())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.Get(old.Config().Host); e == nil {
		t.Fatal("old client still usable")
	}
	after, _ := os.ReadFile(h.w.path)
	if string(before) != string(after) {
		t.Fatal("sign-out rewrote profile")
	}
}
func TestNativeOIDCSignOutFailureIsRedactedAndNeverReplayed(t *testing.T) {
	h, _ := signOutWorkbench(t)
	calls := 0
	h.w.oidcSignOutBrowser = func(string) error { calls++; return errors.New("id_token_hint=secret-untrusted-error") }
	h.click("Confirm issuer sign-out context")
	h.tt.Type("vault-test")
	h.click("Disconnect and open issuer sign-out")
	if calls != 1 || h.w.backend != nil || strings.Contains(h.w.oidcStatus, "secret") || !strings.Contains(h.w.oidcStatus, "unverified") {
		t.Fatal("handoff failure misrepresented")
	}
	for range 3 {
		h.tt.Frame()
	}
	if calls != 1 {
		t.Fatal("automatic retry")
	}
}
func TestNativeOIDCSignOutStaleLeaseContextPendingAndWindow(t *testing.T) {
	for _, which := range []string{"changed-context", "pending", "closed-lease", "unpumped-expiry", "closed-window", "back", "stale-panel", "composing"} {
		t.Run(which, func(t *testing.T) {
			h, _ := signOutWorkbench(t)
			cap, epoch, reviewEpoch := h.w.oidcSignOut, h.w.contextEpoch, h.w.oidcEpoch
			h.w.oidcSignOutConfirmation = "vault-test"
			calls := 0
			h.w.oidcSignOutBrowser = func(string) error { calls++; return nil }
			switch which {
			case "changed-context":
				h.w.currentContext = "other"
			case "pending":
				h.w.oidcPending = true
			case "closed-lease":
				h.w.connectionCancel()
			case "unpumped-expiry":
				h.w.credentialExpiry = time.Now().Add(-time.Second)
			case "closed-window":
				h.w.Close()
			case "stale-panel":
				h.w.oidcEpoch++
			case "composing":
				h.w.oidcSignOutComposing = true
			case "back":
				h.w.oidcOpen = false
			}
			h.w.signOutOIDC(ui.Services{}, epoch, reviewEpoch, cap)
			if calls != 0 {
				t.Fatal("ineligible action launched browser")
			}
			h.w.oidcPending = false // restore harness cleanup state only
		})
	}
}
func TestNativeOIDCSignOutConfirmationHistoryAndRenewalReplacement(t *testing.T) {
	h, _ := signOutWorkbench(t)
	h.click("Confirm issuer sign-out context")
	h.tt.Type("vault-")
	h.w.oidcStatus = "Unrelated frame"
	h.tt.Frame()
	h.tt.Type("test")
	if h.w.oidcSignOutConfirmation != "vault-test" {
		t.Fatal("unstable confirmation identity")
	}
	old := h.w.oidcSignOut
	h.click("Renew identity once")
	h.pump(func() bool { return !h.w.oidcPending })
	if h.w.oidcSignOutConfirmation != "" || h.w.oidcSignOut != old || h.w.oidcIdentity == nil {
		t.Fatal("renewal lost current sign-out or retained confirmation")
	}
	h.click("Confirm issuer sign-out context")
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.oidcSignOutConfirmation != "" {
		t.Fatal("old confirmation undo recovered")
	}
	h.tt.SetSize(1100, 700)
	h.tt.Frame()
	b, ok := h.tt.Find("Disconnect and open issuer sign-out")
	if !ok || b.Y+b.H > 700 {
		t.Fatal("pending replacement clips sign-out", b)
	}
	h.click("Confirm OIDC replacement context")
	h.tt.Type("vault-test")
	h.click("Replace verified connection")
	h.pump(func() bool { return h.w.status == "Live" && !h.w.connectionPending })
	if h.w.oidcSignOut == nil || h.w.oidcSignOut == old {
		t.Fatal("sign-out capability not replaced")
	}
	if _, e := old.TakeURL(context.Background(), "vault-test"); e == nil {
		t.Fatal("old capability survived renewal")
	}
}
func TestNativeOIDCSignOutDoesNotEraseOtherWorkspace(t *testing.T) {
	h, _ := signOutWorkbench(t)
	other, _ := signOutWorkbench(t)
	otherCap, otherConn := other.w.oidcSignOut, other.w.connectionCtx
	h.w.oidcSignOutBrowser = func(string) error { return nil }
	h.click("Confirm issuer sign-out context")
	h.tt.Type("vault-test")
	h.click("Disconnect and open issuer sign-out")
	if other.w.oidcSignOut != otherCap || otherConn.Err() != nil || other.w.backend == nil || other.w.oidcRenewal == nil {
		t.Fatal("unrelated workspace erased")
	}
}

func TestNativeOIDCSignOutPersistentServicesDispatchOnce(t *testing.T) {
	h, p := signOutWorkbench(t)
	lease := h.w.connectionCtx
	h.w.oidcSignOutBrowser = nil
	h.click("Confirm issuer sign-out context")
	h.tt.Type("vault-test")
	h.click("Disconnect and open issuer sign-out")
	for range 3 {
		h.tt.Frame()
	}
	urls := h.tt.OpenedURLs()
	if lease.Err() == nil || len(urls) != 1 || urls[0] != p.Server.URL+"/logout?client_id=aster-test" {
		t.Fatal("wrong persistent-service dispatch")
	}
}
