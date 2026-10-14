package uiworkbench

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/delivery"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/operation"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

type deliveryAttempt struct {
	ID, Kind, State string
}

const (
	maxDeliveryAttempts      = 16
	maxDeliveryClipboardLine = 8192
)

type deliveryOutcome struct {
	State    string
	Attempts []deliveryAttempt
	UID      types.UID
	ID       string
	Dropped  int
}

func (o *deliveryOutcome) appendAttempt(attempt deliveryAttempt) int {
	if len(o.Attempts) == maxDeliveryAttempts {
		copy(o.Attempts, o.Attempts[1:])
		o.Attempts = o.Attempts[:maxDeliveryAttempts-1]
		o.Dropped++
	}
	o.Attempts = append(o.Attempts, attempt)
	return len(o.Attempts) - 1
}

func boundedDeliveryStatus(message string) string {
	count := 0
	for offset := range message {
		if count == 2048 {
			return message[:offset] + "… (status shortened)"
		}
		count++
	}
	return message
}

type deliveryBinding struct {
	context, epoch, revision uint64
	session, namespace       string
	ops                      *operation.Service
	set                      *delivery.Set
}

// The window owns progress. The imported Set and issued Prepared never expose
// mutable payloads, and workers receive only captured immutable inputs.
type deliveryState struct {
	helm                                     helmRenderState
	origin                                   string
	open, halted, acknowledged, showOutcomes bool
	editor                                   *ui.TextBuffer
	observed                                 ui.TextSnapshot
	revision, epoch                          uint64
	composing, confirmationComposing         bool
	busy, status, diff, confirmation         string
	retainedPaste                            string
	cancel                                   context.CancelFunc
	set                                      *delivery.Set
	outcomes                                 []deliveryOutcome
	plan                                     *operation.Prepared
	planBinding                              deliveryBinding
	inventory                                ui.ListState
	selected                                 int
	writeID                                  string
}

func (w *Workbench) deliveryBinding() deliveryBinding {
	d := &w.delivery
	return deliveryBinding{w.contextEpoch, d.epoch, d.revision, w.sessionID, strings.TrimSpace(w.namespace), w.ops, d.set}
}

func (w *Workbench) clearDelivery() {
	d := &w.delivery
	if d.cancel != nil {
		d.cancel()
	}
	w.delivery = deliveryState{epoch: d.epoch + 1}
}

func (w *Workbench) openDelivery() {
	if w.backend == nil || w.ops == nil || w.pendingWrites != 0 {
		return
	}
	w.clearDetail()
	w.clearVault()
	w.vaultOpen = false
	w.clearOIDC()
	w.oidcOpen = false
	d := &w.delivery
	d.open = true
	if d.editor == nil {
		d.editor = ui.NewTextBuffer("")
		d.observed = d.editor.Snapshot()
		d.status = "Paste already rendered YAML, then import it for review."
	}
	d.inventory.Selected = &d.selected
}

func (w *Workbench) closeDelivery() {
	w.cancelDelivery()
	w.delivery.open = false
	w.notice = "Delivery retained in this workspace. Reopen Deliver manifests to continue; accepted objects are not rolled back."
}

func (w *Workbench) stopDeliveryReview() {
	d := &w.delivery
	if d.plan != nil {
		if next := d.next(); next >= 0 && d.outcomes[next].State == "Ready" {
			d.outcomes[next].State = "Unreviewed"
		}
	}
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	d.epoch++
	d.helm.result = nil
	d.helm.replace = false
	d.plan, d.planBinding = nil, deliveryBinding{}
	d.confirmation, d.diff = "", ""
	d.confirmationComposing = false
	if d.busy != "Submitting" {
		d.busy = ""
	}
}

func (w *Workbench) deliverySelectionChanged() {
	w.stopDeliveryReview()
	w.delivery.status = "Selection changed; prior approval was cleared. Preview next object always reviews the first unfinished item."
}

// Pausing never describes a submitted request as rolled back. Its callback may
// still record the outcome for this exact set/attempt, but cannot restore a plan.
func (w *Workbench) cancelDelivery() {
	d := &w.delivery
	wasBusy := d.busy
	hadPlan := d.plan != nil
	w.stopDeliveryReview()
	d.halted = d.set != nil
	if d.set != nil && wasBusy != "Submitting" && (wasBusy != "" || hadPlan) {
		if next := d.next(); next >= 0 {
			d.outcomes[next].State = "Cancelled"
			d.outcomes[next].appendAttempt(deliveryAttempt{Kind: "preview", State: "Cancelled"})
		}
	}
	if wasBusy == "Submitting" {
		d.status = "Cancellation requested. The API may have accepted the request; no rollback is implied."
	} else if d.set != nil && d.next() < 0 {
		d.halted = false
		d.status = "All imported objects have been accepted. Workload readiness remains a separate observation."
	} else if d.set != nil {
		d.status = "Delivery paused. Review remaining again before another submission; accepted objects remain accepted."
	} else {
		d.status = "Import cancelled. The full input is retained."
	}
}

func (w *Workbench) changeDeliveryManifests() {
	d := &w.delivery
	if d.busy == "Submitting" || w.pendingWrites != 0 {
		return
	}
	w.stopDeliveryReview()
	d.set = nil
	d.acknowledged, d.halted, d.showOutcomes = false, false, false
	d.status = "Input unlocked. Import creates a new ordered set; previous submitted outcomes remain in session history."
}

func (w *Workbench) deliveryNamespaceChanged() {
	d := &w.delivery
	if d.editor == nil {
		return
	}
	w.stopDeliveryReview()
	d.set = nil
	d.acknowledged = false
	d.halted = true
	d.status = "Namespace changed. Import again for the explicitly selected namespace. Submitted requests are not rolled back."
}

func sameDeliveryText(a, b ui.TextSnapshot) bool {
	if a == b {
		return true
	}
	if !deliverySnapshotWithinBudget(a) || a.ByteLen() != b.ByteLen() || a.Len() != b.Len() || a.LineCount() != b.LineCount() {
		return false
	}
	for start := 0; start < a.Len(); start += 1024 {
		end := min(start+1024, a.Len())
		if a.Slice(start, end) != b.Slice(start, end) {
			return false
		}
	}
	return true
}

func deliverySnapshotWithinBudget(snapshot ui.TextSnapshot) bool {
	return snapshot.ByteLen() <= delivery.MaxBytes && snapshot.LineCount() <= delivery.MaxNewlines+1
}

// Clipboard preflight is deliberately conservative: it uses insertion bounds
// without reconstructing a native selection. A taken paste leaves the control's
// buffer, selection and undo history alone, and retains one complete incoming
// string until explicitly discarded. IME/direct text events do not use OnPaste.
func (w *Workbench) preflightDeliveryPaste(binding deliveryBinding, editor *ui.TextBuffer, text string) bool {
	d := &w.delivery
	if binding != w.deliveryBinding() || d.editor != editor || !d.open || d.set != nil || d.busy != "" || d.retainedPaste != "" {
		return true
	}
	if !clipboardPasteExceedsBudget(editor.Snapshot(), text, delivery.MaxBytes, delivery.MaxNewlines, maxDeliveryClipboardLine) {
		return false
	}
	w.stopDeliveryReview()
	d.retainedPaste = text
	d.status = "Clipboard paste retained separately; the original draft is unchanged. Discard the retained paste to resume import."
	return true
}

func (w *Workbench) syncDelivery() {
	d := &w.delivery
	if d.editor == nil {
		return
	}
	snapshot := d.editor.Snapshot()
	if snapshot == d.observed {
		return
	}
	equal := sameDeliveryText(d.observed, snapshot)
	d.observed = snapshot
	if !equal {
		d.revision++
		d.origin = ""
		w.stopDeliveryReview()
		d.set = nil
		d.acknowledged = false
		d.halted = false
		d.status = "Manifests changed. Import again before previewing."
	}
}

func (w *Workbench) importDelivery() {
	w.syncDelivery()
	d := &w.delivery
	if !d.open || d.editor == nil || d.set != nil || d.busy != "" || d.composing || d.retainedPaste != "" || w.backend == nil || w.ops == nil || w.pendingWrites != 0 {
		return
	}
	snapshot := d.editor.Snapshot()
	if snapshot.ByteLen() == 0 || !deliverySnapshotWithinBudget(snapshot) {
		d.status = "Import requires nonempty manifests within 2 MiB / 160000 newline limits. The full input is retained."
		return
	}
	namespace := strings.TrimSpace(w.namespace)
	if namespace == "" || namespace == "*" {
		d.status = "Choose one explicit namespace before importing manifests."
		return
	}
	w.stopDeliveryReview()
	binding := w.deliveryBinding()
	backend := w.backend
	ctx, cancel := context.WithTimeout(w.operationContext(), 45*time.Second)
	d.cancel, d.busy, d.status = cancel, "Importing", "Discovering resource types with the current connection…"
	w.run(func(context.Context) {
		defer cancel()
		kinds, warnings, err := backend.Discover(ctx)
		var set *delivery.Set
		if err == nil {
			set, err = delivery.Parse(ctx, []byte(snapshot.String()), binding.session, namespace, kinds)
		}
		if err == nil {
			var currentNamespace = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
			ns, lookupErr := backend.GetObject(ctx, currentNamespace, "", namespace)
			err = lookupErr
			if err == nil && (ns == nil || ns.GetAPIVersion() != "v1" || ns.GetKind() != "Namespace" || ns.GetName() != namespace || ns.GetNamespace() != "" || ns.GetUID() == "" || ns.GetDeletionTimestamp() != nil) {
				err = errors.New("the selected namespace is unavailable or terminating")
			}
		}
		if err == nil {
			err = ctx.Err()
		}
		w.emit(func() {
			w.syncDelivery()
			if binding != w.deliveryBinding() {
				return
			}
			d := &w.delivery
			d.busy, d.cancel = "", nil
			if err != nil {
				d.status = boundedDeliveryStatus("Import failed: " + err.Error())
				return
			}
			d.set, d.halted, d.acknowledged = set, false, false
			d.outcomes = make([]deliveryOutcome, set.Len())
			for i := range d.outcomes {
				d.outcomes[i].State = "Unreviewed"
			}
			d.selected = 0
			d.showOutcomes = false
			d.status = fmt.Sprintf("Imported %d objects in source order. Preview and submit one object at a time.", set.Len())
			if len(warnings) != 0 {
				d.status += fmt.Sprintf(" %d API groups were unavailable; only freshly discovered kinds were accepted.", len(warnings))
			}
		})
	})
}

func (d *deliveryState) next() int {
	for i := range d.outcomes {
		if d.outcomes[i].State != "Succeeded" {
			return i
		}
	}
	return -1
}

func deliveryFailure(err error) string {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "Cancelled"
	case errors.Is(err, operation.ErrConflict), apierrors.IsConflict(err), apierrors.IsAlreadyExists(err):
		return "Conflict"
	default:
		return "Rejected"
	}
}

func (w *Workbench) previewDelivery(reviewAgain bool) {
	w.syncDelivery()
	d := &w.delivery
	if !d.open || d.set == nil || d.busy != "" || d.composing || w.ops == nil || w.pendingWrites != 0 || d.halted && !reviewAgain {
		return
	}
	if d.set.SessionID() != w.sessionID || d.set.Namespace() != strings.TrimSpace(w.namespace) {
		w.deliveryNamespaceChanged()
		return
	}
	index := d.next()
	if index < 0 {
		return
	}
	w.stopDeliveryReview()
	binding := w.deliveryBinding()
	set, service, acknowledged := d.set, w.ops, d.acknowledged
	ctx, cancel := context.WithTimeout(w.operationContext(), 30*time.Second)
	d.cancel, d.busy, d.status = cancel, "Previewing", "Reading current state and performing server-side dry-run…"
	d.selected = index
	w.run(func(context.Context) {
		defer cancel()
		plan, err := delivery.Prepare(ctx, service, set, index, acknowledged)
		diff := ""
		if err == nil {
			before, after := plan.Preview()
			var changes []manifest.Change
			changes, err = manifest.Compare(before, after)
			if err == nil {
				diff = manifest.Summary(changes)
				if diff == "" {
					diff = "No field changes after server-side validation."
				}
				if plan.Kind() == "apply" {
					var owners string
					owners, err = manifest.OwnershipReview(before, after)
					diff = "APPLY · aster-apply · force disabled\nOmitted previously owned fields may be removed.\n\n" + diff + owners
				} else {
					diff = "CREATE ONLY · aster-desktop\nA concurrent existing object will not be overwritten.\n\n" + diff
				}
			}
		}
		if err == nil {
			err = ctx.Err()
		}
		w.emit(func() {
			w.syncDelivery()
			if binding != w.deliveryBinding() {
				return
			}
			d := &w.delivery
			d.busy, d.cancel = "", nil
			if err != nil {
				state := deliveryFailure(err)
				d.outcomes[index].State = state
				d.outcomes[index].appendAttempt(deliveryAttempt{Kind: "preview", State: state})
				d.halted = true
				d.status = boundedDeliveryStatus("Preview " + state + ": " + err.Error() + " Delivery paused; review remaining again.")
				return
			}
			d.plan, d.planBinding, d.diff = plan, binding, diff
			d.outcomes[index].State = "Ready"
			d.halted, d.showOutcomes = false, false
			d.status = "Review " + plan.Kind() + " for " + plan.Target().Namespace + "/" + plan.Target().Name + " · expires " + plan.Expires().Local().Format("15:04:05")
		})
	})
}

func (w *Workbench) executeDelivery() {
	w.syncDelivery()
	d := &w.delivery
	if !d.open || d.plan == nil || d.set == nil || d.busy != "" || d.halted || w.ops == nil || w.pendingWrites != 0 || d.confirmationComposing {
		return
	}
	if d.planBinding != w.deliveryBinding() || d.confirmation != d.plan.Target().Name {
		return
	}
	index := d.next()
	if index < 0 || d.selected != index {
		return
	}
	plan, service, set, contextName := d.plan, w.ops, d.set, w.activeContext
	connection, session := w.contextEpoch, w.sessionID
	d.plan, d.confirmation, d.confirmationComposing = nil, "", false
	d.epoch++
	epoch := d.epoch
	ctx, cancel := context.WithTimeout(w.operationContext(), 30*time.Second)
	d.cancel, d.busy, d.writeID = cancel, "Submitting", plan.ID()
	d.status = "Submitting one reviewed object. Cancellation cannot guarantee rollback."
	d.outcomes[index].State = "Submitting"
	attempt := d.outcomes[index].appendAttempt(deliveryAttempt{ID: plan.ID(), Kind: plan.Kind(), State: "Submitting"})
	w.pendingWrites++
	w.run(func(context.Context) {
		defer cancel()
		result, err := service.Execute(ctx, plan)
		w.emit(func() {
			w.pendingWrites--
			target := plan.Target()
			w.recordHistory(fmt.Sprintf("%s [%s] %s %s/%s · %s", plan.ID()[:12], contextName, plan.Kind(), target.Namespace, target.Name, result.State))
			d := &w.delivery
			ownedWrite := d.writeID == plan.ID()
			if ownedWrite {
				d.busy, d.cancel, d.writeID = "", nil, ""
			}
			if connection != w.contextEpoch || session != w.sessionID || service != w.ops || set != d.set || !ownedWrite {
				return
			}
			// A cancelled/closed view may receive its own submitted outcome, but
			// never a replacement plan or automatic next-object submission.
			d.outcomes[index].Attempts[attempt].State = result.State
			d.outcomes[index].State = result.State
			if err != nil {
				d.halted = true
				d.status = boundedDeliveryStatus(result.State + ": " + err.Error() + " Delivery paused. Review remaining again; no request is automatically retried.")
				return
			}
			d.outcomes[index].ID = plan.ID()
			if result.Object != nil {
				d.outcomes[index].UID = result.Object.GetUID()
			}
			d.halted = d.next() >= 0 && (d.halted || epoch != d.epoch)
			d.status = "API accepted this object. Workload readiness is separate."
			if d.halted {
				d.status += " Delivery remains paused; review remaining again."
			} else if d.next() >= 0 {
				d.status += " Choose Preview next object to continue."
			} else {
				d.status += " All imported objects have been accepted."
			}
		})
	})
}

func (w *Workbench) inspectDeliveryResource() {
	d := &w.delivery
	index := d.selected
	if d.set == nil || d.busy != "" || index < 0 || index >= len(d.outcomes) || d.outcomes[index].State != "Succeeded" || d.outcomes[index].UID == "" {
		return
	}
	item, outcome := d.set.Items()[index], d.outcomes[index]
	w.closeDelivery()
	w.openResourceKind(item.Kind, resourceRow{Name: item.Name, Namespace: item.Namespace, UID: string(outcome.UID)})
}

func (d *deliveryState) outcomeText() string {
	var b strings.Builder
	if d.set == nil {
		return "Import a set to review its per-object outcomes."
	}
	for i, item := range d.set.Items() {
		fmt.Fprintf(&b, "%d. %s %s/%s · %s\n", i+1, item.Kind.Kind, item.Namespace, item.Name, d.outcomes[i].State)
		if dropped := d.outcomes[i].Dropped; dropped != 0 {
			fmt.Fprintf(&b, "   %d earlier attempt records omitted; latest %d retained.\n", dropped, maxDeliveryAttempts)
		}
		for _, a := range d.outcomes[i].Attempts {
			id := a.ID
			if len(id) > 12 {
				id = id[:12]
			}
			fmt.Fprintf(&b, "   %s %s · %s\n", a.Kind, id, a.State)
		}
	}
	return b.String()
}

func (w *Workbench) deliveryView(c *ui.Context) {
	d := &w.delivery
	t := c.Theme()
	ui.Column(c.Key("delivery.panel")).Grow(1).Padding(12).Gap(8).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "Deliver manifests").FontSize(20).Bold().Grow(1)
			ui.Button(c, "Cancel delivery work").Disabled(d.busy == "" && d.plan == nil).OnClick(w.cancelDelivery)
			ui.Button(c, "Back to resources").OnClick(w.closeDelivery)
		})
		namespace := strings.TrimSpace(w.namespace)
		if d.set != nil {
			namespace = d.set.Namespace()
		}
		ui.Text(c, "Context: "+w.activeContext+" · Delivery namespace: "+namespace).Label("Delivery identity").FontSize(12).SingleLine()
		ui.Text(c, "Rendered YAML · 2 MiB / 160000 newlines / 32 objects · one namespace. Back pauses and retains this set.").FontSize(11).SingleLine()
		if d.origin != "" {
			ui.Text(c, d.origin).Label("Delivery render origin").FontSize(10).SingleLine()
		}
		if d.set == nil {
			if d.helm.open {
				w.helmRenderView(c)
			}
			if d.retainedPaste != "" {
				ui.Text(c, fmt.Sprintf("Clipboard paste retained separately (%d bytes); original draft unchanged. Import is paused. Clipboard preflight also limits potential lines to 8192 bytes.", len(d.retainedPaste))).Label("Delivery retained paste").FontSize(12)
				ui.Button(c, "Discard retained paste").OnClick(func() {
					w.stopDeliveryReview()
					d.retainedPaste = ""
					d.status = "Retained clipboard paste discarded. The original draft is unchanged."
				})
			}
			if !deliverySnapshotWithinBudget(d.editor.Snapshot()) {
				ui.Text(c, "Input exceeds the 2 MiB / 160000 newline budget. Full text is retained; layout and import are suspended.").Label("Delivery budget exceeded").Grow(1)
				ui.Button(c, "Clear oversized manifests").Disabled(d.busy != "" || w.pendingWrites != 0).OnClick(func() {
					d.editor = ui.NewTextBuffer("")
					w.syncDelivery()
				})
			} else {
				editor, binding := d.editor, w.deliveryBinding()
				e := ui.TextAreaBuffer(c.Key("delivery.source"), editor).Label("Delivery manifests").Grow(1).ReadOnly(d.busy != "" || d.retainedPaste != "").OnPaste(func(text string) bool {
					return w.preflightDeliveryPaste(binding, editor, text)
				})
				d.composing = e.Composing()
				if e.Changed() {
					w.syncDelivery()
				}
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.PrimaryButton(c, "Import manifests").Disabled(d.busy != "" || d.composing || d.retainedPaste != "" || w.pendingWrites != 0 || d.editor.Snapshot().ByteLen() == 0 || !deliverySnapshotWithinBudget(d.editor.Snapshot())).OnClick(w.importDelivery)
				if !d.helm.open {
					w.helmRenderView(c)
				}
			})
		} else {
			items := d.set.Items()
			cols := []ui.TableColumn{{ID: "object", Title: "Source order / object"}, {ID: "kind", Title: "Kind", Width: 180}, {ID: "namespace", Title: "Namespace", Width: 140}, {ID: "state", Title: "State", Width: 125}}
			inventory := ui.Table(c.Key("delivery.inventory"), &d.inventory, cols, len(items), func(row, col int) {
				item := items[row]
				switch col {
				case 0:
					ui.Button(c, fmt.Sprintf("%d. %s", row+1, item.Name)).Disabled(d.busy == "Submitting").OnClick(func() {
						if d.selected != row {
							d.selected = row
							w.deliverySelectionChanged()
						}
					})
				case 1:
					ui.Text(c, item.Kind.Label()).SingleLine()
				case 2:
					ui.Text(c, item.Namespace).SingleLine()
				case 3:
					ui.Text(c, d.outcomes[row].State).SingleLine()
				}
			}).Label("Delivery inventory").Height(135).Disabled(d.busy == "Submitting")
			if inventory.Changed() {
				w.deliverySelectionChanged()
			}
			if ui.Checkbox(c.Key("delivery.acknowledged"), &d.acknowledged, "I understand omitted fields may be removed").Disabled(d.busy != "").Changed() {
				w.stopDeliveryReview()
				d.status = "Acknowledgement changed. Preview again before submission."
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.Button(c, "Review details").OnClick(func() { d.showOutcomes = false })
				ui.Button(c, "Delivery outcomes").OnClick(func() { d.showOutcomes = true })
				ui.Text(c, "Imported input is locked until Change manifests.").FontSize(11).TextColor(t.TextMuted).Grow(1).SingleLine()
			})
			text := d.diff
			if text == "" {
				text = "Choose Preview next object. GET and dry-run do not submit a change."
			}
			if d.showOutcomes {
				text = d.outcomeText()
			}
			ui.Scroll(c.Key("delivery.preview.scroll")).Grow(1).Children(func() { ui.Text(c, text).Label("Delivery preview").FontSize(12) })
			ui.Row(c).Gap(8).Children(func() {
				e := ui.TextInput(c.Key(fmt.Sprintf("delivery.confirmation.%d", d.epoch)), &d.confirmation).Label("Delivery confirmation").Placeholder("Type the exact reviewed resource name").Grow(1).Disabled(d.plan == nil || d.busy != "")
				d.confirmationComposing = e.Composing()
				ui.PrimaryButton(c, "Submit reviewed object").Disabled(d.plan == nil || d.busy != "" || d.halted || d.confirmationComposing || w.pendingWrites != 0 || d.plan != nil && d.confirmation != d.plan.Target().Name).OnClick(w.executeDelivery)
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.Button(c, "Preview next object").Disabled(d.busy != "" || d.halted || d.next() < 0 || w.pendingWrites != 0).OnClick(func() { w.previewDelivery(false) })
				ui.Button(c, "Review remaining again").Disabled(d.busy != "" || !d.halted || d.next() < 0 || w.pendingWrites != 0).OnClick(func() { w.previewDelivery(true) })
				inspectable := d.selected >= 0 && d.selected < len(d.outcomes) && d.outcomes[d.selected].State == "Succeeded" && d.outcomes[d.selected].UID != ""
				ui.Button(c, "Inspect accepted resource").Disabled(d.busy != "" || !inspectable).OnClick(w.inspectDeliveryResource)
				ui.Button(c, "Change manifests").Disabled(d.busy == "Submitting" || w.pendingWrites != 0).OnClick(w.changeDeliveryManifests)
			})
		}
		ui.Scroll(c.Key("delivery.status.scroll")).Height(48).Children(func() {
			ui.Text(c, d.status).Label("Delivery status").FontSize(11)
		})
	})
}
