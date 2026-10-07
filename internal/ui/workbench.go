package uiworkbench

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/kubeconfig"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type resourceRow struct {
	UID       string
	Name      string
	Namespace string
	Created   string
}

type Workbench struct {
	cluster   string
	namespace string
	status    string
	errText   string
	rows      []resourceRow
	table     ui.ListState
	selected  int
}

func New() *Workbench {
	w := &Workbench{cluster: "No cluster", namespace: "default", status: "Connecting"}
	w.table.Selected = &w.selected
	w.table.Key = func(i int) any {
		if i < 0 || i >= len(w.rows) {
			return ""
		}
		return w.rows[i].UID
	}
	return w
}

func (w *Workbench) View(c *ui.Context) {
	theme := c.Theme()
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(48).Padding(12).Gap(12).Children(func() {
			ui.Text(c, "Aster").Bold().FontSize(18)
			ui.Text(c, w.cluster).TextColor(theme.TextMuted)
			ui.Text(c, w.namespace).TextColor(theme.TextMuted)
		})
		ui.Row(c).Fill().Children(func() {
			ui.Column(c).Width(220).Padding(12).Gap(8).Children(func() {
				ui.Text(c, "WORKSPACE").FontSize(11).TextColor(theme.TextMuted)
				for _, label := range []string{"Applications", "Workloads", "Network", "Storage", "Configuration", "Access Control", "Custom Resources"} {
					ui.Button(c, label).Fill()
				}
			})
			ui.Column(c).Fill().Padding(16).Gap(10).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, "Pods").Bold().FontSize(20)
					ui.Text(c, fmt.Sprintf("%d resources", len(w.rows))).TextColor(theme.TextMuted)
				})
				if w.errText != "" {
					ui.Text(c, w.errText).TextColor(theme.TextMuted)
				}
				cols := []ui.TableColumn{
					{ID: "name", Title: "Name", Sortable: true},
					{ID: "namespace", Title: "Namespace", Width: 180},
					{ID: "created", Title: "Created", Width: 210},
				}
				ui.Table(c, &w.table, cols, len(w.rows), func(row, col int) {
					item := w.rows[row]
					switch col {
					case 0:
						ui.Text(c, item.Name).SingleLine()
					case 1:
						ui.Text(c, item.Namespace).SingleLine()
					case 2:
						ui.Text(c, item.Created).SingleLine()
					}
				}).Grow(1)
				ui.Text(c, "Status: "+w.status).FontSize(12).TextColor(theme.TextMuted)
			})
		})
	})
}

func Open() {
	w := New()
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:   "Aster",
		Width:   1280,
		Height:  800,
		Content: ui.View(w.View),
	})
	go w.loadDefaultCluster(win)
}

func (w *Workbench) loadDefaultCluster(win *mygo.Window) {
	conn, err := kubeconfig.LoadDefault()
	if err != nil {
		win.Update(func() {
			w.status = "No kubeconfig connection"
			w.errText = err.Error()
		})
		return
	}
	backend, err := kube.New(conn.Config)
	if err != nil {
		win.Update(func() {
			w.status = "Connection failed"
			w.errText = err.Error()
		})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	items, _, err := backend.ListObjects(ctx, schema.GroupVersionResource{Version: "v1", Resource: "pods"}, conn.Namespace, metav1.ListOptions{})
	if err != nil {
		win.Update(func() {
			w.cluster = conn.ContextName
			w.namespace = conn.Namespace
			w.status = "Resource query failed"
			w.errText = err.Error()
		})
		return
	}
	rows := make([]resourceRow, 0, len(items))
	for i := range items {
		item := &items[i]
		rows = append(rows, resourceRow{
			UID:       string(item.GetUID()),
			Name:      item.GetName(),
			Namespace: item.GetNamespace(),
			Created:   item.GetCreationTimestamp().UTC().Format(time.RFC3339),
		})
	}
	win.Update(func() {
		w.cluster = conn.ContextName
		w.namespace = conn.Namespace
		w.rows = rows
		w.status = "Live"
		w.errText = ""
	})
}
