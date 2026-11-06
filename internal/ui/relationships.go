package uiworkbench

import (
	"context"
	"fmt"
	"slices"
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
	w.relatedGroups = nil
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
			w.relatedStatus = fmt.Sprintf("Relationships: %d", len(result.Links))
			if result.Incomplete {
				w.relatedStatus += "; partial results"
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
			w.relatedStatus = "Lookup stopped"
		})
	})
	ui.Text(c, w.relatedStatus).SingleLine()
	if len(w.relatedResult.Warnings) > 0 {
		warning := strings.Join(w.relatedResult.Warnings, "\n")
		ui.TextArea(c.Key("relationship-warnings"), &warning).Label("Relationship warnings").ReadOnly(true).Height(64)
	}
	links := w.relatedResult.Links
	if w.relatedGroups == nil {
		w.relatedGroups = make(map[string]*bool)
	}
	var groups []string
	for _, link := range links {
		if !slices.Contains(groups, link.Relation) {
			groups = append(groups, link.Relation)
		}
		if w.relatedGroups[link.Relation] == nil {
			w.relatedGroups[link.Relation] = new(true)
		}
	}
	ui.Scroll(c.Key("relationship.scroll")).Grow(1).MinHeight(0).MinWidth(0).Children(func() {
		ui.Tree(c.Key("relationship.tree"), func() {
			rowHeight := c.Theme().Space(7) * max(float32(1), c.Preferences().TextScale)
			for _, group := range groups {
				ui.TreeItem(c.Key(group), group, w.relatedGroups[group], func() {
					for _, link := range links {
						if link.Relation != group {
							continue
						}
						name := link.Type.Kind + "/" + link.Target.Name
						label := name
						if link.State != relationship.Verified {
							label += " (" + string(link.State) + ")"
						}
						ui.TreeItem(c.Key(link.Relation+"|"+link.Target.Key()), label, nil, nil).Height(rowHeight).
							Label("Open " + name).Tooltip(name + "\n" + string(link.State) + "\n" + link.Note).
							Disabled(!link.Navigable()).OnClick(func() { w.openRelated(link) })
					}
				}).Height(rowHeight)
			}
		}).Label("Direct relationships").Tooltip("Selectors do not imply ownership or traffic").MinWidth(0).Padding(4)
	})
	if !w.relatedActive && len(w.relatedResult.Links) == 0 && w.relatedResult.SourceVersion != "" {
		text := "No supported direct relationships found in this snapshot."
		if w.relatedResult.Incomplete {
			text = "Results are incomplete; absence does not mean no relationships exist."
		}
		ui.Text(c, text)
	}
}
