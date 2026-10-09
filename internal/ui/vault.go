package uiworkbench

import (
	"context"
	"errors"
	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/credentialexec"
	"github.com/laojianzi/aster/internal/credentialvault"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"time"
)

// clearVault invalidates queued callbacks but retains admission until the worker
// joins. Forgetting local UI data is not deletion from the OS credential store.
func (w *Workbench) clearVault() {
	if w.vaultPending && w.vaultMutation {
		w.vaultUncertain = true
	}
	w.vaultEpoch++
	if w.vaultCancel != nil {
		w.vaultCancel()
		w.vaultCancel = nil
	}
	w.vaultToken, w.vaultConfirmation, w.vaultStatus, w.vaultTrust, w.vaultTrustPending = "", "", "", "", ""
	w.vaultTarget = nil
}
func (w *Workbench) reviewVault() {
	if w.vaultPending || w.connectionPending || w.backend != nil {
		return
	}
	w.vaultPending = true
	w.vaultMutation = false
	w.vaultEpoch++
	epoch := w.vaultEpoch
	w.vaultToken, w.vaultConfirmation = "", ""
	w.vaultTarget = nil
	w.vaultStatus = "Reviewing credential-free profile (no cluster request)"
	opts := kubeconfig.Options{Path: w.path, Context: w.currentContext, Namespace: w.namespace, TrustToken: w.vaultTrust}
	ctx, cancel := context.WithCancel(w.ctx)
	w.vaultCancel = cancel
	w.run(func(context.Context) {
		_, target, err := kubeconfig.LoadVault(opts)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		w.emit(func() {
			w.vaultPending = false
			cancel()
			if epoch != w.vaultEpoch {
				return
			}
			if err != nil {
				var trust *kubeconfig.TrustRequiredError
				if errors.As(err, &trust) {
					w.vaultTrustPending = trust.Fingerprint
					w.vaultStatus = "This profile references a CA file. Trust only a file you control."
				} else {
					w.vaultStatus = "Use a credential-free HTTPS user profile. Existing tokens, exec, certificates, impersonation and unsafe transport cannot be replaced."
				}
				return
			}

			w.vaultTarget = target
			w.vaultTrustPending = ""
			w.vaultStatus = "Target reviewed. Stored expiry is only a local upper bound, not proof of token validity."
		})
	})
}
func (w *Workbench) writeVault(forget bool) {
	if w.vaultTarget == nil || w.vaultPending || w.connectionPending || w.backend != nil || w.vaultConfirmation != w.vaultTarget.Context {
		return
	}
	target, store := w.vaultTarget, w.vaultStore
	token := w.vaultToken
	w.vaultToken = ""
	w.vaultConfirmation = ""
	hours := map[string]time.Duration{"15 minutes": 15 * time.Minute, "1 hour": time.Hour, "8 hours": 8 * time.Hour, "24 hours": 24 * time.Hour}
	ttl := hours[w.vaultDuration]
	if ttl == 0 {
		ttl = time.Hour
	}
	expires := time.Now().Add(ttl)
	opts := kubeconfig.Options{Path: w.path, Context: w.currentContext, Namespace: w.namespace, TrustToken: w.vaultTrust}
	w.vaultPending = true
	w.vaultMutation = true
	w.vaultEpoch++
	epoch := w.vaultEpoch
	ctx, cancel := context.WithCancel(w.ctx)
	w.vaultCancel = cancel
	w.vaultStatus = "OS credential operation in progress"
	w.run(func(context.Context) {
		_, fresh, err := kubeconfig.LoadVault(opts)
		if err == nil && fresh.Key() != target.Key() {
			err = credentialvault.ErrInvalid
		}
		if err == nil {
			if forget {
				err = target.Forget(ctx, store)
			} else {
				err = target.Put(ctx, store, token, expires)
			}
		}
		token = ""
		w.emit(func() {
			w.vaultPending = false
			cancel()
			if epoch != w.vaultEpoch {
				return
			}
			if err != nil {
				w.vaultStatus = err.Error()
				return
			}
			w.vaultUncertain = false
			if forget {
				w.vaultStatus = "Stored token deleted for this target. This is not remote token revocation."
			} else {
				w.vaultStatus = "Token stored in OS credentials. Connect with stored token explicitly; no automatic login."
			}
		})
	})
}
func (w *Workbench) connectVault() {
	if w.vaultTarget == nil || w.vaultPending || w.connectionPending || w.backend != nil || w.vaultConfirmation != w.vaultTarget.Context {
		return
	}
	key, trust, store := w.vaultTarget.Key(), w.vaultTrust, w.vaultStore
	w.vaultOpen = false
	w.beginConnection(func(ctx context.Context, opts kubeconfig.Options) (kubeconfig.Connection, credentialexec.Resolved, error) {
		opts.TrustToken = trust
		conn, target, err := kubeconfig.LoadVault(opts)
		if err != nil {
			return conn, credentialexec.Resolved{}, err
		}
		if target.Key() != key {
			return conn, credentialexec.Resolved{}, credentialvault.ErrInvalid
		}
		resolved, err := target.Resolve(ctx, store)
		return conn, resolved, err
	})
}
func (w *Workbench) vaultView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).Padding(16).Gap(10).Children(func() {
		ui.Text(c, "OS credential storage").FontSize(20).Bold()
		if w.vaultUncertain {
			ui.Text(c, "An interrupted OS operation may have changed storage. Explicitly review and load or forget; cancellation is not rollback.").FontSize(12).TextColor(t.Danger)
		}
		ui.Text(c, "Explicit operations only. Use an HTTPS kubeconfig context with an empty user entry; existing credentials are never overridden.").FontSize(12)
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, "Review credential target").Disabled(w.currentContext == "" || w.vaultPending || w.connectionPending || w.backend != nil).Clicked() {
				w.reviewVault()
			}
			if ui.Button(c, "Back to resources").Clicked() {
				w.clearVault()
				w.vaultOpen = false
			}
		})
		if w.backend != nil {
			ui.Text(c, "Disconnect first to review, store, load or forget a token.").FontSize(12)
		}
		if w.vaultTrustPending != "" {
			if ui.Button(c, "Trust CA file and review").Disabled(w.vaultPending).Clicked() {
				w.vaultTrust = w.vaultTrustPending
				w.reviewVault()
			}
		}
		if w.vaultTarget != nil {
			ui.Text(c, "Context: "+w.vaultTarget.Context).Label("Credential target context").SingleLine()
			ui.Text(c, "Server: "+w.vaultTarget.Server).Label("Credential target server").SingleLine()
			ui.Text(c, "User alias: "+w.vaultTarget.User).SingleLine()
			ui.TextInput(c, &w.vaultToken).Password().Label("Token to store").Placeholder("Token (never written to kubeconfig)").Disabled(w.vaultPending)
			ui.Select(c, &w.vaultDuration, []string{"15 minutes", "1 hour", "8 hours", "24 hours"}).Label("Local token lifetime").Disabled(w.vaultPending)
			ui.TextInput(c, &w.vaultConfirmation).Label("Confirm credential context").Placeholder("Type the exact context name to authorize an action").Disabled(w.vaultPending)
			enabled := !w.vaultPending && !w.connectionPending && w.backend == nil && w.vaultConfirmation == w.vaultTarget.Context
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, "Store token").Disabled(!enabled || w.vaultToken == "").Clicked() {
					w.writeVault(false)
				}
				if ui.Button(c, "Connect with stored token").Disabled(!enabled).Clicked() {
					w.connectVault()
				}
				if ui.Button(c, "Forget stored token").Disabled(!enabled).Clicked() {
					w.writeVault(true)
				}
			})
			ui.Text(c, "A connection lasts at most 15 minutes. Forget affects this OS slot only, not other workspaces or server-issued token validity.").FontSize(12)
		}
		ui.Text(c, w.vaultStatus).Label("Credential storage status").FontSize(12).TextColor(t.TextMuted)
	})
}
