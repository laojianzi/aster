package uiworkbench

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

func (w *Workbench) View(c *ui.Context) {
	w.frames++
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Row(c).Height(58).Padding(12).Gap(10).Children(func() {
			ui.Text(c, "Aster").Bold().FontSize(22)
			ui.Text(c, "Native Kubernetes Workbench").FontSize(12).TextColor(t.TextMuted)
			if ui.Select(c, &w.currentContext, w.contexts).Label("Cluster context").Width(250).Changed() {
				w.namespace = ""
				w.trustedFingerprint = ""
				w.trustRequired = false
				w.disconnect()
			}
			ui.TextInput(c, &w.namespace).Label("Namespace").Placeholder("Namespace or *").Width(150)
			if ui.PrimaryButton(c, "Connect").Disabled(w.currentContext == "" || w.connectionPending || w.vaultPending).Clicked() {
				w.connect()
			}
			if ui.Button(c, "Disconnect").Disabled(w.backend == nil && !w.connectionPending && !w.vaultPending).Clicked() {
				w.disconnect()
			}
		})
		ui.Row(c).Height(44).Padding(6, 12).Gap(8).Children(func() {
			if ui.TextInput(c, &w.path).Label("Kubeconfig path").Placeholder("Kubeconfig path (empty: default)").Width(320).Changed() {
				w.trustedFingerprint = ""
				w.trustRequired = false
				w.disconnect()
			}
			if ui.Button(c, "Load contexts").Clicked() {
				w.loadContexts()
			}
			if ui.Button(c, "Credentials").Clicked() {
				w.vaultOpen = !w.vaultOpen
				w.clearVault()
			}
			ui.Text(c, w.notice).FontSize(12).TextColor(t.TextMuted).Grow(1).SingleLine()
			if w.workspaceNumber > 0 {
				ui.Text(c, fmt.Sprintf("Workspace %d", w.workspaceNumber)).Label("Workspace identity").FontSize(12).SingleLine()
			}
			if ui.Button(c, "New workspace").Disabled(w.newWorkspace == nil).Clicked() {
				if err := w.newWorkspace(); err != nil {
					w.notice = err.Error()
				}
			}
		})
		if w.trustRequired {
			ui.Row(c).Padding(10).Gap(10).Children(func() {
				ui.Text(c, "This context requests local credentials, executable authentication or unsafe transport. Trust only a configuration you control.").FontSize(12)
				if ui.Button(c, "Trust this context and connect").Disabled(w.connectionPending).Clicked() {
					w.trustedFingerprint = w.pendingTrustFingerprint
					w.connect()
				}
			})
		}
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			if w.vaultOpen {
				w.vaultView(c)
				return
			}
			ui.Column(c).Width(174).Padding(10).Gap(5).Children(func() {
				ui.Text(c, "RESOURCES").FontSize(11).Bold().TextColor(t.TextMuted)
				for _, item := range []struct{ Label, Resource string }{{"Pods", "pods"}, {"Deployments", "deployments"}, {"StatefulSets", "statefulsets"}, {"DaemonSets", "daemonsets"}, {"Jobs", "jobs"}, {"CronJobs", "cronjobs"}, {"Services", "services"}, {"Ingresses", "ingresses"}, {"ConfigMaps", "configmaps"}, {"Secrets", "secrets"}, {"Volume Claims", "persistentvolumeclaims"}, {"Nodes", "nodes"}, {"Namespaces", "namespaces"}} {
					if ui.Button(c, item.Label).Width(152).Height(30).Clicked() {
						for _, kind := range catalog() {
							if kind.GVR.Resource == item.Resource {
								w.chooseKind(kind)
								break
							}
						}
					}
				}
			})
			ui.Column(c).Grow(1).Padding(12).Gap(8).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, w.currentKind.Kind).Bold().FontSize(20)
					ui.Text(c, fmt.Sprintf("%d / %d resources", len(w.rows), w.total)).FontSize(12).TextColor(t.TextMuted)
				})
				ui.Row(c).Gap(8).Children(func() {
					if ui.Button(c, "Refresh").Disabled(w.backend == nil).Clicked() {
						w.startScope()
					}
					if ui.Button(c, "New resource").Disabled(w.backend == nil || w.currentKind.GVR.Resource == "secrets").Clicked() {
						w.beginCreate()
					}
				})
				labels := make([]string, 0, len(w.kinds))
				for _, k := range w.kinds {
					labels = append(labels, k.Label())
				}
				if ui.Select(c, &w.kindChoice, labels).Label("Resource kind").Changed() {
					for _, k := range w.kinds {
						if k.Label() == w.kindChoice {
							w.chooseKind(k)
							break
						}
					}
				}
				ui.Row(c).Gap(8).Children(func() {
					if ui.TextInput(c, &w.filter).Label("Filter resources").Placeholder("Filter name, namespace or status").Grow(1).Changed() {
						w.queryChanged()
					}
					if ui.Select(c, &w.sortBy, []string{"Name", "Namespace", "Status"}).Label("Sort resources").Width(115).Changed() {
						w.queryChanged()
					}
				})
				ui.Row(c).Gap(8).Children(func() {
					ui.TextInput(c, &w.labelSelector).Label("Label selector").Placeholder("Server label selector, e.g. app=api").Grow(1)
					if ui.Button(c, "Apply selector").Disabled(w.backend == nil).Clicked() {
						w.startScope()
					}
				})
				if w.errText != "" {
					ui.Text(c, w.errText).FontSize(12).TextColor(t.Danger)
				}
				cols := []ui.TableColumn{{ID: "name", Title: "Name"}, {ID: "namespace", Title: "Namespace", Width: 130}, {ID: "status", Title: "Status", Width: 150}}
				ui.Table(c, &w.table, cols, len(w.rows), func(row, col int) {
					r := w.rows[row]
					switch col {
					case 0:
						if ui.Button(c, r.Name).Clicked() {
							w.openResource(r)
						}
					case 1:
						ui.Text(c, r.Namespace).SingleLine()
					case 2:
						ui.Text(c, r.Status).SingleLine()
					}
				}).Grow(1)
				if len(w.rows) == 0 && w.status == "Live" {
					ui.Text(c, "No resources match this scope and filter.").TextColor(t.TextMuted)
				}
				ui.Text(c, "Select a resource to inspect or prepare a change.").FontSize(11).TextColor(t.TextMuted)
			})
			if w.detail != nil {
				w.detailView(c)
			}
		})
		ui.Row(c).Height(34).Padding(6, 12).Gap(12).Children(func() {
			ui.Text(c, "Status: "+w.connectionStatus()).Label("Connection status").FontSize(12).Grow(1).SingleLine()
			ui.Text(c, "Connected: "+w.activeContext+" / "+w.activeNamespace).FontSize(11).TextColor(t.TextMuted)
			ui.Text(c, fmt.Sprintf("%d active write(s)", w.pendingWrites)).FontSize(12).TextColor(t.TextMuted)
			if w.forwardLocal != 0 {
				ui.Text(c, fmt.Sprintf("Forward: 127.0.0.1:%d", w.forwardLocal)).FontSize(11)
			}
			if len(w.history) > 0 {
				ui.Text(c, w.history[len(w.history)-1]).FontSize(11).Grow(1).SingleLine().TextColor(t.TextMuted)
			}
		})
	})
}
