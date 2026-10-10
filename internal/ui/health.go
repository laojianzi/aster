package uiworkbench

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/health"
	"github.com/laojianzi/aster/internal/operation"
	"github.com/laojianzi/aster/internal/resource"
	"github.com/laojianzi/aster/internal/rollout"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func (w *Workbench) stopHealth() {
	w.healthEpoch++
	if w.healthCancel != nil {
		w.healthCancel()
		w.healthCancel = nil
	}
	w.healthActive = false
}

func (w *Workbench) loadHealth() {
	if w.detail == nil || w.backend == nil || w.creating {
		return
	}
	w.stopLogs()
	w.stopHealth()
	epoch, revision := w.detailEpoch, w.healthEpoch
	backend, target, parent := w.backend, w.target(), w.operationContext()
	w.detailMode, w.healthText = "Health", "Loading current status…"
	w.run(func(context.Context) {
		ctx, cancel := context.WithTimeout(parent, 15*time.Second)
		defer cancel()
		obj, err := backend.GetObject(ctx, target.GVR, target.Namespace, target.Name)
		if err == nil && obj.GetUID() != target.UID {
			err = operation.ErrConflict
		}
		text := ""
		if err == nil {
			text = health.Assess(obj).String()
		}
		w.emit(func() {
			if epoch != w.detailEpoch || revision != w.healthEpoch {
				return
			}
			if err != nil {
				w.healthText = "Health unavailable: " + err.Error()
			} else {
				w.healthText = text
			}
		})
	})
}

// Start from the object returned by the accepted mutation. Pin both its UID
// and generation; never reinterpret a later independent rollout as this one.
func (w *Workbench) startHealthTracking(obj *unstructured.Unstructured, operationID string) {
	if obj == nil || w.backend == nil || !health.Supports(obj) {
		return
	}
	w.stopLogs()
	w.stopHealth()
	epoch, revision := w.detailEpoch, w.healthEpoch
	target := resource.Identity{SessionID: w.sessionID, GVR: w.detailKind.GVR, Namespace: obj.GetNamespace(), Name: obj.GetName(), UID: obj.GetUID()}
	backend, generation, contextName := w.backend, obj.GetGeneration(), w.activeContext
	ctx, cancel := context.WithCancel(w.operationContext())
	w.healthCancel, w.healthActive, w.detailMode = cancel, true, "Health"
	w.healthText = "Tracking the current generation. Observation stops after five minutes."
	w.run(func(context.Context) {
		defer cancel()
		observer := rollout.New(backend, rollout.Options{})
		final, err := observer.Observe(ctx, target, generation, func(observation rollout.Observation) {
			w.emit(func() {
				if epoch == w.detailEpoch && revision == w.healthEpoch && ctx.Err() == nil {
					w.healthText = observation.String()
				}
			})
		})
		w.emit(func() {
			if epoch != w.detailEpoch || revision != w.healthEpoch {
				return
			}
			w.healthActive = false
			w.healthText = final.String()
			if err != nil && final.State == rollout.Unavailable {
				w.healthText += "\n\n" + err.Error()
			}
			if operationID != "" {
				w.recordHistory(fmt.Sprintf("%s [%s] readiness %s/%s · %s", operationID[:min(12, len(operationID))], contextName, target.Namespace, target.Name, final.State))
			}
		})
	})
}

func (w *Workbench) healthView(c *ui.Context) {
	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, "Refresh health").Disabled(w.healthActive).OnClick(func() {
			w.loadHealth()
		})
		ui.Button(c, "Track readiness").Disabled(w.healthActive || w.detail == nil || !health.Supports(w.detail)).OnClick(func() {
			w.startHealthTracking(w.detail.DeepCopy(), "")
		})
		ui.Button(c, "Stop tracking").Disabled(!w.healthActive).OnClick(func() {
			w.stopHealth()
			w.healthText += "\n\nObservation stopped. The Kubernetes change was not undone."
		})
	})
	ui.Text(c, "Read-only observation · no automatic rollback or command replay").FontSize(11)
	ui.TextArea(c.Key("field.healthText"), &w.healthText).Label("Workload health").ReadOnly(true).Grow(1)
}

func (w *Workbench) recordHistory(line string) {
	w.history = append(w.history, line)
	if len(w.history) > 100 {
		w.history = append([]string(nil), w.history[len(w.history)-100:]...)
	}
}
