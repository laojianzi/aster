package uiworkbench

import (
	"context"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/helmrender"
)

type helmRenderState struct {
	open, trusted, replace, composing            bool
	archive, chart, values, release, kubeVersion string
	result                                       *helmrender.Result
	binding                                      deliveryBinding
}

func (w *Workbench) helmRequest() helmrender.Request {
	h := &w.delivery.helm
	return helmrender.Request{HelmArchive: h.archive, ChartArchive: h.chart, ValuesFile: h.values, Release: h.release, Namespace: strings.TrimSpace(w.namespace), KubeVersion: h.kubeVersion, Trusted: h.trusted}
}
func (w *Workbench) openHelmRender() {
	d := &w.delivery
	if d.set != nil || d.busy != "" || d.retainedPaste != "" || w.pendingWrites != 0 {
		return
	}
	w.stopDeliveryReview()
	d.helm.open = true
	if d.helm.release == "" {
		d.helm.release = "aster-render"
	}
	if d.helm.kubeVersion == "" {
		d.helm.kubeVersion = "1.37.0"
	}
	d.status = "Select trusted local files. Rendering has no cluster credentials; accepting output still requires Import and per-object review."
}
func (w *Workbench) helmInputsChanged() {
	w.stopDeliveryReview()
	w.delivery.helm.trusted = false
	w.delivery.helm.replace = false
	w.delivery.status = "Helm input changed. Confirm chart trust again; previous render result is invalid."
}
func (w *Workbench) renderHelm() {
	w.syncDelivery()
	d := &w.delivery
	h := &d.helm
	if !d.open || !h.open || !h.trusted || h.composing || d.composing || d.set != nil || d.retainedPaste != "" || d.busy != "" || w.pendingWrites != 0 || w.backend == nil || w.ops == nil {
		return
	}
	request := w.helmRequest()
	w.stopDeliveryReview()
	h.replace = false
	binding := w.deliveryBinding()
	ctx, cancel := context.WithCancel(w.operationContext())
	d.cancel, d.busy, d.status = cancel, "Rendering Helm", "Rendering local chart with isolated credentials and bounded input/output/time…"
	w.run(func(context.Context) {
		defer cancel()
		result, err := helmrender.Render(ctx, request)
		w.emit(func() {
			w.syncDelivery()
			if binding != w.deliveryBinding() || !w.delivery.open || !w.delivery.helm.open || request != w.helmRequest() {
				return
			}
			d := &w.delivery
			d.busy, d.cancel = "", nil
			if err != nil {
				d.status = err.Error()
				return
			}
			d.helm.result, d.helm.binding = result, binding
			d.status = "Helm output captured. It has not replaced the current input, been imported, previewed or submitted."
		})
	})
}
func (w *Workbench) useHelmResult() {
	w.syncDelivery()
	d := &w.delivery
	h := &d.helm
	if !d.open || !h.open || !h.replace || h.result == nil || h.binding != w.deliveryBinding() || d.set != nil || d.busy != "" || d.composing || h.composing || d.retainedPaste != "" || w.pendingWrites != 0 || h.result.Namespace() != strings.TrimSpace(w.namespace) {
		return
	}
	result := h.result
	w.stopDeliveryReview()
	// This explicit replacement starts a new input/review boundary. It is not an
	// edit transaction and does not claim to preserve the previous Undo history.
	d.editor = ui.NewTextBuffer(result.Manifest())
	d.observed = d.editor.Snapshot()
	d.revision++
	d.helm = helmRenderState{}
	d.origin = result.Summary()
	d.status = "Captured Helm output replaced the input. Import manifests next; then review and confirm each object. No release was installed."
}
func (w *Workbench) discardHelmResult() {
	w.stopDeliveryReview()
	w.delivery.helm.replace = false
	w.delivery.status = "Helm result discarded. Original manifest buffer and mounted history are unchanged."
}

func (w *Workbench) helmRenderView(c *ui.Context) {
	d := &w.delivery
	h := &d.helm
	if !h.open {
		ui.Button(c, "Render Helm chart").Disabled(d.busy != "" || d.retainedPaste != "" || w.pendingWrites != 0).OnClick(w.openHelmRender)
		return
	}
	ui.Column(c.Key("helm.render.panel")).Gap(4).Children(func() {
		ui.Text(c, "Local Helm · "+helmrender.ApprovedArchive()+" · chart .tgz · optional values YAML").FontSize(11).SingleLine()
		disabled := d.busy != "" || h.result != nil
		composing, changed := false, false
		field := func(key string, value *string, label, placeholder string) {
			e := guardedTextInput(c, key, value, !disabled).Label(label).Placeholder(placeholder).Grow(1).Disabled(disabled)
			composing = composing || e.Composing()
			changed = changed || (!disabled && e.Changed())
		}
		ui.Row(c).Gap(6).Children(func() {
			field("helm.archive", &h.archive, "Helm distribution archive", "Absolute path to approved Helm distribution")
			field("helm.chart", &h.chart, "Helm chart archive", "Absolute path to local chart .tgz")
		})
		ui.Row(c).Gap(6).Children(func() {
			field("helm.values", &h.values, "Helm values file", "Optional absolute values-file path")
			field("helm.release", &h.release, "Helm release name", "Render release name")
			field("helm.kubeVersion", &h.kubeVersion, "Helm Kubernetes version", "Explicit 1.35/36/37 patch version")
		})
		h.composing = composing
		if changed {
			w.helmInputsChanged()
		}
		if guardedCheckbox(c, "helm.trust", &h.trusted, "I trust this local chart (not sandboxed)", !disabled).Disabled(disabled).Changed() && !disabled {
			w.stopDeliveryReview()
		}
		if h.result != nil {
			ui.Scroll(c.Key("helm.summary.scroll")).Height(40).Children(func() { ui.Text(c, h.result.Summary()).Label("Helm result summary").FontSize(11) })
			guardedCheckbox(c, "helm.replace", &h.replace, "Replace current input; start a new Undo/review history", d.busy == "").Disabled(d.busy != "")
		}
		ui.Row(c).Gap(6).Children(func() {
			ui.Button(c, "Render local Helm").Disabled(disabled || !h.trusted || composing || d.composing || d.retainedPaste != "" || w.pendingWrites != 0).OnClick(w.renderHelm)
			ui.Button(c, "Use Helm result").Disabled(h.result == nil || !h.replace || d.busy != "" || composing || d.composing || w.pendingWrites != 0).OnClick(w.useHelmResult)
			ui.Button(c, "Discard Helm result").Disabled(h.result == nil).OnClick(w.discardHelmResult)
			ui.Button(c, "Close Helm rendering").OnClick(func() { w.discardHelmResult(); h.open = false; h.trusted = false })
		})
	})
}
