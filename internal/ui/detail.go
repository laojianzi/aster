package uiworkbench

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/operation"
	"github.com/laojianzi/aster/internal/relationship"
	"github.com/laojianzi/aster/internal/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func (w *Workbench) clearDetail() {
	w.stopRelationships()
	w.relatedResult, w.relatedStatus = relationship.Snapshot{}, ""
	w.detailEpoch++
	w.draftRevision++
	w.eventsRevision++
	w.eventsText = ""
	w.stopCommand()
	w.commandArgv, w.commandContainer, w.commandOutput, w.commandStatus = "", "", "", ""
	w.stopHealth()
	w.healthText = ""
	w.stopForward()
	w.stopLogs()
	w.logRows, w.logDropped, w.logStatus = nil, 0, ""
	w.detail, w.plan = nil, nil
	w.diff, w.editor, w.detailText, w.confirmation, w.detailMessage = "", "", "", "", ""
	w.preparing, w.creating = false, false
}
func (w *Workbench) openResource(row resourceRow) {
	w.openResourceKind(w.currentKind, row)
}

// Related navigation changes only the detail target, not the active list's scope.
func (w *Workbench) openResourceKind(kind kube.ResourceKind, row resourceRow) {
	if w.backend == nil {
		return
	}
	w.clearDetail()
	epoch, scope := w.detailEpoch, w.scopeEpoch
	backend, parent := w.backend, w.operationContext()
	w.run(func(context.Context) {
		ctx, cancel := context.WithTimeout(parent, 20*time.Second)
		defer cancel()
		obj, err := backend.GetObject(ctx, kind.GVR, row.Namespace, row.Name)
		text := ""
		if err == nil && (obj == nil || string(obj.GetUID()) != row.UID || obj.GetName() != row.Name || obj.GetNamespace() != row.Namespace || obj.GroupVersionKind() != kind.GVR.GroupVersion().WithKind(kind.Kind)) {
			err = operation.ErrConflict
		}
		if err == nil {
			text, err = manifest.Display(obj, false)
		}
		w.emit(func() {
			if epoch != w.detailEpoch || scope != w.scopeEpoch {
				return
			}
			if err != nil {
				w.errText = err.Error()
				return
			}
			w.detail, w.detailKind, w.detailText, w.editor, w.detailMode = obj, kind, text, text, "YAML"
			w.containers = nil
			for _, field := range []string{"containers", "initContainers", "ephemeralContainers"} {
				items, _, _ := unstructured.NestedSlice(obj.Object, "spec", field)
				for _, value := range items {
					if m, ok := value.(map[string]interface{}); ok {
						if name, ok := m["name"].(string); ok {
							w.containers = append(w.containers, name)
						}
					}
				}
			}
			w.container = ""
			if len(w.containers) > 0 {
				w.container = w.containers[0]
			}
			w.replicas = "1"
			if n, ok, _ := unstructured.NestedInt64(obj.Object, "spec", "replicas"); ok {
				w.replicas = fmt.Sprint(n)
			}
		})
	})
}
func (w *Workbench) target() resource.Identity {
	return resource.Identity{SessionID: w.sessionID, GVR: w.detailKind.GVR, Namespace: w.detail.GetNamespace(), Name: w.detail.GetName(), UID: w.detail.GetUID()}
}
func (w *Workbench) loadEvents() {
	if w.detail == nil || w.backend == nil {
		return
	}
	w.stopLogs()
	epoch := w.detailEpoch
	w.eventsRevision++
	revision := w.eventsRevision
	backend, target, parent := w.backend, w.target(), w.operationContext()
	w.detailMode, w.eventsText = "Events", "Loading events…"
	w.run(func(context.Context) {
		ctx, cancel := context.WithTimeout(parent, 20*time.Second)
		defer cancel()
		items, _, err := backend.ListObjects(ctx, schema.GroupVersionResource{Version: "v1", Resource: "events"}, target.Namespace, metav1.ListOptions{FieldSelector: "involvedObject.uid=" + string(target.UID), Limit: 200})
		var lines []string
		for _, item := range items {
			reason, _, _ := unstructured.NestedString(item.Object, "reason")
			message, _, _ := unstructured.NestedString(item.Object, "message")
			lines = append(lines, reason+": "+message)
		}
		w.emit(func() {
			if epoch != w.detailEpoch || revision != w.eventsRevision {
				return
			}
			if err != nil {
				w.eventsText = "Events unavailable: " + err.Error()
			} else if len(lines) == 0 {
				w.eventsText = "No retained events for this resource UID."
			} else {
				w.eventsText = strings.Join(lines, "\n\n")
			}
		})
	})
}
