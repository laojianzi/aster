package uiworkbench

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/eventqueue"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/kubeconfig"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
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
	ctx, cancel := context.WithCancel(context.Background())
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:    "Aster",
		Width:    1280,
		Height:   800,
		MinWidth: 900,
		MinHeight: 600,
		StateKey: "main",
		Content:  ui.View(w.View),
	})
	win.OnClosed(cancel)
	go w.runClusterSession(ctx, win)
}

func (w *Workbench) runClusterSession(ctx context.Context, win *mygo.Window) {
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
	win.Update(func() {
		w.cluster = conn.ContextName
		w.namespace = conn.Namespace
		w.status = "Synchronizing"
		w.errText = ""
	})

	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	queue := eventqueue.New[string, kube.ResourceEvent](4096)
	defer queue.Close()
	batchDone := make(chan struct{})
	go func() {
		defer close(batchDone)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
				events := queue.Drain(256)
				if len(events) == 0 {
					continue
				}
				win.Update(func() {
					w.apply(events)
					w.status = "Live"
				})
			}
		}
	}()

	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	err = backend.WatchResource(watchCtx, gvr, conn.Namespace, metav1.ListOptions{}, func(event kube.ResourceEvent) error {
		uid := string(event.Object.GetUID())
		if uid == "" {
			uid = event.Object.GetNamespace() + "/" + event.Object.GetName()
		}
		return queue.Put(uid, event)
	})
	stopWatch()
	if err != nil && !errors.Is(err, context.Canceled) {
		win.Update(func() {
			w.status = "Watch failed"
			w.errText = err.Error()
		})
	}
	<-batchDone
}

func (w *Workbench) apply(events []kube.ResourceEvent) {
	for _, event := range events {
		uid := string(event.Object.GetUID())
		index := -1
		for i := range w.rows {
			if w.rows[i].UID == uid {
				index = i
				break
			}
		}
		if event.Type == watch.Deleted {
			if index >= 0 {
				w.rows = append(w.rows[:index], w.rows[index+1:]...)
			}
			continue
		}
		row := resourceRow{
			UID:       uid,
			Name:      event.Object.GetName(),
			Namespace: event.Object.GetNamespace(),
			Created:   event.Object.GetCreationTimestamp().UTC().Format(time.RFC3339),
		}
		if index >= 0 {
			w.rows[index] = row
		} else {
			w.rows = append(w.rows, row)
		}
	}
}
