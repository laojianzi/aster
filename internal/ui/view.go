package uiworkbench

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/health"
	"github.com/laojianzi/aster/internal/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func (w *Workbench) View(c *ui.Context) {
	c.SetTheme(workbenchTheme(c))
	if w.applications.open && w.applications.namespace != strings.TrimSpace(w.namespace) {
		w.clearApplications()
	}
	w.syncEditor()
	w.syncFind()
	w.syncDelivery()
	w.syncHelmReleases()
	w.syncHelmUninstall()
	w.expireHelmUninstall(time.Now())
	w.expireHelmUninstallFlow(time.Now())
	switch w.detailMode {
	case "Logs", "Terminal", "Command", "Forward":
		if w.bindDiagnostic() {
			w.diagnostic.mode = w.detailMode
		}
	}
	w.frames++
	t := c.Theme()
	compact := w.detail != nil && compactDetail(c)
	headerPadding := float32(12)
	if compact {
		headerPadding = 6
	}
	connectionSetupHidden := w.backend != nil && !w.connectionsOpen && !w.oidcOpen && !w.vaultOpen && !w.connectionPending && !w.vaultPending && !w.oidcPending && !w.trustRequired
	ui.Column(c).Direction(ui.LeftToRight).Fill().Background(t.Background).Children(func() {

		ui.Row(c).Padding(headerPadding).Gap(12).Wrap().Children(func() {
			ui.Text(c, "Aster").Hidden(compact).Bold()
			if compact {
				ui.PopoverMenuButton(c, "Aster", func(m *ui.Menu) {
					if m.Item("New workspace").Disabled(w.newWorkspace == nil).Chosen() {
						if err := w.newWorkspace(); err != nil {
							w.notice = err.Error()
						}
					}
				}).Tooltip(fmt.Sprintf("Workspace %d", w.workspaceNumber))
			}
			ui.Button(c, "Applications").Selected(w.applications.open).Disabled(w.backend == nil || w.pendingWrites != 0).OnClick(w.openApplications)
			ui.Button(c, "Resources").Selected(!w.applications.open && !w.connectionsOpen && !w.oidcOpen && !w.vaultOpen).OnClick(func() {
				w.focusDiagnosis = false
				w.applications.open = false
				w.connectionsOpen = false
				w.clearHelmReleases()
				w.clearHelmUninstall()
				if w.delivery.open {
					w.closeDelivery()
				}
				w.clearOIDC()
				w.oidcOpen = false
				w.clearVault()
				w.vaultOpen = false
			})
			ui.Button(c, "Connections").Selected(!w.applications.open && (w.connectionsOpen || w.oidcOpen || w.vaultOpen)).OnClick(func() {
				w.clearApplications()
				w.invalidateDraft()
				w.clearFind(true)
				w.clearHelmReleases()
				w.clearHelmUninstall()
				if w.delivery.open {
					w.closeDelivery()
				}
				w.clearOIDC()
				w.oidcOpen = false
				w.clearVault()
				w.vaultOpen = false
				w.connectionsOpen = true
			})
			ui.Box(c).Grow(1)
			if w.workspaceNumber > 0 && !compact {
				ui.Text(c, fmt.Sprintf("Workspace %d", w.workspaceNumber)).Label("Workspace identity").Shrink(0)
			}
			ui.Button(c, "New workspace").Hidden(compact).Disabled(w.newWorkspace == nil).OnClick(func() {
				if err := w.newWorkspace(); err != nil {
					w.notice = err.Error()
				}
			})
		})
		ui.Row(c).Padding(6, 12).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			if connectionSetupHidden && w.activeContext != "" {
				ui.Row(c.Key("connected.cluster")).Label("Connected cluster").Width(300).Focusable().FocusRing(true).Tooltip(w.activeContext).Children(func() {
					ui.Text(c, w.activeContext).SingleLine().MinWidth(0)
				})
			}
			if len(w.contexts) == 0 {
				ui.Button(c, "No contexts").Label("Cluster context").Hidden(connectionSetupHidden).Width(300).Disabled(true)
			} else if ui.Select(c.Key("field.currentContext"), &w.currentContext, w.contexts).Hidden(connectionSetupHidden).Label("Cluster context").Width(300).Changed() {
				w.namespace = ""
				w.trustedFingerprint = ""
				w.trustRequired = false
				w.disconnect()
			}
			if ui.TextInput(c.Key("field.namespace"), &w.namespace).Label("Namespace").Placeholder("Namespace or *").Width(180 * c.Preferences().TextScale).Changed() {
				w.clearOIDC()
				w.deliveryNamespaceChanged()
				w.clearHelmUninstall()
			}
			ui.PrimaryButton(c, "Connect").Hidden(connectionSetupHidden).Disabled(w.currentContext == "" || w.connectionPending || w.vaultPending || w.oidcPending).OnClick(func() {
				w.connect()
			})
			ui.Button(c, "Disconnect").Disabled(w.backend == nil && !w.connectionPending && !w.vaultPending && !w.oidcPending).OnClick(func() {
				w.disconnect()
			})
			if w.notice != "" {
				ui.Text(c, w.notice).Grow(1).MinWidth(240).TextColor(t.TextMuted)
			}
		})
		if w.trustRequired {
			ui.Row(c).Padding(10).Gap(10).Children(func() {
				ui.Text(c, "This context requests local credentials, executable authentication or unsafe transport. Trust only a configuration you control.")
				ui.Button(c, "Trust this context and connect").Disabled(w.connectionPending).OnClick(func() {
					w.trustedFingerprint = w.pendingTrustFingerprint
					w.connect()
				})
			})
		}
		if w.connectionsOpen || w.oidcOpen || w.vaultOpen {
			selected := 0
			if w.vaultOpen {
				selected = 1
			} else if w.oidcOpen {
				selected = 2
			}
			ui.Row(c).Padding(6, 24).Children(func() {
				if ui.Tabs(c.Key("connection.sections"), &selected, "Configuration", "Credentials", "Browser sign-in").Label("Connection sections").Wrap().Changed() {
					w.clearHelmReleases()
					if w.delivery.open {
						w.closeDelivery()
					}
					w.clearOIDC()
					w.clearVault()
					w.connectionsOpen = true
					w.vaultOpen = selected == 1
					w.oidcOpen = selected == 2
				}
			})
		}
		ui.Row(c.Key("task.workspace")).Grow(1).Basis(0).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			if w.applications.open {
				w.applicationsView(c)
			} else if w.helmUninstall.open {
				w.helmUninstallView(c)
			} else if w.helmReleases.open {
				w.helmReleasesView(c)
			} else if w.delivery.open {
				w.deliveryView(c)
			} else if w.oidcOpen {
				w.oidcView(c)
			} else if w.vaultOpen {
				w.vaultView(c)
			} else if w.connectionsOpen {
				ui.Scroll(c.Key("connections.workspace")).Grow(1).Padding(24).Gap(16).Children(func() {
					ui.Text(c, "Connections").Bold()
					ui.Column(c).MaxWidth(720).Gap(12).Children(func() {
						if ui.TextInput(c.Key("field.path"), &w.path).Label("Kubeconfig path").Placeholder("Kubeconfig path (empty: default)").Grow(1).Changed() {
							w.trustedFingerprint = ""
							w.trustRequired = false
							w.disconnect()
						}
						ui.Button(c, "Load contexts").OnClick(func() {
							w.loadContexts()
						})

					})
				})
			}

			ui.Row(c.Key("resources.workspace")).Hidden(!w.resourceTaskVisible()).Grow(1).Basis(0).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
				ui.Scroll(c.Key("resource.navigation")).Hidden(w.focusDiagnosis || compact).Label("Resource navigation").Width(220 * c.Preferences().TextScale).MinWidth(220).MaxWidth(360).MinHeight(0).Shrink(0).Background(c.Theme().Surface).Padding(4).Children(func() {
					ui.Tree(c.Key("resource.tree"), func() {
						rowHeight := c.Theme().Space(7) * max(float32(1), c.Preferences().TextScale)
						groups := []string{"Workloads", "Network", "Configuration", "Storage", "Access", "Cluster", "Custom resources"}
						navigation := []struct {
							category               int
							group, resource, label string
						}{
							{0, "", "pods", "Pods"},
							{0, "apps", "deployments", "Deployments"},
							{0, "apps", "statefulsets", "StatefulSets"},
							{0, "apps", "daemonsets", "DaemonSets"},
							{0, "apps", "replicasets", "ReplicaSets"},
							{0, "batch", "jobs", "Jobs"},
							{0, "batch", "cronjobs", "CronJobs"},
							{0, "autoscaling", "horizontalpodautoscalers", "Autoscalers"},
							{1, "", "services", "Services"},
							{1, "networking.k8s.io", "ingresses", "Ingresses"},
							{1, "networking.k8s.io", "networkpolicies", "Network Policies"},
							{1, "discovery.k8s.io", "endpointslices", "Endpoint Slices"},
							{1, "", "endpoints", "Endpoints"},
							{2, "", "configmaps", "ConfigMaps"},
							{2, "", "secrets", "Secrets"},
							{3, "", "persistentvolumeclaims", "Volume Claims"},
							{3, "", "persistentvolumes", "Volumes"},
							{3, "storage.k8s.io", "storageclasses", "Storage Classes"},
							{4, "", "serviceaccounts", "Service Accounts"},
							{4, "rbac.authorization.k8s.io", "roles", "Roles"},
							{4, "rbac.authorization.k8s.io", "rolebindings", "Role Bindings"},
							{4, "rbac.authorization.k8s.io", "clusterroles", "Cluster Roles"},
							{4, "rbac.authorization.k8s.io", "clusterrolebindings", "Cluster Role Bindings"},
							{5, "", "nodes", "Nodes"},
							{5, "", "namespaces", "Namespaces"},
							{5, "", "events", "Events"},
							{5, "events.k8s.io", "events", "Events"},
							{5, "apiextensions.k8s.io", "customresourcedefinitions", "Resource Definitions"},
						}
						var indices [7][]int
						labels := make([]string, len(w.kinds))
						for _, entry := range navigation {
							for index, kind := range w.kinds {
								if kind.GVR.Group == entry.group && kind.GVR.Resource == entry.resource {
									indices[entry.category] = append(indices[entry.category], index)
									labels[index] = entry.label
								}
							}
						}
						for index, kind := range w.kinds {
							if labels[index] == "" {
								indices[6] = append(indices[6], index)
								labels[index] = kind.Kind
							}
						}
						labelCounts := make(map[string]int, len(labels))
						for _, label := range labels {
							labelCounts[label]++
						}
						for index, label := range labels {
							if labelCounts[label] > 1 {
								group := w.kinds[index].GVR.Group
								if group == "" {
									group = "core"
								}
								labels[index] = label + " (" + group + ")"
							}
						}
						for group, title := range groups {
							if len(indices[group]) == 0 {
								continue
							}
							if w.navigationShown != w.kindChoice {
								for _, index := range indices[group] {
									if w.kinds[index].Label() == w.kindChoice {
										w.resourceGroups[group] = true
									}
								}
							}
							ui.TreeItem(c.Key(title), title, &w.resourceGroups[group], func() {
								for _, index := range indices[group] {
									kind := w.kinds[index]
									label := labels[index]
									item := ui.TreeItem(c.Key(kind.Label()), label, nil, nil).Height(rowHeight).Tooltip(kind.Label()).Selected(kind.Label() == w.kindChoice).OnClick(func() {
										if kind.Label() != w.kindChoice {
											w.chooseKind(kind)
										}
									})
									if kind.Label() == w.kindChoice && w.navigationShown != w.kindChoice {
										item.ScrollIntoView()
									}
								}
							}).Height(rowHeight).OnClick(func() { w.resourceGroups[group] = !w.resourceGroups[group] })
						}
						w.navigationShown = w.kindChoice
					})
				})
				if w.focusDiagnosis {
					w.focusQueueView(c)
				}
				ui.Column(c.Key("object.workspace")).Grow(1).Basis(0).MinWidth(0).MinHeight(0).Children(func() {
					// Native Undo survives only while the editor keeps its full parent-key path.
					defer func() {
						for _, draft := range w.objectDrafts {
							if draft.editor == w.editor || !draft.hasSafe || draft.editor.Snapshot() != draft.lastSafe {
								continue
							}
							ui.Column(c.Key(draft.editor)).Hidden(true).Children(func() {
								ui.Column(c.Key("diagnostics.layout")).Children(func() {
									ui.Box(c.Key("diagnostics.body")).Children(func() {
										ui.Column(c.Key("object.content")).Children(func() {
											ui.TextAreaBuffer(c.Key("field.editor"), draft.editor).Label("Manifest editor").Font("monospace")
										})
									})
								})
							})
						}
					}()

					if len(w.objectTabs) > 0 {
						labels := []string{"Resource list"}
						selected := 0
						for i, tab := range w.objectTabs {
							name := tab.row.Name
							if tab.row.Namespace != "" {
								name = tab.row.Namespace + "/" + name
							}
							labels = append(labels, tab.kind.Label()+" "+tab.kind.GVR.Version+" / "+name)
							if w.objectRequest != nil && w.objectRequest.target == tab.target {
								selected = i + 1
							}
						}
						stripPadding := float32(24)
						if compact {
							stripPadding = 8
						}
						ui.ScrollHorizontal(c.Key("object.strip")).Label("Open objects").Height(c.Theme().FontSize*1.25 + stripPadding).Shrink(0).Children(func() {
							tabs := ui.TabsBase(c.Key("open.tabs"), &selected, len(labels))
							tabs.List.NoWrap().Gap(t.Space(1)).Children(func() {
								for i, label := range labels {
									caption := label
									if i > 0 {
										name := w.objectTabs[i-1].row.Name
										count := 0
										for _, object := range w.objectTabs {
											if object.row.Name == name {
												count++
											}
										}
										if count == 1 {
											caption = name
										}
									}
									tab := tabs.Tab(i).Label(label).Tooltip(label).Padding(stripPadding/3, t.Space(3)).TextColor(t.TextMuted)
									if i == selected {
										tab.TextColor(t.Text).FontWeight(600).BorderColor(t.Accent).BorderWidth(0, 0, 0.5, 0)
										var target resource.Identity
										if i > 0 {
											target = w.objectTabs[i-1].target
										}
										if w.objectTabShown != target || w.objectTabScale != c.Preferences().TextScale {
											tab.ScrollIntoView()
											w.objectTabShown, w.objectTabScale = target, c.Preferences().TextScale
										}
									}
									tab.Children(func() { ui.Text(c, caption).SingleLine() })
								}
							})
							if tabs.List.Changed() {
								if selected == 0 {
									w.saveObjectDraft()
									w.clearObject()
								} else {
									tab := w.objectTabs[selected-1]
									w.openResourceKind(tab.kind, tab.row)
								}
							}
						})
					}
					if w.detail != nil {
						w.detailView(c)
						return
					}
					if w.objectRequest != nil {
						request := *w.objectRequest
						w.diagnosticLayout(c, func() {
							ui.Scroll(c.Key("object.load")).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
								ui.Column(c).Padding(16).Gap(12).Children(func() {
									ui.Text(c, request.row.Name).Bold().FontSize(t.FontSize * 1.4)
									ui.Text(c, request.kind.Label()+" / "+request.row.Namespace).TextColor(t.TextMuted)
									if w.objectLoadError == "" {
										ui.Row(c).Gap(8).Children(func() {
											ui.Spinner(c).Size(20, 20)
											ui.Text(c, "Loading object…")
										})
									} else {
										ui.Text(c, "Object unavailable").Bold()
										ui.Text(c, w.objectLoadError).Label("Object load error").TextColor(t.Danger)
									}
									ui.Row(c).Gap(8).Wrap().Children(func() {
										if w.objectLoadError != "" {
											ui.Button(c, "Retry object").OnClick(func() { w.openResourceKind(request.kind, request.row) })
										}
										ui.Button(c, "Close detail").OnClick(w.closeObjectTab)
									})
								})
							})
						})
						return
					}
					w.diagnosticLayout(c, func() {
						if w.focusDiagnosis {
							ui.Text(c, "Select a resource").TextColor(t.TextMuted).Padding(24)
							return
						}
						disconnected := false
						if len(w.rows) == 0 && w.backend == nil && !w.connectionPending && !w.vaultPending && !w.oidcPending {
							switch w.status {
							case "Not connected", "Disconnected", "No kubeconfig contexts", "Choose a context and connect":
								disconnected = true
							}
						}
						ui.Column(c).Grow(1).Basis(0).MinWidth(0).Padding(12).Gap(8).Children(func() {
							ui.Row(c).Gap(8).Wrap().Children(func() {
								ui.Text(c, w.currentKind.Kind).Bold().FontSize(t.FontSize * 1.4).
									SingleLine().MinWidth(0).Tooltip(w.currentKind.Kind)
								if len(w.rows) > 0 || w.status == "Live" {
									ui.Text(c, fmt.Sprintf("%d / %d resources", len(w.rows), w.total)).TextColor(t.TextMuted)
								}
							})
							if health.Supports(&unstructured.Unstructured{Object: map[string]any{"apiVersion": w.currentKind.GVR.GroupVersion().String(), "kind": w.currentKind.Kind}}) {
								if ui.Tabs(c.Key("resource.health"), &w.resourceView, "All", "Needs attention").Label("Resource health filter").Hidden(disconnected).Changed() {
									w.queryChanged()
								}
							}
							if w.resourceView == 1 {
								ui.Button(c, "Focus diagnosis").Hidden(disconnected).Disabled(w.backend == nil).OnClick(func() { w.focusDiagnosis = true })
							}
							ui.Row(c.Key("resource.actions")).Role(ui.RoleToolbar).FocusGroup(ui.Horizontal).AlignItems(ui.Center).Gap(t.Space(1)).Padding(t.Space(1)).MinWidth(0).Wrap().Children(func() {
								ui.Button(c, "Refresh").Hidden(w.backend == nil).Disabled(w.backend == nil).OnClick(func() {
									w.startScope()
								})
								ui.Button(c, "New resource").Hidden(w.backend == nil).Disabled(w.backend == nil || w.currentKind.GVR.Resource == "secrets").OnClick(func() {
									w.beginCreate()
								})
								ui.PopoverMenuButton(c, "Tools", func(m *ui.Menu) {
									if m.Item("Deliver manifests").Disabled(w.backend == nil || w.pendingWrites != 0).Chosen() {
										w.openDelivery()
									}
									if w.detail == nil {
										if m.Item("Helm releases").Disabled(w.backend == nil || w.pendingWrites != 0).Chosen() {
											w.openHelmReleases()
										}
										if m.Item("Helm recovery").Disabled(w.pendingWrites != 0).Chosen() {
											w.openHelmRecovery()
										}
									}
								}).Hidden(w.backend == nil)
								if w.detail == nil && w.backend == nil {
									ui.Button(c, "Helm recovery").Disabled(w.pendingWrites != 0).OnClick(w.openHelmRecovery)
								}
							}).Label("Resource actions")
							ui.Row(c).Hidden(disconnected).Gap(8).Wrap().Children(func() {
								if ui.SearchField(c.Key("field.filter"), &w.filter).Label("Filter resources").Grow(1).Changed() {
									w.queryChanged()
								}
								if ui.Select(c.Key("field.sortBy"), &w.sortBy, []string{"Name", "Namespace", "Status"}).Label("Sort resources").Changed() {
									w.queryChanged()
								}
								filters := "Filters"
								if w.appliedLabelSelector != "" {
									filters = "Filters (1)"
								}
								ui.Button(c, filters).Label("Filters").Tooltip(w.appliedLabelSelector).OnClick(func() { w.filtersOpen = !w.filtersOpen })
							})
							if w.filtersOpen {
								ui.Row(c).Hidden(disconnected).Gap(8).Wrap().Children(func() {
									ui.TextInput(c.Key("field.labelSelector"), &w.labelSelector).Label("Label selector").Placeholder("app=api").Grow(1)
									if w.labelSelector != w.appliedLabelSelector {
										ui.Text(c, "Not applied").TextColor(t.TextMuted)
									}
									ui.Button(c, "Apply selector").Disabled(w.backend == nil || w.labelSelector == w.appliedLabelSelector).OnClick(func() {
										w.appliedLabelSelector = w.labelSelector
										w.startScope()
									})
								})
							}
							if w.errText != "" {
								ui.Text(c, w.errText).TextColor(t.Danger)
							}
							cols := []ui.TableColumn{{ID: "name", Title: "Name"}}
							if w.currentKind.Namespaced && (w.activeNamespace == "" || w.activeNamespace == "all namespaces") {
								cols = append(cols, ui.TableColumn{ID: "namespace", Title: "Namespace", Width: 130 * c.Preferences().TextScale})
							}
							statusWidth := float32(150)
							if len(cols) == 1 {
								statusWidth = 200 * c.Preferences().TextScale
							}
							cols = append(cols, ui.TableColumn{ID: "status", Title: "Status", Width: statusWidth})
							table := ui.Table(c.Key("field.table"), &w.table, cols, len(w.rows), func(row, col int) {
								r := w.rows[row]
								switch cols[col].ID {
								case "name":
									open := func() { w.openResource(r) }
									ui.ButtonBase(c).Justify(ui.Start).Padding(6, 0).Label(r.Name).Tooltip(r.Name).TextColor(t.Focus).OnClick(open).OnShortcut(0, ui.KeyEnter, open).Children(func() { ui.Text(c, r.Name).SingleLine() })
								case "namespace":
									ui.Text(c, r.Namespace).SingleLine()
								case "status":
									status := r.Status
									if w.resourceView == 1 {
										status = string(r.Health)
									}
									label := ui.Text(c, status).SingleLine().Tooltip(r.Status)
									if r.Health == health.Degraded || r.Health == health.Failed {
										label.TextColor(t.Danger).Bold()
									}
								}
							}).Grow(1)
							if disconnected {
								table.Hidden(true)
								ui.Column(c).Padding(16, 8).Gap(8).AlignItems(ui.Start).Children(func() {
									ui.Text(c, "No cluster connected").TextColor(t.TextMuted)
									ui.Button(c, "Open connections").OnClick(func() { w.connectionsOpen = true })
								})
								ui.Box(c).Grow(1)
							}
							if len(w.rows) == 0 && w.status == "Live" {
								table.Hidden(true)
								ui.Text(c, "No resources match this scope and filter.").TextColor(t.TextMuted).Padding(16, 8)
								ui.Box(c).Grow(1)
							}

						})
					})
				})
			})
		})
		ui.Row(c).MinHeight(34).Shrink(0).Padding(6, 12).Gap(12).Children(func() {
			ui.Text(c, w.connectionStatus()).Label("Connection status").Shrink(0).SingleLine()
			ui.Text(c, w.activeContext+" / "+w.activeNamespace).Label("Active connection").Hidden(w.activeContext == "").Grow(1).MinWidth(0).SingleLine().TextColor(t.TextMuted).Tooltip(w.activeContext + " / " + w.activeNamespace)
			ui.Text(c, fmt.Sprintf("%d active write(s)", w.pendingWrites)).Hidden(w.pendingWrites == 0).TextColor(t.TextMuted)
			if w.forwardLocal != 0 {
				ui.Text(c, fmt.Sprintf("Forward: 127.0.0.1:%d", w.forwardLocal))
			}
			if len(w.history) > 0 {
				ui.Text(c, w.history[len(w.history)-1]).Grow(1).SingleLine().TextColor(t.TextMuted)
			}
		})
	})
}

func (w *Workbench) resourceTaskVisible() bool {
	return !w.applications.open && !w.connectionsOpen && !w.oidcOpen && !w.vaultOpen && !w.delivery.open && !w.helmReleases.open && !w.helmUninstall.open
}
