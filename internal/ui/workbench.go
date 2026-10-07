package uiworkbench

import (
	"fmt"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type Workbench struct {
	cluster    string
	namespace  string
	status     string
}

func New() *Workbench {
	return &Workbench{cluster: "No cluster", namespace: "default", status: "Ready"}
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
			ui.Column(c).Fill().Padding(20).Gap(12).Children(func() {
				ui.Text(c, "Cluster workspace").Bold().FontSize(22)
				ui.Text(c, "Connect a Kubernetes cluster to inspect resources, logs, events and operations.").TextColor(theme.TextMuted)
				ui.Row(c).Gap(8).Children(func() {
					if ui.PrimaryButton(c, "Add cluster").Clicked() {
						w.status = "Cluster connection flow is being configured"
					}
					if ui.Button(c, "Refresh").Clicked() {
						w.status = "Refreshed"
					}
				})
				ui.Text(c, fmt.Sprintf("Status: %s", w.status)).FontSize(12).TextColor(theme.TextMuted)
			})
		})
	})
}

func Open() {
	w := New()
	mygo.NewWindow(mygo.WindowOptions{
		Title:   "Aster",
		Width:   1280,
		Height:  800,
		Content: ui.View(w.View),
	})
}
