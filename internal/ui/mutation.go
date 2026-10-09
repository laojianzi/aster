package uiworkbench

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/operation"
)

// Cancel only unsent preview work. Submitted writes retain their independent
// outcome and may be Unknown; this must never imply rollback.
func (w *Workbench) stopPreview() {
	w.previewEpoch++
	if w.previewCancel != nil {
		w.previewCancel()
		w.previewCancel = nil
	}
	w.preparing = false
}

func (w *Workbench) prepare(kind string) {
	if w.detail == nil || w.ops == nil || w.preparing {
		return
	}
	if kind == "apply" && !w.applyAcknowledged {
		w.detailMessage = "Acknowledge field omission before previewing apply."
		return
	}
	epoch, revision := w.detailEpoch, w.draftRevision
	target, service, text, replicas := w.target(), w.ops, []byte(w.editor), w.replicas
	if kind == "apply" {
		text = []byte(w.applyDraft)
	}
	if kind == "create" {
		obj, err := manifest.Decode(text)
		if err != nil {
			w.detailMessage = err.Error()
			return
		}
		target.Name = obj.GetName()
	}
	ctx, cancel := context.WithTimeout(w.operationContext(), 30*time.Second)
	w.previewEpoch++
	previewEpoch := w.previewEpoch
	w.previewCancel = cancel
	w.preparing, w.plan, w.confirmation, w.detailMessage = true, nil, "", "Performing server-side dry-run…"
	w.run(func(context.Context) {
		defer cancel()
		var plan *operation.Prepared
		var err error
		switch kind {
		case "create":
			plan, err = service.PrepareCreate(ctx, target, text)
		case "apply":
			plan, err = service.PrepareApply(ctx, target, text)
		case "edit":
			plan, err = service.PrepareEdit(ctx, target, text)
		case "delete":
			plan, err = service.PrepareDelete(ctx, target)
		case "scale":
			var n int64
			n, err = parseReplicaCount(replicas)
			if err == nil {
				plan, err = service.PrepareScale(ctx, target, n)
			}
		case "restart":
			plan, err = service.PrepareRestart(ctx, target)
		default:
			err = errors.New("unknown operation")
		}
		diff := ""
		if err == nil {
			if target.GVR.Resource == "secrets" {
				diff = "Delete Secret " + target.Namespace + "/" + target.Name + "\nSecret payload is not included in the preview."
			} else {
				before, after := plan.Preview()
				var changes []manifest.Change
				changes, err = manifest.Compare(before, after)
				if err == nil {
					diff = manifest.Summary(changes)
					if diff == "" {
						diff = "No field changes after server-side validation."
					}
					if kind == "apply" {
						var ownership string
						ownership, err = manifest.OwnershipReview(before, after)
						diff = "APPLY · aster-apply · force disabled\nOmitted previously owned fields may be removed.\n\n" + diff + ownership
					}
				}
			}
		}
		w.emit(func() {
			if epoch != w.detailEpoch || previewEpoch != w.previewEpoch {
				return
			}
			w.preparing = false
			w.previewCancel = nil
			if revision != w.draftRevision {
				w.detailMessage = "Draft changed; prepare a new preview."
				return
			}
			if err != nil {
				w.detailMessage = err.Error()
				return
			}
			w.plan, w.diff, w.detailMode = plan, diff, "Diff"
			w.detailMessage = "Review " + kind + " for " + target.Namespace + "/" + target.Name + " · expires " + plan.Expires().Local().Format("15:04:05")
		})
	})
}
func (w *Workbench) execute() {
	if w.plan == nil || w.ops == nil || w.pendingWrites > 0 {
		return
	}
	plan, service, epoch, contextName := w.plan, w.ops, w.detailEpoch, w.activeContext
	target := plan.Target()
	if w.confirmation != target.Name {
		return
	}
	w.plan = nil
	w.pendingWrites++
	w.detailMessage = "Submitting reviewed change…"
	parent := w.operationContext()
	w.run(func(context.Context) {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		result, err := service.Execute(ctx, plan)
		w.emit(func() {
			w.pendingWrites--
			line := fmt.Sprintf("%s [%s] %s %s/%s · %s", plan.ID()[:12], contextName, plan.Kind(), target.Namespace, target.Name, result.State)
			w.recordHistory(line)
			if epoch != w.detailEpoch {
				return
			}
			if err != nil {
				w.detailMessage = result.State + ": " + err.Error()
			} else {
				w.detailMessage = "API accepted the change. Workload readiness is a separate observation."
				if result.Object != nil {
					w.creating, w.detail = false, result.Object.DeepCopy()
					w.applyDraft, w.ownersText, w.applyAcknowledged = "", "", false
					if text, displayErr := manifest.Display(w.detail, false); displayErr == nil {
						w.detailText, w.editor = text, text
					}
					w.detailMode = "YAML"
					w.startHealthTracking(result.Object, plan.ID())
				}
			}
			w.confirmation = ""
		})
	})
}
