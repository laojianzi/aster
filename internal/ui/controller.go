package uiworkbench

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

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
	w.contextEpoch++
	w.scopeEpoch++
	if w.connectionCancel != nil {
		w.connectionCancel()
		w.connectionCancel = nil
	}
	w.activeContext, w.activeNamespace, w.notice = "", "", ""
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

func (w *Workbench) connect() {
	w.disconnect()
	w.status = "Connecting"
	epoch := w.contextEpoch
	connectionCtx, connectionCancel := context.WithCancel(w.ctx)
	w.connectionCtx, w.connectionCancel = connectionCtx, connectionCancel
	opts := kubeconfig.Options{Path: w.path, Context: w.currentContext, Namespace: w.namespace, TrustToken: w.trustedFingerprint}
	w.run(func(ctx context.Context) {
		conn, err := kubeconfig.Load(opts)
		var backend *kube.Backend
		if err == nil {
			backend, err = kube.New(conn.Config)
		}
		var sessionBytes [16]byte
		if err == nil {
			_, err = rand.Read(sessionBytes[:])
		}
		w.emit(func() {
			if epoch != w.contextEpoch {
				return
			}
			if err != nil {
				w.status, w.errText = "Connection failed", err.Error()
				var trust *kubeconfig.TrustRequiredError
				w.trustRequired = errors.As(err, &trust)
				w.pendingTrustFingerprint = ""
				if w.trustRequired {
					w.pendingTrustFingerprint = trust.Fingerprint
				}
				return
			}
			w.backend = backend
			w.activeContext, w.currentContext, w.namespace = conn.ContextName, conn.ContextName, conn.Namespace
			w.sessionID = hex.EncodeToString(sessionBytes[:])
			w.ops = operation.NewService(backend, w.sessionID)
			w.trustRequired, w.errText = false, ""
			w.startScope()
			w.run(func(ctx context.Context) {
				kinds, warnings, err := backend.Discover(connectionCtx)
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
