package uiworkbench

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/laojianzi/aster/internal/credentialexec"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/operation"
)

func (w *Workbench) loadContexts() {
	path := w.path
	w.disconnect()
	epoch := w.contextEpoch
	w.run(func(ctx context.Context) {
		items, current, err := kubeconfig.Contexts(path)
		w.emit(func() {
			if epoch != w.contextEpoch {
				return
			}
			if err != nil {
				w.errText = err.Error()
				return
			}
			w.contexts = nil
			for _, item := range items {
				w.contexts = append(w.contexts, item.Name)
			}
			w.currentContext = current
			w.trustedFingerprint = ""
			w.trustRequired = false
			if len(items) == 0 {
				w.status = "No kubeconfig contexts"
				return
			}
			if w.currentContext == "" {
				w.currentContext = items[0].Name
			}
			w.status = "Choose a context and connect"
		})
	})
}

func (w *Workbench) disconnect() {
	w.clearVault()
	w.contextEpoch++
	w.scopeEpoch++
	if w.connectionCancel != nil {
		w.connectionCancel()
		w.connectionCancel = nil
	}
	w.activeContext, w.activeNamespace, w.notice = "", "", ""
	w.credentialExpiry = time.Time{}
	w.kinds = catalog()
	if w.scopeCancel != nil {
		w.scopeCancel()
		w.scopeCancel = nil
	}
	w.clearDetail()
	w.backend, w.ops, w.rows, w.store = nil, nil, nil, nil
	w.total, w.selected = 0, -1
	w.status, w.errText = "Disconnected", ""
}

type connectionResolver func(context.Context, kubeconfig.Options) (kubeconfig.Connection, credentialexec.Resolved, error)

func (w *Workbench) connect() { w.beginConnection(nil) }

func (w *Workbench) beginConnection(resolve connectionResolver) {
	if w.connectionPending || w.vaultPending {
		return
	}
	w.connectionPending = true
	w.disconnect()
	w.status = "Connecting"
	epoch := w.contextEpoch
	connectionCtx, connectionCancel := context.WithCancel(w.ctx)
	w.connectionCtx, w.connectionCancel = connectionCtx, connectionCancel
	opts := kubeconfig.Options{Path: w.path, Context: w.currentContext, Namespace: w.namespace, TrustToken: w.trustedFingerprint}
	w.run(func(ctx context.Context) {
		var conn kubeconfig.Connection
		var err error
		var backend *kube.Backend
		var resolved credentialexec.Resolved
		sessionCtx := connectionCtx
		closeConnection := connectionCancel
		if resolve != nil {
			conn, resolved, err = resolve(connectionCtx, opts)
		} else {
			conn, err = kubeconfig.Load(opts)
			if err == nil {
				resolved, err = credentialexec.Resolve(connectionCtx, conn.Config)
			}
		}
		if err == nil && !resolved.ExpiresAt.IsZero() {
			var cancelLease context.CancelFunc
			sessionCtx, cancelLease = context.WithDeadline(connectionCtx, resolved.ExpiresAt)
			closeConnection = func() { cancelLease(); connectionCancel() }
		}
		if err == nil {
			backend, err = kube.New(resolved.Config)
		}
		var sessionBytes [16]byte
		if err == nil {
			_, err = rand.Read(sessionBytes[:])
		}
		w.emit(func() {
			// Admission stays closed until the previous authentication worker
			// has joined, including when Disconnect invalidated its epoch.
			w.connectionPending = false
			if epoch != w.contextEpoch {
				closeConnection()
				return
			}
			if err == nil {
				err = sessionCtx.Err()
			}
			if err != nil {
				closeConnection()
				w.status, w.errText = "Connection failed", err.Error()
				var trust *kubeconfig.TrustRequiredError
				w.trustRequired = errors.As(err, &trust)
				w.pendingTrustFingerprint = ""
				if w.trustRequired {
					w.pendingTrustFingerprint = trust.Fingerprint
				}
				return
			}
			w.connectionCtx, w.connectionCancel = sessionCtx, closeConnection
			w.credentialExpiry = resolved.ExpiresAt
			w.backend = backend
			w.activeContext, w.currentContext, w.namespace = conn.ContextName, conn.ContextName, conn.Namespace
			w.sessionID = hex.EncodeToString(sessionBytes[:])
			w.ops = operation.NewService(backend, w.sessionID)
			w.trustRequired, w.errText = false, ""
			w.startScope()
			if !resolved.ExpiresAt.IsZero() {
				w.run(func(context.Context) {
					<-sessionCtx.Done()
					expired := errors.Is(sessionCtx.Err(), context.DeadlineExceeded)
					closeConnection()
					if expired {
						w.emit(func() { w.expireConnection(epoch) })
					}
				})
			}
			w.run(func(ctx context.Context) {
				kinds, warnings, err := backend.Discover(sessionCtx)
				w.emit(func() {
					if epoch != w.contextEpoch {
						return
					}
					if err != nil {
						w.notice = "Discovery unavailable; built-in resources remain accessible"
						return
					}
					if len(kinds) > 0 {
						w.kinds = kinds
					}
					w.notice = fmt.Sprintf("%d resource types · %d unavailable API group(s)", len(kinds), len(warnings))
				})
			})
		})
	})
}

// expireConnection is UI-thread only. A delayed expiry cannot disconnect a
// later identity. Child contexts already stopped streams before UI dispatch.
func (w *Workbench) expireConnection(epoch uint64) {
	if epoch != w.contextEpoch {
		return
	}
	w.disconnect()
	w.status = "Credentials expired"
	w.errText = credentialexec.ErrExpired.Error()
}

func (w *Workbench) connectionStatus() string {
	if w.backend != nil && !w.credentialExpiry.IsZero() {
		return w.status + " · Credentials expire " + w.credentialExpiry.Local().Format("15:04:05") + " · Reconnect to renew"
	}
	return w.status
}
