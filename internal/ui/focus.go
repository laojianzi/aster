package uiworkbench

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

func (w *Workbench) focusQueueView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c.Key("diagnosis.queue")).Label("Diagnosis queue").Width(min(300*c.Preferences().TextScale, 400)).Shrink(0).MinHeight(0).Padding(8).Gap(8).Background(t.Surface).Children(func() {
		ui.Row(c).Gap(8).Wrap().Children(func() {
			ui.Button(c, "Back to resources").OnClick(func() { w.focusDiagnosis = false })
			ui.Button(c, "Refresh").Label("Refresh diagnosis").OnClick(w.startScope)
		})
		title := "Needs attention"
		if w.status == "Live" {
			title = fmt.Sprintf("Needs attention (%d)", len(w.rows))
		}
		ui.Text(c, title).Bold()
		ui.Text(c, w.currentKind.Kind+" / "+w.activeNamespace).SingleLine().Tooltip(w.currentKind.Label() + " / " + w.activeNamespace).TextColor(t.TextMuted)
		if ui.SearchField(c.Key("diagnosis.search"), &w.filter).Label("Filter diagnosis").Changed() {
			w.queryChanged()
		}
		if w.status == "Unavailable" {
			ui.Text(c, "Diagnosis unavailable").TextColor(t.Danger)
			ui.Text(c, w.errText).Label("Diagnosis error").TextColor(t.TextMuted)
		} else if w.status != "Live" {
			ui.Text(c, w.status).TextColor(t.TextMuted)
		} else if len(w.rows) == 0 {
			message := "No resources need attention"
			if w.filter != "" || w.appliedLabelSelector != "" {
				message = "No matching resources"
			}
			ui.Text(c, message).TextColor(t.TextMuted)
		}
		selected := ""
		if w.objectRequest != nil && w.objectRequest.kind.GVR == w.currentKind.GVR {
			selected = w.objectRequest.row.key()
		}
		if ui.Sidebar(c.Key("diagnosis.items"), &selected, func() {
			for _, row := range w.rows {
				name := row.Name
				if row.Namespace != "" {
					name = row.Namespace + "/" + name
				}
				display := name
				if row.Namespace == w.activeNamespace {
					display = row.Name
				}
				ui.SidebarItem(c.Key(row.key()), row.key(), nil, display).Label("Diagnose " + name).Tooltip(name + "\n" + string(row.Health) + "\n" + row.Status).Children(func() {
					ui.Badge(c, string(row.Health))
				})
			}
		}).Label("Attention resources").Grow(1).MinHeight(0).Changed() {
			for _, row := range w.rows {
				if row.key() == selected {
					w.openResource(row)
					break
				}
			}
		}
	})
}
