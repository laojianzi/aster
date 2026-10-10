package uiworkbench

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/relationship"
)

func (w *Workbench) stopRelationships() {
	w.relatedEpoch++
	if w.relatedCancel != nil {
		w.relatedCancel()
		w.relatedCancel = nil
	}
	w.relatedActive = false
}
func (w *Workbench) loadRelationships() {
	if w.detail == nil || w.backend == nil || w.creating {
		return
	}
	w.stopLogs()
	w.stopRelationships()
	epoch, revision := w.detailEpoch, w.relatedEpoch
	target, backend := w.target(), w.backend
	types := make([]relationship.Type, 0, len(w.kinds))
	for _, k := range w.kinds {
		types = append(types, relationship.Type{GVR: k.GVR, Kind: k.Kind, Namespaced: k.Namespaced})
	}
	reader := relationship.New(backend, types, relationship.Limits{})
	ctx, cancel := context.WithCancel(w.operationContext())
	w.relatedCancel, w.relatedActive, w.detailMode = cancel, true, "Related"
	w.relatedResult, w.relatedStatus = relationship.Snapshot{}, "Reading one-hop relationships…"
	w.relatedList = ui.ListState{}
	w.run(func(context.Context) {
		defer cancel()
		result, err := reader.Read(ctx, target)
		w.emit(func() {
			if epoch != w.detailEpoch || revision != w.relatedEpoch {
				return
			}
			w.relatedActive = false
			if err != nil {
				w.relatedResult = relationship.Snapshot{}
				w.relatedStatus = "Relationships unavailable: " + err.Error()
				return
			}
			w.relatedResult = result
			w.relatedStatus = fmt.Sprintf("%d links · %d lookups · %d objects scanned", len(result.Links), result.Requests, result.Scanned)
			if result.Incomplete {
				w.relatedStatus += " · PARTIAL"
			}
		})
	})
}
func (w *Workbench) openRelated(link relationship.Link) {
	if w.detail == nil || !link.Navigable() || w.sessionID != link.Target.SessionID || w.target().Key() != w.relatedResult.Target.Key() {
		return
	}
	kind := kube.ResourceKind{GVR: link.Type.GVR, Kind: link.Type.Kind, Namespaced: link.Type.Namespaced}
	w.openResourceKind(kind, resourceRow{UID: string(link.Target.UID), Name: link.Target.Name, Namespace: link.Target.Namespace})
}
func (w *Workbench) relationshipView(c *ui.Context) {
	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, "Refresh relationships").Disabled(w.relatedActive).OnClick(func() {
			w.loadRelationships()
		})
		ui.Button(c, "Stop lookup").Disabled(!w.relatedActive).OnClick(func() {
			w.stopRelationships()
			w.relatedStatus = "Lookup stopped; no cluster mutation was performed."
		})
	})
	ui.Text(c, w.relatedStatus).FontSize(11).SingleLine()
	ui.Text(c, "One-hop snapshot · selectors are not ownership or proof of traffic").FontSize(11).SingleLine()
	if len(w.relatedResult.Warnings) > 0 {
		warning := strings.Join(w.relatedResult.Warnings, "\n")
		ui.TextArea(c.Key("relationship-warnings"), &warning).Label("Relationship warnings").ReadOnly(true).Height(64)
	}
	links := w.relatedResult.Links
	ui.List(c.Key("field.relatedList"), &w.relatedList, len(links), func(i int) {
		link := links[i]
		ui.Column(c).Padding(6).Gap(3).Children(func() {
			ui.Text(c, link.Relation+" · "+string(link.State)).FontSize(11).SingleLine()
			ui.Button(c, "Open "+link.Type.Kind+"/"+link.Target.Name).Disabled(!link.Navigable()).OnClick(func() {
				w.openRelated(link)
			})
			ui.Text(c, link.Note).FontSize(10).SingleLine()
		})
	}).Grow(1)
	if !w.relatedActive && len(w.relatedResult.Links) == 0 && w.relatedResult.SourceVersion != "" {
		text := "No supported direct relationships found in this snapshot."
		if w.relatedResult.Incomplete {
			text = "Results are incomplete; absence does not mean no relationships exist."
		}
		ui.Text(c, text).FontSize(11)
	}
}
