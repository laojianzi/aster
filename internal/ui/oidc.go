package uiworkbench

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/credentialexec"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/oidclogin"
)

func (w *Workbench) clearOIDC() {
	w.oidcReplacing = false
	w.oidcEpoch++
	if w.oidcCancel != nil {
		w.oidcCancel()
		w.oidcCancel = nil
	}
	if w.oidcReview != nil {
		w.oidcReview.Close()
		w.oidcReview = nil
	}
	if w.oidcIdentity != nil {
		w.oidcIdentity.Close()
		w.oidcIdentity = nil
	}
	w.oidcTarget = nil
	w.oidcConfirmation, w.oidcStatus, w.oidcTrust, w.oidcTrustPending = "", "", "", ""
}
func (w *Workbench) reviewOIDC() {
	if w.oidcPending || w.vaultPending || w.connectionPending || w.backend != nil {
		return
	}
	trust := w.oidcTrust
	w.clearOIDC()
	w.oidcTrust = trust
	port, e := strconv.ParseUint(w.oidcPort, 10, 16)
	if e != nil || (port != 0 && port < 1024) {
		w.oidcStatus = "Callback port must be 0 (automatic) or 1024–65535"
		return
	}
	if len(w.oidcCA) > oidclogin.MaxCABytes {
		w.oidcStatus = oidclogin.ErrConfiguration.Error()
		return
	}
	options := oidclogin.Options{Issuer: w.oidcIssuer, ClientID: w.oidcClient, CAPEM: []byte(w.oidcCA), Port: uint16(port), AllowRenewal: w.oidcAllowRenewal}
	profile := kubeconfig.Options{Path: w.path, Context: w.currentContext, Namespace: w.namespace, TrustToken: trust}
	w.oidcPending = true
	epoch := w.oidcEpoch
	ctx, cancel := context.WithCancel(w.ctx)
	w.oidcCancel = cancel
	w.oidcStatus = "Reading target and issuer discovery; no cluster authentication"
	w.run(func(context.Context) {
		_, target, err := kubeconfig.LoadVault(profile)
		var review *oidclogin.Review
		if err == nil {
			options.TargetKey = target.Key()
			review, err = oidclogin.Discover(ctx, options)
		}
		w.emit(func() {
			w.oidcPending = false
			cancel()
			if epoch != w.oidcEpoch {
				if review != nil {
					review.Close()
				}
				return
			}
			if err != nil {
				var required *kubeconfig.TrustRequiredError
				if errors.As(err, &required) {
					w.oidcTrustPending = required.Fingerprint
					w.oidcStatus = "Profile references a CA file. Trust only a file you control before reviewing."
				} else {
					w.oidcStatus = "Review failed: use a credential-free HTTPS target and a supported HTTPS issuer with trusted certificates."
				}
				return
			}
			w.oidcTarget, w.oidcReview = target, review
			w.oidcStatus = "Review these endpoints, then confirm the context to open the system browser. No automatic connection."
		})
	})
}
func (w *Workbench) loginOIDC(services ui.Services) {
	if w.oidcTarget == nil || w.oidcReview == nil || w.oidcPending || w.vaultPending || w.connectionPending || w.backend != nil || w.oidcConfirmation != w.oidcTarget.Context {
		return
	}
	review := w.oidcReview
	w.oidcReview = nil
	w.oidcConfirmation = ""
	w.oidcEpoch++
	epoch := w.oidcEpoch
	w.oidcPending = true
	ctx, cancel := context.WithCancel(w.ctx)
	w.oidcCancel = cancel
	w.oidcStatus = "Complete sign-in in the system browser, then return to Aster. Login times out after five minutes."
	override := w.oidcBrowser // test fixture only; the production constructor leaves it nil.
	w.run(func(context.Context) {
		id, err := review.Login(ctx, func(link string) error {
			if override != nil {
				return override(link)
			}
			dispatched := make(chan error, 1)
			w.emit(func() {
				if epoch != w.oidcEpoch || ctx.Err() != nil {
					dispatched <- oidclogin.ErrInterrupted
					return
				}
				services.OpenURL(link)
				dispatched <- nil
			})
			select {
			case <-ctx.Done():
				return oidclogin.ErrInterrupted
			case e := <-dispatched:
				return e
			}
		})
		review.Close()
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
			if epoch != w.oidcEpoch {
				if id != nil {
					id.Close()
				}
				return
			}
			if err != nil {
				w.oidcStatus = err.Error()
				return
			}
			w.oidcIdentity = id
			w.oidcStatus = "Identity verified. Confirm the context again to connect. Kubernetes permissions are evaluated by the cluster."
		})
	})
}
func (w *Workbench) connectOIDC() {
	if w.oidcTarget == nil || w.oidcIdentity == nil || w.oidcPending || w.vaultPending || w.connectionPending || w.oidcConfirmation != w.oidcTarget.Context {
		return
	}
	if w.backend != nil && (!w.oidcReplacing || w.oidcReplacementEpoch != w.contextEpoch) {
		return
	}
	id, trust, target := w.oidcIdentity, w.oidcTrust, w.oidcTarget
	// Detach before beginConnection cancels the old connection and pending UI state.
	w.oidcIdentity = nil
	w.oidcOpen = false
	var next *oidclogin.Renewal
	stopCleanup := func() bool { return true }
	var profile kubeconfig.Options
	w.beginConnectionWithCompletion(func(ctx context.Context, opts kubeconfig.Options) (kubeconfig.Connection, credentialexec.Resolved, error) {
		defer id.Close()
		opts.TrustToken = trust
		profile = opts
		conn, freshTarget, err := kubeconfig.LoadVault(opts)
		if err != nil {
			return conn, credentialexec.Resolved{}, oidclogin.ErrConfiguration
		}
		resolved, err := id.Resolve(ctx, conn.Config, conn.ContextName, freshTarget.User)
		if err == nil {
			next = id.TakeRenewal()
		}
		if ctx.Err() != nil && next != nil {
			next.Close()
			next = nil
			err = oidclogin.ErrInterrupted
		}
		if next != nil {
			stopCleanup = context.AfterFunc(ctx, next.Close)
		}
		return conn, resolved, err
	}, func(ok bool) {
		defer stopCleanup()
		// A stopped/replaced connection cannot inherit the rotating credential.
		if !ok || next == nil || !time.Now().Before(next.Info().FamilyExpiresAt) || next.Info().Remaining <= 0 {
			if next != nil {
				next.Close()
			}
			return
		}
		w.oidcRenewal, w.oidcRenewalProfile, w.oidcRenewalTarget = next, profile, target
	})
}
func (w *Workbench) oidcView(c *ui.Context) {
	if w.backend != nil {
		w.oidcRenewalView(c)
		return
	}
	t := c.Theme()
	services := c.Services()
	ui.Column(c).Grow(1).Padding(12).Gap(6).Children(func() {
		ui.Text(c, "Browser sign-in · OpenID Connect").FontSize(20).Bold()
		ui.Text(c, "Use a credential-free HTTPS context. Tokens remain in this window's memory; the OS token vault is not read or changed.").FontSize(12)
		ui.Row(c).Gap(8).Children(func() {
			if ui.TextInput(c.Key("oidc.issuer"), &w.oidcIssuer).Label("OIDC issuer").Placeholder("HTTPS issuer URL").Grow(1).Changed() {
				w.clearOIDC()
			}
			if ui.TextInput(c.Key("oidc.client"), &w.oidcClient).Label("OIDC client ID").Placeholder("Registered public client ID").Width(240).Changed() {
				w.clearOIDC()
			}
			if ui.TextInput(c.Key("oidc.port"), &w.oidcPort).Label("OIDC callback port").Placeholder("0: automatic").Width(110).Changed() {
				w.clearOIDC()
			}
		})
		if ui.TextArea(c.Key("oidc.ca"), &w.oidcCA).Label("OIDC issuer CA PEM").Placeholder("Optional issuer CA PEM (empty: system trust). Independent of the cluster CA.").Height(60).Changed() {
			w.clearOIDC()
		}
		if ui.Checkbox(c.Key("oidc.allowRenewal"), &w.oidcAllowRenewal, "Allow memory-only renewal (requests offline access and consent)").Disabled(w.oidcPending).Changed() {
			w.clearOIDC()
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, "Review OIDC target").Disabled(w.currentContext == "" || w.backend != nil || w.oidcPending || w.vaultPending || w.connectionPending).OnClick(func() { w.reviewOIDC() })
			ui.Button(c, "Cancel sign-in").Disabled(!w.oidcPending && w.oidcReview == nil && w.oidcIdentity == nil).OnClick(func() {
				w.clearOIDC()
				w.oidcStatus = "Sign-in canceled locally; this does not log out the browser or revoke issuer credentials."
			})
			ui.Button(c, "Back to resources").OnClick(func() { w.clearOIDC(); w.oidcOpen = false })
		})
		if w.oidcTrustPending != "" {
			ui.Button(c, "Trust cluster CA file and review OIDC").Disabled(w.oidcPending).OnClick(func() { w.oidcTrust = w.oidcTrustPending; w.reviewOIDC() })
		}
		if w.backend != nil {
			ui.Text(c, "Disconnect before reviewing another identity.").FontSize(12)
		}
		if w.oidcTarget != nil {
			ui.Text(c, "Context: "+w.oidcTarget.Context+" · Server: "+w.oidcTarget.Server).Label("OIDC reviewed target").SingleLine().FontSize(12)
			if w.oidcReview != nil {
				ep := w.oidcReview.Endpoints()
				ui.Text(c, "Browser: "+ep.Authorization).Label("OIDC authorization endpoint").SingleLine().FontSize(12)
				ui.Text(c, "Token: "+ep.Token+" · Keys: "+ep.Keys).Label("OIDC token and key endpoints").SingleLine().FontSize(12)
			}
			if w.oidcIdentity != nil {
				info := w.oidcIdentity.Info()
				ui.Text(c, fmt.Sprintf("Verified subject: %q · Issuer: %s", info.Subject, info.Issuer)).Label("OIDC verified identity").SingleLine().FontSize(12)
				ui.Text(c, "ID token expires "+info.ExpiresAt.Local().Format("2006-01-02 15:04:05")+" · Connection capped at 15 minutes").Label("OIDC expiry").FontSize(12)
			}
			ui.TextInput(c.Key("oidc.confirm"), &w.oidcConfirmation).Label("Confirm OIDC context").Placeholder("Type exact context name; confirmation is cleared after each step").Disabled(w.oidcPending)
			enabled := !w.oidcPending && !w.vaultPending && !w.connectionPending && w.backend == nil && w.oidcConfirmation == w.oidcTarget.Context
			ui.Row(c).Gap(8).Children(func() {
				ui.Button(c, "Sign in with browser").Disabled(!enabled || w.oidcReview == nil).OnClick(func() { w.loginOIDC(services) })
				ui.Button(c, "Connect verified identity").Disabled(!enabled || w.oidcIdentity == nil).OnClick(func() { w.connectOIDC() })
			})
		}
		ui.Text(c, w.oidcStatus).Label("OIDC sign-in status").FontSize(12).TextColor(t.TextMuted)
		ui.Text(c, "Public client + S256 PKCE · same-origin HTTPS endpoints · single-use exchange · opt-in rotation only · no persistence, automatic refresh or issuer logout").FontSize(11).TextColor(t.TextMuted)
	})
}
