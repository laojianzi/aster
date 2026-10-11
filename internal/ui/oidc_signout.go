package uiworkbench

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/oidclogin"
)

func (w *Workbench) clearOIDCSignOut() {
	w.oidcSignOut.Close()
	w.oidcSignOut = nil
	w.oidcSignOutConfirmation = ""
	w.oidcSignOutComposing = false
}

func (w *Workbench) oidcSignOutView(c *ui.Context) {
	capability := w.oidcSignOut
	if capability == nil {
		ui.Text(c, "No supported issuer sign-out endpoint for this connection. Disconnect only closes this workspace.").Label("OIDC sign-out unavailable").FontSize(11)
		return
	}
	info, epoch, reviewEpoch, services := capability.Info(), w.contextEpoch, w.oidcEpoch, c.Services()
	ui.Text(c, "Issuer sign-out · separate from local Disconnect").FontSize(16).Bold()
	ui.Row(c).Label("OIDC sign-out issuer").Children(func() {
		ui.Text(c, "Original issuer: "+info.Issuer).FontSize(12).SingleLine().Grow(1)
	})
	ui.Text(c, fmt.Sprintf("Workspace context: %s · Aster subject: %q", info.Context, info.Subject)).Label("OIDC sign-out identity").FontSize(12).SingleLine()
	ui.Text(c, "Browser destination: "+info.Endpoint).Label("OIDC sign-out endpoint").FontSize(12).SingleLine()
	ui.Text(c, "This closes this workspace first. Confirm the browser's current account at the issuer; it may differ or be shared by other apps.").FontSize(12)
	ui.Text(c, "No token is sent to the browser. Handoff does not prove issuer completion or revoke existing Kubernetes JWTs; other workspaces and stored tokens are not erased.").Label("OIDC sign-out boundary").FontSize(11)
	w.oidcSignOutComposing = ui.TextInput(c.Key(fmt.Sprintf("oidc.signoutConfirm.%d.%d", w.contextEpoch, w.oidcEpoch)), &w.oidcSignOutConfirmation).Label("Confirm issuer sign-out context").Placeholder("Type exact active context; watches, logs and terminal sessions will stop").Composing()
	ui.Button(c, "Disconnect and open issuer sign-out").Disabled(!w.eligibleOIDCSignOut(epoch, reviewEpoch, capability)).OnClick(func() {
		w.signOutOIDC(services, epoch, reviewEpoch, capability)
	})
}

func (w *Workbench) eligibleOIDCSignOut(epoch, reviewEpoch uint64, capability *oidclogin.SignOut) bool {
	if capability == nil || w.oidcSignOut != capability || epoch != w.contextEpoch || reviewEpoch != w.oidcEpoch || w.oidcSignOutComposing || !w.oidcOpen || w.vaultOpen ||
		w.backend == nil || w.oidcPending || w.vaultPending || w.connectionPending || w.ctx.Err() != nil ||
		w.connectionCtx == nil || w.connectionCtx.Err() != nil || !time.Now().Before(w.credentialExpiry) {
		return false
	}
	info := capability.Info()
	return info.Context != "" && info.Context == w.activeContext && w.currentContext == w.activeContext && w.oidcSignOutConfirmation == info.Context
}

func (w *Workbench) signOutOIDC(services ui.Services, epoch, reviewEpoch uint64, capability *oidclogin.SignOut) {
	if !w.eligibleOIDCSignOut(epoch, reviewEpoch, capability) {
		return
	}
	// Only the active connection's immutable reviewed endpoint is used. Editable
	// issuer/client fields, kubeconfig changes and pending renewed identities do
	// not supply a destination. Taking the public URL is local and one-use.
	link, err := capability.TakeURL(w.connectionCtx, w.activeContext)
	w.disconnect() // cancel old leases/streams and discard local renewal BEFORE OpenURL
	w.oidcOpen = true
	if err != nil {
		w.oidcStatus = "Workspace disconnected. Issuer sign-out was unavailable or expired; no browser request was made."
		return
	}
	if w.oidcSignOutBrowser != nil { // fixture only; never set by the application
		if w.oidcSignOutBrowser(link) != nil {
			w.oidcStatus = "Workspace disconnected. Browser sign-out handoff failed; issuer result unverified. No automatic retry."
			return
		}
	} else {
		services.OpenURL(link)
	}
	w.oidcStatus = "Workspace disconnected. System-browser sign-out requested: confirm at the issuer. Issuer completion and JWT revocation are NOT verified."
}
