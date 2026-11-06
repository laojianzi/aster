package uiworkbench

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/health"
	"github.com/laojianzi/aster/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type applicationResource struct {
	application string
	kind        kube.ResourceKind
	row         resourceRow
}

type applicationState struct {
	open, busy, showIssues      bool
	epoch                       uint64
	cancel                      context.CancelFunc
	namespace, selected, status string
	resources                   []applicationResource
	names, issues               []string
	table                       ui.ListState
	view                        int
	graph                       applicationMap
	showMapIssues               bool
}

func (w *Workbench) clearApplications() {
	a := &w.applications
	if a.cancel != nil {
		a.cancel()
	}
	w.applications = applicationState{epoch: a.epoch + 1}
}

func (w *Workbench) openApplications() {
	if w.backend == nil || w.pendingWrites != 0 {
		return
	}
	resume := !w.applications.open && w.applications.namespace == strings.TrimSpace(w.namespace) && w.applications.status != ""
	selected := w.applications.selected
	if !resume {
		w.clearApplications()
	}
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
	w.connectionsOpen = false
	a := &w.applications
	if resume {
		a.open = true
		return
	}
	a.open, a.namespace, a.selected = true, strings.TrimSpace(w.namespace), selected
	if a.namespace == "" || a.namespace == "*" {
		a.status = "Choose a namespace to browse applications."
		return
	}
	a.busy, a.status = true, "Loading applications…"
	epoch, connection, session, ns, backend := a.epoch, w.contextEpoch, w.sessionID, a.namespace, w.backend
	kinds := slices.Clone(w.kinds)
	ctx, cancel := context.WithTimeout(w.operationContext(), 20*time.Second)
	a.cancel = cancel
	w.run(func(context.Context) {
		defer cancel()
		var resources []applicationResource
		var names, issues []string
		requests := 0
		exhausted := false
		for _, kind := range kinds {
			if !kind.Namespaced || kind.GVR.Resource == "secrets" {
				continue
			}
			token := ""
			for {
				if requests >= 64 || len(resources) >= 2000 {
					issues = append(issues, "Read limit reached; some resources were not loaded.")
					exhausted = true
					break
				}
				requests++
				page, err := backend.ListPage(ctx, kind.GVR, ns, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name", Limit: 200, Continue: token})
				if err != nil {
					issues = append(issues, kind.Label()+": "+err.Error())
					break
				}
				for i := range page.Items {
					obj := &page.Items[i]
					app := obj.GetLabels()["app.kubernetes.io/name"]
					if app == "" {
						continue
					}
					if len(resources) >= 2000 {
						issues = append(issues, "Resource limit reached; some resources were not loaded.")
						exhausted = true
						break
					}
					resources = append(resources, applicationResource{app, kind, project(obj)})
					if !slices.Contains(names, app) {
						names = append(names, app)
					}
				}
				token = page.GetContinue()
				if token == "" || exhausted {
					break
				}
			}
			if exhausted || ctx.Err() != nil {
				break
			}
		}
		slices.Sort(names)
		slices.SortFunc(resources, func(a, b applicationResource) int {
			if v := strings.Compare(a.application, b.application); v != 0 {
				return v
			}
			if v := strings.Compare(a.kind.Label(), b.kind.Label()); v != 0 {
				return v
			}
			return strings.Compare(a.row.key(), b.row.key())
		})
		w.emit(func() {
			a := &w.applications
			if a.epoch != epoch || w.contextEpoch != connection || w.sessionID != session || w.backend != backend || strings.TrimSpace(w.namespace) != ns {
				return
			}
			a.busy, a.cancel = false, nil
			a.resources, a.names, a.issues = resources, names, issues
			if !slices.Contains(names, a.selected) {
				a.selected = ""
				if len(names) > 0 {
					a.selected = names[0]
				}
			}
			a.status = fmt.Sprintf("%d applications / %d resources", len(names), len(resources))
			if len(issues) > 0 {
				a.status = "Partial results"
				if len(names) == 0 {
					a.status = "No applications loaded"
				}
			} else if len(names) == 0 {
				a.status = "No labelled applications in this namespace."
			}
		})
	})
}

func (w *Workbench) applicationsView(c *ui.Context) {
	a := &w.applications
	t := c.Theme()
	ui.Column(c.Key("applications.workspace")).Grow(1).MinHeight(0).MinWidth(0).Padding(16).Gap(8).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, "Applications").Bold().FontSize(t.FontSize * 1.4).Tooltip("Grouped by app.kubernetes.io/name")
			ui.Text(c, a.namespace).TextColor(t.TextMuted)
			ui.Text(c, a.status).TextColor(t.TextMuted)
			ui.Box(c).Grow(1)
			ui.Button(c, "Refresh").Label("Refresh applications").Disabled(a.busy).OnClick(w.openApplications)
		})
		if len(a.issues) > 0 {
			ui.Collapsible(c, "Read issues", &a.showIssues, func() { ui.Text(c, strings.Join(a.issues, "\n")) })
		}
		ui.Row(c).Hidden(len(a.names) == 0).Grow(1).MinHeight(0).MinWidth(0).AlignItems(ui.Stretch).Gap(16).Children(func() {
			if ui.Sidebar(c.Key("application.navigation"), &a.selected, func() {
				for _, name := range a.names {
					ui.SidebarItem(c.Key(name), name, nil, name).Label("Application " + name).Tooltip(name)
				}
			}).Label("Application navigation").Width(220).MinHeight(0).Changed() {
				a.table = ui.ListState{}
				a.graph = applicationMap{}
				if a.cancel != nil {
					a.cancel()
					a.cancel = nil
				}
				a.epoch++
				if a.view == 1 {
					w.loadApplicationMap()
				}
			}
			ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Gap(8).Children(func() {
				if ui.Tabs(c.Key("application.views"), &a.view, "Members", "Map").Changed() && a.view == 1 && a.graph.status == "" {
					w.loadApplicationMap()
				}
				if a.view == 1 {
					w.applicationMapView(c)
					return
				}
				var members []applicationResource
				for _, item := range a.resources {
					if item.application == a.selected {
						members = append(members, item)
					}
				}
				scale := c.Preferences().TextScale
				columns := []ui.TableColumn{{ID: "kind", Title: "Kind", Width: 150 * scale}, {ID: "name", Title: "Name", MinWidth: 160 * scale}, {ID: "health", Title: "Status", Width: 170 * scale}}
				ui.Table(c.Key("application.members"), &a.table, columns, len(members), func(row, col int) {
					item := members[row]
					switch col {
					case 0:
						ui.Text(c, item.kind.Kind).SingleLine().Tooltip(item.kind.Kind)
					case 1:
						ui.Button(c, item.row.Name).Shrink(1).MinWidth(0).Label("Open " + item.kind.Kind + "/" + item.row.Name).Tooltip(item.row.Name).OnClick(func() { a.open = false; w.openResourceKind(item.kind, item.row) })
					case 2:
						status := string(item.row.Health)
						if item.row.Health == health.Unsupported {
							status = item.row.Status
						}
						ui.Text(c, status).SingleLine().Tooltip(status)
					}
				}).Grow(1).MinWidth(0).MinHeight(0)
			})
		})
	})
}
