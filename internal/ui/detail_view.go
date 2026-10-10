package uiworkbench

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/resourcemetrics"
)

// detailWidgetKey prevents focus, IME state and Undo history from crossing
// immutable resource/connection identities, even without an intervening frame.
type detailWidgetKey struct{ Connection, Detail uint64 }

func (w *Workbench) detailView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c.Key(detailWidgetKey{w.contextEpoch, w.detailEpoch})).Width(510).Padding(12).Gap(8).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			title := w.detail.GetName()
			if w.creating {
				title = "New " + w.detailKind.Kind
			}
			ui.Text(c, title).Bold().FontSize(18).Grow(1).SingleLine()
			ui.Button(c, "Refresh detail").Disabled(w.creating).OnClick(func() {
				w.openResourceKind(w.detailKind, project(w.detail))
			})
			ui.Button(c, "Close detail").OnClick(func() {
				w.clearDetail()
			})
		})
		if w.detail == nil {
			return
		}
		ui.Text(c, w.activeContext+" / "+w.detail.GetNamespace()+" / "+w.detailKind.Kind).FontSize(11).TextColor(t.TextMuted)
		ui.Row(c).Gap(6).Children(func() {
			ui.Button(c, "YAML").OnClick(func() {
				w.detailMode = "YAML"
				w.stopLogs()
			})
			ui.Button(c, "Edit").Disabled(w.detail.GetKind() == "Secret").OnClick(func() {
				w.detailMode = "Edit"
				w.stopLogs()
			})
			ui.Button(c, "Health").Disabled(w.creating).OnClick(func() {
				if w.healthActive {
					w.detailMode = "Health"
					w.stopLogs()
				} else {
					w.loadHealth()
				}
			})
			ui.Button(c, "Events").Disabled(w.creating).OnClick(func() {
				w.loadEvents()
			})
			ui.Button(c, "Related").Disabled(w.creating).OnClick(func() {
				w.loadRelationships()
			})
			ui.Button(c, "Metrics").Disabled(w.creating || !resourcemetrics.Supported(w.detailKind.GVR)).OnClick(func() {
				w.loadMetrics(false)
			})
		})
		ui.Row(c).Gap(6).Children(func() {
			ui.Button(c, "Logs").Disabled(w.creating || w.detailKind.GVR.Group != "" || w.detailKind.GVR.Resource != "pods").OnClick(func() {
				w.detailMode = "Logs"
			})
			ui.Button(c, "Terminal").Disabled(w.creating || w.detailKind.GVR.Group != "" || w.detailKind.GVR.Resource != "pods").OnClick(func() {
				w.detailMode = "Terminal"
				w.stopLogs()
				if w.terminalArgv == "" {
					w.terminalArgv = `["/bin/sh"]`
				}
			})
			ui.Button(c, "Command").Disabled(w.creating || w.detailKind.GVR.Group != "" || w.detailKind.GVR.Resource != "pods").OnClick(func() {
				w.detailMode = "Command"
				w.stopLogs()
				if w.commandArgv == "" {
					w.commandArgv = `["id"]`
				}
			})
			ui.Button(c, "Port forward").Disabled(w.creating || w.detailKind.GVR.Group != "" || w.detailKind.GVR.Resource != "pods").OnClick(func() {
				w.detailMode = "Forward"
				w.stopLogs()
			})
			ui.Button(c, "Owners").Disabled(w.creating).OnClick(func() {
				w.stopLogs()
				w.ownersText = manifest.Ownership(w.detail)
				w.detailMode = "Owners"
			})
		})
		if w.detailMode != "Edit" && (w.schemaActive || w.schemaText != "" || w.schemaStatus != "") {
			w.clearSchema()
		}
		if w.detailMode != "Terminal" && w.terminalCancel != nil {
			w.stopTerminal()
		}
		if w.detailMode != "Metrics" && w.metricsCancel != nil {
			w.stopMetrics()
		}
		if w.detailMessage != "" {
			ui.Text(c, w.detailMessage).FontSize(12).TextColor(t.TextMuted)
		}
		switch w.detailMode {
		case "Apply":
			w.applyView(c)
		case "Owners":
			ui.TextArea(c.Key("field.ownersText"), &w.ownersText).Label("Field ownership").ReadOnly(true).Grow(1)
		case "Edit":
			if ui.TextArea(c.Key("field.editor"), &w.editor).Label("Manifest editor").Grow(1).Changed() {
				w.clearSchema()
				w.stopPreview()
				w.draftRevision++
				w.plan = nil
				w.diff = ""
				w.confirmation = ""
			}
			w.schemaView(c)
			if w.creating {
				ui.PrimaryButton(c, "Preview create").Disabled(w.preparing).OnClick(func() {
					w.prepare("create")
				})
			} else {
				ui.Row(c).Gap(6).Children(func() {
					ui.PrimaryButton(c, "Preview edit").Disabled(w.preparing).OnClick(func() {
						w.prepare("edit")
					})
					ui.Button(c, "Apply").Disabled(w.preparing).OnClick(func() {
						w.beginApply()
					})
				})
			}
		case "Diff":
			ui.Text(c, "Server dry-run preview · live state is rechecked at execution").FontSize(12)
			ui.TextArea(c.Key("field.diff"), &w.diff).Label("Change diff").ReadOnly(true).Grow(1)
			ui.TextInput(c.Key("field.confirmation"), &w.confirmation).Label("Confirm resource name").Placeholder("Type the resource name to confirm")
			ui.PrimaryButton(c, "Execute reviewed change").Disabled(w.plan == nil || w.confirmation != w.confirmationName() || w.pendingWrites > 0).OnClick(func() {
				w.execute()
			})
		case "Metrics":
			w.metricsView(c)
		case "Related":
			w.relationshipView(c)
		case "Health":
			w.healthView(c)
		case "Terminal":
			w.terminalView(c)
		case "Command":
			w.commandView(c)
		case "Forward":
			w.forwardView(c)
		case "Events":
			ui.TextArea(c.Key("field.eventsText"), &w.eventsText).Label("Resource events").ReadOnly(true).Grow(1)
		case "Logs":
			if ui.Select(c.Key("field.container"), &w.container, w.containers).Label("Container").Changed() {
				w.stopLogs()
				w.logRows = nil
				w.logDropped = 0
				w.logStatus = "Select a log action for this container"
			}
			ui.Row(c).Gap(6).Children(func() {
				ui.Button(c, "Follow logs").OnClick(func() {
					w.startLogs(false, true)
				})
				ui.Button(c, "Previous logs").OnClick(func() {
					w.startLogs(true, false)
				})
				ui.Button(c, "Stop logs").OnClick(func() {
					w.stopLogs()
					w.logStatus = "Stopped"
				})
			})
			ui.List(c.Key("field.logList"), &w.logList, len(w.logRows), func(i int) { ui.Text(c, w.logRows[i]).FontSize(11).SingleLine() }).Grow(1)
			ui.Text(c, fmt.Sprintf("%s · %d truncated line(s)", w.logStatus, w.logDropped)).FontSize(11)
		default:
			ui.TextArea(c.Key("field.detailText"), &w.detailText).Label("Resource document").ReadOnly(true).Grow(1)
		}
		if w.detail != nil && !w.creating && w.detailMode != "Diff" {
			ui.Row(c).Gap(6).Children(func() {
				ui.TextInput(c.Key("field.replicas"), &w.replicas).Label("Replica count").Width(70)
				ui.Button(c, "Preview scale").Disabled(w.preparing).OnClick(func() {
					w.prepare("scale")
				})
				ui.Button(c, "Preview restart").Disabled(w.preparing).OnClick(func() {
					w.prepare("restart")
				})
				ui.Button(c, "Preview delete").Disabled(w.preparing).OnClick(func() {
					w.prepare("delete")
				})
			})
		}
	})
}
