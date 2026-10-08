package uiworkbench

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (w *Workbench) chooseKind(kind kube.ResourceKind) { w.currentKind = kind; w.kindChoice = kind.Label(); w.startScope() }
func (w *Workbench) queryChanged() { if w.store != nil { w.store.setQuery(w.filter, w.sortBy) } }
func (w *Workbench) startScope() {
	w.scopeEpoch++
	epoch := w.scopeEpoch
	if w.scopeCancel != nil { w.scopeCancel() }
	w.clearDetail()
	w.rows, w.total, w.selected, w.errText = nil, 0, -1, ""
	if w.backend == nil { w.status = "Not connected"; return }
	ctx, cancel := context.WithCancel(w.operationContext())
	w.scopeCancel = cancel
	kind, backend := w.currentKind, w.backend
	ns := strings.TrimSpace(w.namespace)
	if !kind.Namespaced || ns == "*" { ns = "" }
	w.activeNamespace = ns
	if !kind.Namespaced { w.activeNamespace = "cluster-scoped" } else if ns == "" { w.activeNamespace = "all namespaces" }
	opts := metav1.ListOptions{LabelSelector: w.labelSelector}
	store := newRowStore()
	store.setQuery(w.filter, w.sortBy)
	w.store, w.status = store, "Synchronizing"
	w.run(func(context.Context) {
		err := backend.WatchWithStatus(ctx, kind.GVR, ns, opts, func(e kube.ResourceEvent) error { store.event(e); return nil }, func(status string) { store.setStatus(status, "") })
		if err != nil && ctx.Err() == nil { store.setStatus("Unavailable", err.Error()) }
	})
	w.run(func(context.Context) {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		var pending atomic.Bool
		for {
			select {
			case <-ctx.Done(): return
			case <-ticker.C:
				if !pending.CompareAndSwap(false, true) { continue }
				snapshot, changed := store.snapshot()
				if !changed { pending.Store(false); continue }
				w.emit(func() {
					defer pending.Store(false)
					if epoch != w.scopeEpoch || ctx.Err() != nil { return }
					if snapshot.Query != w.filter || snapshot.Sort != w.sortBy { store.setQuery(w.filter, w.sortBy); return }
					selectedKey := ""
					if w.selected >= 0 && w.selected < len(w.rows) { selectedKey = w.rows[w.selected].key() }
					w.rows, w.total, w.status, w.errText = snapshot.Rows, snapshot.Total, snapshot.Status, snapshot.Error
					w.selected = -1
					for i, r := range w.rows { if r.key() == selectedKey { w.selected = i; break } }
				})
			}
		}
	})
}
