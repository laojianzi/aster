package uiworkbench

import (
	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/operation"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func (w *Workbench) beginApply() {
	if w.detail == nil || w.creating || w.detail.GetKind() == "Secret" {
		return
	}
	w.stopPreview()
	w.plan, w.confirmation, w.diff = nil, "", ""
	w.applyAcknowledged = false
	w.draftRevision++
	if w.applyDraft == "" {
		metadata := map[string]interface{}{"name": w.detail.GetName()}
		if ns := w.detail.GetNamespace(); ns != "" {
			metadata["namespace"] = ns
		}
		intent := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": w.detail.GetAPIVersion(), "kind": w.detail.GetKind(), "metadata": metadata}}
		text, err := manifest.Display(intent, false)
		if err != nil {
			w.detailMessage = err.Error()
			return
		}
		w.applyDraft = text
	}
	w.detailMode = "Apply"
	w.detailMessage = "Provide minimal declarative intent, not a copy of the live object. New resources use Create."
}

func (w *Workbench) applyView(c *ui.Context) ui.Element {
	ui.Text(c, "Existing resource; manager "+operation.ApplyFieldManager+"; never force conflicts").FontSize(12 * max(1, c.Preferences().TextScale))
	ui.Text(c, "Omitted fields previously owned by this manager can be deleted or defaulted.").FontSize(12 * max(1, c.Preferences().TextScale))
	if ui.TextArea(c.Key("field.applyDraft"), &w.applyDraft).Label("Apply intent YAML").Font("monospace").Grow(1).Changed() {
		w.stopPreview()
		w.draftRevision++
		w.applyAcknowledged = false
		w.plan = nil
		w.confirmation = ""
		w.diff = ""
	}
	if ui.Checkbox(c.Key("field.applyAcknowledged"), &w.applyAcknowledged, "I understand omitted fields may be removed").Changed() {
		w.stopPreview()
		w.draftRevision++
		w.plan = nil
		w.confirmation = ""
		w.diff = ""
	}
	return ui.PrimaryButton(c, "Preview apply").Disabled(w.preparing || !w.applyAcknowledged).OnClick(func() {
		w.prepare("apply")
	})
}
