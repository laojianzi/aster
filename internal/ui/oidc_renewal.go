package uiworkbench

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/oidclogin"
)

func (w *Workbench) clearOIDCRenewal() {
	if w.oidcRenewal != nil {
		w.oidcRenewal.Close()
		w.oidcRenewal = nil
	}
	w.oidcRenewalTarget = nil
	w.oidcRenewalProfile = kubeconfig.Options{}
}
func (w *Workbench) renewOIDC() {
	if w.backend == nil || w.oidcRenewal == nil || w.oidcPending || w.vaultPending || w.connectionPending || w.oidcIdentity != nil {
		return
	}
	w.clearOIDC()
	capability, profile := w.oidcRenewal, w.oidcRenewalProfile
	w.oidcRenewal = nil // single attempt; no repeat button while worker is active
	epoch, connectionEpoch := w.oidcEpoch, w.contextEpoch
	ctx, cancel := context.WithTimeout(w.connectionCtx, oidclogin.RenewalTimeout)
	w.oidcCancel = cancel
	w.oidcPending = true
	w.oidcStatus = "Rotating once. The current connection is unchanged until you confirm a verified replacement."
	w.run(func(context.Context) {
		defer capability.Close()
		conn, target, err := kubeconfig.LoadVault(profile)
		var id *oidclogin.Identity
		if err == nil {
			id, err = capability.Renew(ctx, conn.Config, conn.ContextName, target.User)
		}
		if ctx.Err() != nil && id != nil {
			id.Close()
			id = nil
			err = oidclogin.ErrInterrupted
		}
		stopCleanup := func() bool { return true }
		if id != nil {
			stopCleanup = context.AfterFunc(w.ctx, id.Close)
		}
		w.emit(func() {
			w.oidcPending = false
			cancel()
			defer stopCleanup()
			if epoch != w.oidcEpoch || connectionEpoch != w.contextEpoch {
				if id != nil {
					id.Close()
				}
				return
			}
			if err != nil {
				// The prior credential has been consumed even on preflight failure.
				w.oidcStatus = "Renewal failed or interrupted; sign in again. No retry. The old connection ends at its original deadline."
				return
			}
			w.oidcIdentity, w.oidcTarget, w.oidcTrust = id, target, profile.TrustToken
			w.oidcReplacing, w.oidcReplacementEpoch = true, connectionEpoch
			w.oidcConfirmation = ""
			w.oidcStatus = "New identity verified; confirm the exact context to close old streams and replace the connection."
		})
	})
}
func (w *Workbench) oidcRenewalView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).Padding(12).Gap(8).Children(func() {
		ui.Text(c, "OIDC · reviewed connection renewal").FontSize(20).Bold()
		ui.Text(c, "Explicit, memory-only rotation. No background renewal, persistent refresh storage, browser logout or issuer revocation.").FontSize(12)
		if w.oidcRenewalTarget != nil {
			ui.Text(c, "Context: "+w.oidcRenewalTarget.Context+" · Server: "+w.oidcRenewalTarget.Server).Label("OIDC renewal target").SingleLine().FontSize(12)
		}
		if w.oidcRenewal != nil {
			info := w.oidcRenewal.Info()
			ui.Text(c, fmt.Sprintf("Bound subject: %q · Issuer: %s", info.Subject, info.Issuer)).Label("OIDC renewal identity").SingleLine().FontSize(12)
			ui.Text(c, fmt.Sprintf("Local family expires %s · %d rotations remaining", info.FamilyExpiresAt.Local().Format("15:04:05"), info.Remaining)).Label("OIDC renewal budget").FontSize(12)
		} else if !w.oidcPending && w.oidcIdentity == nil {
			ui.Text(c, "No unused renewal credential. Disconnect and sign in again; offline access is opt-in before login.").Label("OIDC no renewal").FontSize(12)
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, "Renew identity once").Disabled(w.oidcRenewal == nil || w.oidcPending || w.connectionPending || w.oidcIdentity != nil || !time.Now().Before(w.credentialExpiry)).OnClick(func() { w.renewOIDC() })
			ui.Button(c, "Forget renewal").Disabled(w.oidcRenewal == nil && !w.oidcPending && w.oidcIdentity == nil).OnClick(func() {
				w.clearOIDC()
				w.clearOIDCRenewal()
				w.oidcStatus = "Renewal forgotten locally. The existing connection is unchanged; issuer credentials are not revoked."
			})
			ui.Button(c, "Back to resources").OnClick(func() { w.clearOIDC(); w.oidcOpen = false })
		})
		if w.oidcIdentity != nil {
			info := w.oidcIdentity.Info()
			ui.Text(c, fmt.Sprintf("Verified subject: %q · Issuer: %s", info.Subject, info.Issuer)).Label("OIDC verified identity").SingleLine().FontSize(12)
			ui.Text(c, "New token expires "+info.ExpiresAt.Local().Format("15:04:05")+"; the old connection has NOT been replaced.").Label("OIDC replacement deadline").FontSize(12)
			ui.TextInput(c.Key("oidc.renewalConfirm"), &w.oidcConfirmation).Label("Confirm OIDC replacement context").Placeholder("Type exact context to replace; old logs, watches, commands and terminal sessions will stop")
			ui.Button(c, "Replace verified connection").Disabled(!w.oidcReplacing || w.oidcTarget == nil || w.oidcConfirmation != w.oidcTarget.Context || w.oidcPending || w.connectionPending || w.oidcReplacementEpoch != w.contextEpoch).OnClick(func() { w.connectOIDC() })
		}
		ui.Text(c, w.oidcStatus).Label("OIDC renewal status").FontSize(12).TextColor(t.TextMuted)
	})
}
