package uiworkbench

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/operation"
	"github.com/laojianzi/aster/internal/relationship"
	"github.com/laojianzi/aster/internal/resource"
	"github.com/laojianzi/aster/internal/resourcemetrics"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

type objectTab struct {
	target resource.Identity
	kind   kube.ResourceKind
	row    resourceRow
}

type objectDraft struct {
	dirty                                bool
	resourceVersion                      string
	editor                               *ui.TextBuffer
	lastSafe                             ui.TextSnapshot
	hasSafe                              bool
	retainedPaste, apply, replicas, mode string
}

func (w *Workbench) clearDetail() {
	w.objectDrafts = nil
	w.objectTabs = nil
	w.objectTabScope = ""
	w.clearDiagnostics()
	w.clearObject()
}

func (w *Workbench) clearObject() {
	w.objectRequest = nil
	w.objectLoadError = ""
	w.clearFind(true)
	w.clearSchema()
	w.schemaPointer = "/spec"
	w.stopPreview()
	w.applyDraft, w.ownersText, w.applyAcknowledged = "", "", false
	w.stopMetrics()
	w.metricsResult = resourcemetrics.Snapshot{}
	w.metricsHistory = resourcemetrics.History{}
	w.stopRelationships()
	w.relatedResult, w.relatedStatus = relationship.Snapshot{}, ""
	w.relatedGroups = nil
	w.detailEpoch++
	w.draftRevision++
	w.eventsRevision++
	w.eventsText = ""
	w.stopHealth()
	w.healthText = ""
	w.detail, w.plan = nil, nil
	w.reviewReturnMode, w.reviewReturnFocus = "", false
	w.setEditor("")
	w.diff, w.detailText, w.confirmation, w.detailMessage = "", "", "", ""
	w.preparing, w.creating = false, false
}
func (w *Workbench) openResource(row resourceRow) {
	w.openResourceKind(w.currentKind, row)
}

func (w *Workbench) saveObjectDraft() {
	if w.detail == nil || w.creating || w.editor == nil || w.detail.GetKind() == "Secret" {
		return
	}
	current := w.target()

	w.syncEditor()
	changed := !sameManifestText(w.editor.Snapshot(), ui.NewTextBuffer(w.detailText).Snapshot())
	if w.objectDrafts == nil {
		w.objectDrafts = make(map[resource.Identity]objectDraft)
	}
	mode := w.detailMode
	if mode == "Diff" {
		mode = w.reviewReturnMode
	}
	if mode != "Edit" && mode != "Apply" && mode != "Scale" {
		mode = "Overview"
	}
	w.objectDrafts[current] = objectDraft{
		dirty:           changed || w.applyDraft != "" || w.editorRetainedPaste != "" || w.detailMode == "Scale",
		resourceVersion: w.detail.GetResourceVersion(),
		editor:          w.editor, lastSafe: w.editorLastSafe, hasSafe: w.editorHasSafe,
		retainedPaste: w.editorRetainedPaste, apply: w.applyDraft, replicas: w.replicas, mode: mode,
	}
}

func (w *Workbench) closeObjectTab() {
	if w.creating || (w.detail == nil && w.objectRequest == nil) {
		w.clearDetail()
		return
	}
	var target resource.Identity
	if w.objectRequest != nil {
		target = w.objectRequest.target
	}
	if w.detail != nil {
		target = w.target()
	}
	w.objectTabs = slices.DeleteFunc(w.objectTabs, func(tab objectTab) bool { return tab.target == target })
	delete(w.objectDrafts, target)
	w.errText = ""
	w.clearDiagnostics()
	w.clearObject()
}

// Related navigation changes only the detail target, not the active list's scope.
func (w *Workbench) openResourceKind(kind kube.ResourceKind, row resourceRow) {
	if w.backend == nil {
		return
	}
	target := resource.Identity{SessionID: w.sessionID, GVR: kind.GVR, Namespace: row.Namespace, Name: row.Name, UID: types.UID(row.UID)}
	if w.detail != nil && w.target() == target {
		delete(w.objectDrafts, target)
	} else {
		w.saveObjectDraft()
	}

	w.clearObject()
	w.objectRequest = &objectTab{target: target, kind: kind, row: row}
	w.errText = ""
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
				w.objectLoadError = err.Error()
				return
			}
			w.setEditor(text)
			w.detail, w.detailKind, w.detailText, w.detailMode = obj, kind, text, "Overview"
			if !slices.ContainsFunc(w.objectTabs, func(tab objectTab) bool { return tab.target == target }) {
				if len(w.objectTabs) == 0 {
					w.objectTabScope = strings.TrimSpace(w.namespace)
				}
				w.objectTabs = append(w.objectTabs, objectTab{target: target, kind: kind, row: row})
			}
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
			if w.diagnostic == nil {
				w.container = ""
				if len(w.containers) > 0 {
					w.container = w.containers[0]
				}
			}
			w.replicas = "1"
			if n, ok, _ := unstructured.NestedInt64(obj.Object, "spec", "replicas"); ok {
				w.replicas = fmt.Sprint(n)
			}
			if draft, ok := w.objectDrafts[target]; ok && (draft.dirty || draft.resourceVersion == obj.GetResourceVersion()) {
				w.editor, w.editorObserved = draft.editor, draft.editor.Snapshot()
				w.editorLastSafe, w.editorHasSafe = draft.lastSafe, draft.hasSafe
				w.editorRetainedPaste, w.applyDraft = draft.retainedPaste, draft.apply
				w.replicas, w.detailMode = draft.replicas, draft.mode
			} else {
				delete(w.objectDrafts, target)
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
