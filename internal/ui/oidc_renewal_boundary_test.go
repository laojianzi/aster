package uiworkbench

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/oidclogin"
	"github.com/laojianzi/aster/internal/testoidc"
)

// Reject replacement even before the expired connection callback reaches the UI.
func TestNativeOIDCRenewalCannotReplaceAfterUnpumpedExpiry(t *testing.T) {
	p := testoidc.New(t)
	p.EditClaims = func(c map[string]any) { c["exp"] = time.Now().Add(5 * time.Second).Unix() }
	cfg, _ := renewalAPIServer(t)
	h := newRelationshipHarness(t, 12*time.Second)
	loginRenewable(t, h, p, cfg)
	h.click("Browser sign-in")
	h.click("Renew identity once")
	h.pump(func() bool { return !h.w.oidcPending })
	if h.w.oidcIdentity == nil {
		t.Fatal(h.w.oidcStatus)
	}
	old, epoch := h.w.backend, h.w.contextEpoch
	// Do not pump or frame: the expiry callback must remain queued.
	select {
	case <-h.w.connectionCtx.Done():
	case <-h.ctx.Done():
		t.Fatal("old lease did not expire")
	}
	h.w.oidcConfirmation = "vault-test"
	h.w.connectOIDC()
	if h.w.backend != old || h.w.connectionPending || h.w.contextEpoch != epoch {
		t.Fatal("unprocessed expiry silently extended an identity")
	}
	h.pump(func() bool { return h.w.status == "Credentials expired" })
	if h.w.oidcIdentity != nil || h.w.oidcRenewal != nil {
		t.Fatal("expired pending rotation retained")
	}
}

func TestNativeOIDCRenewalFailuresNeverExposeUntrustedErrors(t *testing.T) {
	for _, err := range []error{errors.New("sensitive-path/token"), fmt.Errorf("secret %w", oidclogin.ErrTransport), fmt.Errorf("secret %w", oidclogin.ErrVerification)} {
		status := renewalFailureStatus(err)
		if strings.Contains(status, "secret") || strings.Contains(status, "sensitive-path/token") {
			t.Fatal("untrusted failure reached UI")
		}
	}
	if !strings.Contains(renewalFailureStatus(oidclogin.ErrVerification), "identity rejected") {
		t.Fatal("safe failure category missing")
	}
}
