package uiworkbench

import (
	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/health"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/resourcemetrics"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"
)

func (w *Workbench) detailView(c *ui.Context) {
	t := c.Theme()
	var reviewOpener ui.Element
	displayMode := w.detailMode
	if displayMode == "Diff" {
		displayMode = w.reviewReturnMode
		if displayMode == "" || displayMode == "Diff" {
			displayMode = "Overview"
		}
	}
	switch displayMode {
	case "Logs", "Terminal", "Command", "Forward":
		if w.bindDiagnostic() {
			w.diagnostic.mode = displayMode
		}
		displayMode = "Overview"
	}
	_, height := c.Size()
	compact := height < min(600, 300*c.Preferences().TextScale)
	padding, gap := float32(16), float32(6)
	if compact {
		padding, gap = 8, 3
	}
	workspace := ui.Column(c.Key(w.editor)).Label("Object workspace").Grow(1).Basis(0).MinWidth(0).MinHeight(0).Padding(padding).Gap(gap)
	metadataOpen := ui.Local(workspace, "metadata.open", func() bool { return false })
	workspace.Children(func() {
		title := w.detail.GetName()
		if w.creating {
			title = "New " + w.detailKind.Kind
		}
		if w.creating || w.objectRequest == nil {
			ui.Text(c, title).Bold().FontSize(t.FontSize * 1.4).SingleLine().Tooltip(title)
		}
		tabs := []string{"Overview", "Related", "Events", "YAML"}
		section := "Overview"
		switch displayMode {
		case "YAML", "Edit", "Owners", "Apply":
			section = "YAML"
		case "Related", "Events":
			section = displayMode
		}
		if w.creating {
			section = "YAML"
		}
		selected := slices.Index(tabs, section)
		changeSection := func() {
			switch tabs[selected] {
			case "Overview", "YAML":
				w.detailMode = tabs[selected]
			case "Events":
				w.loadEvents()
			case "Related":
				w.loadRelationships()
			}
		}
		ui.Row(c.Key("object.actions")).Role(ui.RoleToolbar).FocusGroup(ui.Horizontal).AlignItems(ui.Center).Gap(t.Space(1)).Padding(t.Space(1)).MinWidth(0).Wrap().Children(func() {
			if compact && !w.creating && ui.Select(c.Key("object.section"), &section, tabs).Label("Object sections").Width(180*c.Preferences().TextScale).Changed() {
				selected = slices.Index(tabs, section)
				changeSection()
			}
			reviewOpener = ui.PopoverMenuButton(c, "Actions", func(m *ui.Menu) {
				if compact {
					if m.Item("Refresh detail").Disabled(w.creating).Chosen() {
						w.openResourceKind(w.detailKind, project(w.detail))
					}
					if m.Item("Close detail").Chosen() {
						w.closeObjectTab()
					}
				}
				disabled := w.creating || w.preparing || w.editorRetainedPaste != "" || w.detailMode == "Diff"
				gvr := w.detailKind.GVR
				scalable := gvr.Version == "v1" && ((gvr.Group == "apps" && (gvr.Resource == "deployments" || gvr.Resource == "statefulsets" || gvr.Resource == "replicasets")) || (gvr.Group == "" && gvr.Resource == "replicationcontrollers"))
				restartable := gvr.Group == "apps" && gvr.Version == "v1" && (gvr.Resource == "deployments" || gvr.Resource == "statefulsets" || gvr.Resource == "daemonsets")
				if m.Item("Scale").Disabled(disabled || !scalable).Chosen() {
					w.detailMode = "Scale"
				}
				if m.Item("Preview restart").Disabled(disabled || !restartable).Chosen() {
					w.prepare("restart")
				}
				if m.Item("Preview delete").Disabled(disabled).Chosen() {
					w.prepare("delete")
				}
			})
			ui.PopoverMenuButton(c, "Diagnostics", func(m *ui.Menu) {
				disabled := w.creating || w.detailKind.GVR.Group != "" || w.detailKind.GVR.Resource != "pods"
				if m.Item("Logs").Disabled(disabled).Chosen() {
					w.selectDiagnostic("Logs")
					w.detailMode = "Logs"
				}
				if m.Item("Terminal").Disabled(disabled).Chosen() {
					w.selectDiagnostic("Terminal")
					w.detailMode = "Terminal"
					if w.terminalArgv == "" {
						w.terminalArgv = `["/bin/sh"]`
					}
				}
				if m.Item("Command").Disabled(disabled).Chosen() {
					w.selectDiagnostic("Command")
					w.detailMode = "Command"
					if w.commandArgv == "" {
						w.commandArgv = `["id"]`
					}
				}
				if m.Item("Port forward").Disabled(disabled).Chosen() {
					w.selectDiagnostic("Forward")
					w.detailMode = "Forward"
				}
			}).Hidden(w.creating || w.detailKind.GVR.Group != "" || w.detailKind.GVR.Resource != "pods")
			ui.Button(c, "Refresh").Hidden(compact).Label("Refresh detail").Disabled(w.creating).OnClick(func() {
				w.openResourceKind(w.detailKind, project(w.detail))
			})
			ui.Button(c, "Close").Hidden(compact).Label("Close detail").OnClick(w.closeObjectTab)
		}).Label("Object actions")
		if w.detail == nil {
			return
		}
		ui.Text(c, w.activeContext+" / "+w.detail.GetNamespace()+" / "+w.detailKind.Kind).Hidden(compact).TextColor(t.TextMuted)
		if !compact && !w.creating && ui.Tabs(c.Key("object.tabs"), &selected, tabs...).Label("Object sections").Wrap().Changed() {
			changeSection()
		}

		if w.detailMode != "Edit" && (w.schemaActive || w.schemaText != "" || w.schemaStatus != "") {
			w.clearSchema()
		}
		if w.detailMode != "Metrics" && w.metricsCancel != nil {
			w.stopMetrics()
		}
		// The retained-paste panel already explains the pause. Do not render the
		// identical status twice: at minimum size it displaces actions into the
		// footer on platforms with taller native fonts. Other outcomes stay visible.
		if w.detailMode != "Diff" && w.detailMessage != "" && !(w.editorRetainedPaste != "" && w.detailMessage == manifestPastePendingMessage) {
			ui.Text(c, w.detailMessage).TextColor(t.TextMuted)
		}
		w.manifestPasteView(c)
		w.syncFind()
		w.diagnosticLayout(c, func() {
			ui.Column(c.Key("object.content")).Grow(1).MinHeight(0).Gap(6).Children(func() {
				if section == "Overview" {
					ui.Row(c.Key("object.overview.tools")).Hidden(displayMode == "Overview" && w.detail.GetAPIVersion() == "v1" && w.detail.GetKind() == "ConfigMap").Role(ui.RoleToolbar).FocusGroup(ui.Horizontal).Gap(6).Wrap().Children(func() {
						if displayMode != "Overview" {
							ui.Button(c, "Summary").OnClick(func() { w.detailMode = "Overview" })
						}
						ui.Button(c, "Health").Hidden(w.detail.GetAPIVersion() == "v1" && w.detail.GetKind() == "ConfigMap").OnClick(func() {
							if w.healthActive {
								w.detailMode = "Health"
							} else {
								w.loadHealth()
							}
						})
						if resourcemetrics.Supported(w.detailKind.GVR) {
							ui.Button(c, "Metrics").OnClick(func() {
								if w.detailMode != "Metrics" {
									w.loadMetrics(false)
								}
							})
						}
					})
				}
				if section == "YAML" {
					ui.Row(c.Key("object.yaml.tools")).Role(ui.RoleToolbar).FocusGroup(ui.Horizontal).Gap(6).Wrap().Children(func() {
						ui.Button(c, "Read-only").OnClick(func() { w.detailMode = "YAML" })
						if w.detail.GetKind() != "Secret" {
							ui.Button(c, "Edit").OnClick(func() { w.detailMode = "Edit" })
						}
						if !w.creating {
							ui.Button(c, "Field ownership").OnClick(func() {
								w.ownersText = manifest.Ownership(w.detail)
								w.detailMode = "Owners"
							})
						}
					})
				}
				if displayMode != "Edit" && w.detail.GetKind() != "Secret" && w.editor != nil && w.editorHasSafe && w.editor.Snapshot() == w.editorLastSafe {
					ui.TextAreaBuffer(c.Key("field.editor"), w.editor).Label("Manifest editor").Font("monospace").Hidden(true)
				}
				switch displayMode {
				case "Overview":
					w.overviewView(c, metadataOpen)
				case "Scale":
					ui.Text(c, "Scale replicas").Bold()
					ui.TextInput(c.Key("field.replicas"), &w.replicas).Label("Replica count").Width(120)
					reviewOpener = ui.Button(c, "Preview scale").Disabled(w.preparing || w.editorRetainedPaste != "").OnClick(func() { w.prepare("scale") })
					ui.Spacer(c).Grow(1)
				case "Apply":
					reviewOpener = w.applyView(c)
				case "Owners":
					ui.TextArea(c.Key("field.ownersText"), &w.ownersText).Label("Field ownership").ReadOnly(true).Grow(1)
				case "Edit":
					w.findView(c)
					w.manifestEditorView(c)
					w.schemaView(c)
					if w.creating {
						w.findButton(c)
						reviewOpener = ui.PrimaryButton(c, "Preview create").Disabled(w.preparing || w.editorRetainedPaste != "").OnClick(func() {
							w.prepare("create")
						})
					} else {
						ui.Row(c).Gap(6).Wrap().Children(func() {
							w.findButton(c)
							reviewOpener = ui.PrimaryButton(c, "Preview edit").Disabled(w.preparing || w.editorRetainedPaste != "").OnClick(func() {
								w.prepare("edit")
							})
							ui.Button(c, "Apply").Disabled(w.preparing || w.editorRetainedPaste != "").OnClick(func() {
								w.beginApply()
							})
						})
					}
				case "Metrics":
					w.metricsView(c)
				case "Related":
					w.relationshipView(c)
				case "Health":
					w.healthView(c)
				case "Events":
					ui.Button(c, "Refresh events").OnClick(w.loadEvents)
					ui.TextArea(c.Key("field.eventsText"), &w.eventsText).Label("Resource events").ReadOnly(true).Grow(1)

				default:
					ui.TextArea(c.Key("field.detailText"), &w.detailText).Label("Resource document").Font("monospace").ReadOnly(true).Grow(1)
				}
			})
		})
	})
	if w.reviewReturnFocus && w.detailMode != "Diff" {
		reviewOpener.Focus()
		w.reviewReturnFocus = false
	}
	if w.detailMode == "Diff" && w.resourceTaskVisible() {
		unsubmitted := w.plan != nil
		open := true
		width, height := c.Size()
		ui.Modal(c.Key("review.dialog"), &open, func() {
			ui.Column(c).Width(min(760, width-80)).Height(min(560, height-80)).Gap(12).Children(func() {
				ui.Text(c, "Review change").Bold()
				ui.Text(c, w.detailMessage)
				diff := ui.TextArea(c.Key("field.diff"), &w.diff).Label("Change diff").Font("monospace").ReadOnly(true).Grow(1).MinHeight(80)
				if w.reviewStartPending {
					diff.SetTextSelection(0, 0)
					w.reviewStartPending = false
				}
				if unsubmitted {
					ui.TextInput(c.Key("field.confirmation"), &w.confirmation).Label("Confirm resource name").Placeholder("Type the resource name to confirm").AutoFocus()
				}
				ui.Row(c).Gap(8).Wrap().Children(func() {
					closeLabel := "Close review"
					if unsubmitted {
						closeLabel = "Cancel review"
					}
					close := ui.Button(c, closeLabel)
					if !unsubmitted {
						close.AutoFocus()
					}
					if close.Clicked() {
						open = false
					}
					if unsubmitted {
						ui.PrimaryButton(c, "Execute reviewed change").Disabled(w.editorRetainedPaste != "" || w.confirmation != w.confirmationName() || w.pendingWrites > 0).OnClick(w.execute)
					}
				})
			})
		}).Label("Review change")
		if !open {
			w.stopPreview()
			w.plan, w.confirmation, w.diff = nil, "", ""
			w.detailMode = displayMode
			w.reviewReturnFocus = true
			if unsubmitted && w.pendingWrites == 0 {
				w.detailMessage = ""
			}
		}
	}
}

func (w *Workbench) overviewView(c *ui.Context, metadataOpen *bool) {
	t := c.Theme()

	ui.Scroll(c).Label("Object overview").Grow(1).Gap(20).Children(func() {
		if w.detail.GetAPIVersion() == "v1" && w.detail.GetKind() == "ConfigMap" {
			ui.Row(c).Gap(8).Wrap().Children(func() {
				ui.Button(c, "Edit configuration").OnClick(func() { w.detailMode = "Edit" })
				if immutable, _, _ := unstructured.NestedBool(w.detail.Object, "immutable"); immutable {
					ui.Badge(c, "Immutable")
				}
			})
			empty := true
			for _, group := range [][2]string{{"data", "Data"}, {"binaryData", "Binary data (Base64)"}} {
				values, _, _ := unstructured.NestedStringMap(w.detail.Object, group[0])
				if len(values) == 0 {
					continue
				}
				empty = false
				ui.Column(c.Key(group[0])).Gap(6).MinWidth(0).Children(func() {
					ui.Text(c, group[1]).Bold()
					for _, key := range slices.Sorted(maps.Keys(values)) {
						entry := ui.Column(c.Key(key)).MinWidth(0)
						open := ui.Local(entry, "open", func() bool { return false })
						entry.Children(func() {
							ui.Collapsible(c, key, open, func() {
								ui.Text(c, key).Selectable()
								value := values[key]
								height := min(160, max(52, float32(strings.Count(value, "\n")+1)*t.FontSize*1.5+16)) * c.Preferences().TextScale
								ui.TextArea(c.Key("configmap.value"), &value).Label("ConfigMap value " + key).Font("monospace").ReadOnly(true).Height(height)
							})
						})
					}
				})
			}
			if empty {
				ui.Text(c, "No configuration data").TextColor(t.TextMuted)
			}
		} else {
			report := health.Assess(w.detail)
			ui.Column(c).Gap(6).Children(func() {
				ui.Row(c).Gap(12).Children(func() {
					ui.Text(c, "Observed status").Bold()
					ui.Badge(c, string(report.State))
				})
				ui.Text(c, report.Message).TextColor(t.TextMuted)
			})
		}
		w.containerEvidenceView(c)
		metadata := ui.Column(c.Key("overview.metadata"))
		metadata.Children(func() {
			ui.Collapsible(c, "Metadata", metadataOpen, func() {
				created := "Unavailable"
				if stamp := w.detail.GetCreationTimestamp(); !stamp.IsZero() {
					created = stamp.UTC().Format(time.RFC3339)
				}
				namespace := w.detail.GetNamespace()
				if !w.detailKind.Namespaced {
					namespace = "Cluster-scoped"
				}
				properties := [][2]string{{"Namespace", namespace}, {"API version", w.detail.GetAPIVersion()}, {"Created", created}, {"Resource UID", string(w.detail.GetUID())}, {"Resource version", w.detail.GetResourceVersion()}}
				if w.detail.GetAPIVersion() == "v1" && w.detail.GetKind() == "Pod" {
					for _, field := range [][2]string{{"Node", "nodeName"}, {"Service account", "serviceAccountName"}} {
						value, _, _ := unstructured.NestedString(w.detail.Object, "spec", field[1])
						if value != "" {
							properties = append(properties, [2]string{field[0], value})
						}
					}
				}
				ui.Column(c).Gap(10).Children(func() {
					for _, property := range properties {
						ui.Row(c).Gap(16).Children(func() {
							ui.Text(c, property[0]).Width(150 * c.Preferences().TextScale).TextColor(t.TextMuted)
							ui.Text(c, property[1]).Label(property[0]).Selectable().Grow(1).MinWidth(0)
						})
					}
				})
				labels := w.detail.GetLabels()
				if len(labels) > 0 {
					keys := make([]string, 0, len(labels))
					for key := range labels {
						keys = append(keys, key)
					}
					sort.Strings(keys)
					ui.Column(c).Gap(8).Children(func() {
						ui.Text(c, "Labels").Bold()
						for _, key := range keys {
							ui.Text(c, key+"="+labels[key]).Selectable()
						}
					})
				}
			})
		})
	})
}
