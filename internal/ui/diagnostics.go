package uiworkbench

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/resource"
	"strings"
)

type diagnosticBinding struct {
	target      resource.Identity
	context     string
	mode        string
	scope       string
	containers  []string
	expanded    bool
	layoutReady bool
}

func (w *Workbench) bindDiagnostic() bool {
	if w.diagnostic != nil {
		return true
	}
	if w.detail == nil || w.creating || w.detailKind.GVR.Group != "" || w.detailKind.GVR.Resource != "pods" {
		return false
	}
	w.diagnosticEpoch++
	w.container = ""
	if len(w.containers) > 0 {
		w.container = w.containers[0]
	}
	w.diagnostic = &diagnosticBinding{target: w.target(), context: w.activeContext, scope: strings.TrimSpace(w.namespace), mode: w.detailMode, containers: append([]string(nil), w.containers...)}
	return true
}

func (w *Workbench) selectDiagnostic(mode string) {
	if w.diagnostic != nil && w.diagnostic.target != w.target() {
		w.clearDiagnostics()
	}
	if w.bindDiagnostic() {
		w.diagnostic.mode = mode
	}
	if mode != "Logs" {
		w.stopLogs()
	}
	if mode != "Terminal" {
		w.stopTerminal()
	}
}

func (w *Workbench) clearDiagnostics() {
	w.diagnosticEpoch++
	w.stopTerminal()
	w.terminalArgv, w.terminalContainer, w.terminalStatus = "", "", ""
	w.stopCommand()
	w.commandArgv, w.commandContainer, w.commandOutput, w.commandStatus = "", "", "", ""
	w.stopForward()
	w.stopLogs()
	w.logRows, w.logDropped, w.logStatus = nil, 0, ""
	w.diagnostic = nil
}

func (w *Workbench) closeDiagnostic() {
	w.clearDiagnostics()
	switch w.detailMode {
	case "Logs", "Terminal", "Command", "Forward":
		w.detailMode = "Overview"
	}
}

func (w *Workbench) diagnosticLayout(c *ui.Context, body func()) {
	t := c.Theme()
	open := w.diagnostic != nil
	if open && !w.diagnostic.layoutReady {
		_, height := c.Size()
		w.diagnostic.expanded = height < 300*c.Preferences().TextScale
		w.diagnostic.layoutReady = true
	}
	if open && w.diagnosticOverviewPercent == 0 {
		w.diagnosticOverviewPercent = 25
		if _, height := c.Size(); height >= 800 {
			w.diagnosticOverviewPercent = 40
		}
	}
	expanded := open && w.diagnostic.expanded
	layout := ui.Column(c.Key("diagnostics.layout")).Grow(1).MinHeight(0)
	layout.Children(func() {
		pane := ui.Box(c.Key("diagnostics.body")).Grow(1).MinHeight(0).Clip().Hidden(expanded)
		share := min(max(w.diagnosticOverviewPercent, 0), 100) / 100
		minimum, grip := t.Space(10), t.Space(1.5)
		if open {
			pane.Grow(share).Shrink(1).MinHeight(minimum)
		}
		pane.Children(body)
		if !open {
			return
		}
		total := layout.LayoutBounds().H - grip
		shown := pane.LayoutBounds().H
		divider := ui.Box(c.Key("diagnostics.divider")).Height(grip).Shrink(0).Focusable().Role(ui.RoleSplitter).Label("Divider").Cursor(ui.CursorResizeNS).Hidden(expanded)
		divider.Range(float64(minimum), float64(max(total-minimum, minimum)), float64(shown)).Step(float64(t.Space(2.5)))
		_, dy, held := divider.Dragged()
		if held || divider.Clicked() {
			divider.Focus()
		}
		if divider.Shortcut(0, ui.KeyUp) {
			dy -= t.Space(2.5)
		}
		if divider.Shortcut(0, ui.KeyDown) {
			dy += t.Space(2.5)
		}
		if dy != 0 && total > 0 {
			w.diagnosticOverviewPercent = max(minimum, min(shown+dy, total-minimum)) * 100 / total
			c.AnimationFrame()
		}
		divider.Draw(func(p *ui.Painter, r ui.Rect) {
			color := t.Border
			if held || divider.Hovered() {
				color = t.Accent
			}
			p.Fill(ui.Rect{X: r.X, Y: r.Y + (r.H-0.5)/2, W: r.W, H: 0.5}, color, 0)
			if divider.FocusVisible() {
				p.FocusRing(r, [4]float32{})
			}
		})
		dockShare := 1 - share
		if expanded {
			dockShare = 1
		}
		ui.Box(c.Key("diagnostics.dock")).Grow(dockShare).Shrink(1).MinHeight(minimum).Clip().Children(func() { w.diagnosticView(c) })
	})
}

func (w *Workbench) diagnosticView(c *ui.Context) {
	expandLabel := "Expand diagnostic"
	if w.diagnostic.expanded {
		expandLabel = "Restore split"
	}
	label := "Diagnostic dock"
	if w.diagnostic.mode == "Logs" {
		label = "Log dock"
	}
	dock := ui.Scroll(c.Key(w.diagnosticEpoch)).Label(label).Grow(1).MinHeight(0)
	dock.Children(func() {
		ui.Column(c).MinHeight(dock.LayoutBounds().H).Gap(6).Children(func() {
			if w.diagnostic.mode != "Logs" {
				ui.Row(c).Gap(6).Wrap().Children(func() {
					ui.Text(c, w.diagnostic.mode+" / "+w.diagnostic.target.Namespace+"/"+w.diagnostic.target.Name).Label("Diagnostic target").Tooltip(w.diagnostic.context + " / " + string(w.diagnostic.target.UID)).Bold().Grow(1).MinWidth(0).SingleLine()
					ui.Button(c, expandLabel).OnClick(func() { w.diagnostic.expanded = !w.diagnostic.expanded })
					ui.Button(c, "Close diagnostic").OnClick(w.closeDiagnostic)
				})
				switch w.diagnostic.mode {
				case "Terminal":
					w.terminalView(c)
				case "Command":
					w.commandView(c)
				case "Forward":
					w.forwardView(c)
				}
				return
			}
			ui.Row(c).Gap(6).Wrap().Children(func() {
				ui.Text(c, w.diagnostic.target.Namespace+"/"+w.diagnostic.target.Name).Label("Diagnostic target").Tooltip(w.diagnostic.context + " / " + string(w.diagnostic.target.UID)).SingleLine()
				if ui.Select(c.Key("field.container"), &w.container, w.diagnostic.containers).Label("Container").Grow(1).MinWidth(0).Changed() {
					w.stopLogs()
					w.logRows = nil
					w.logDropped = 0
					w.logStatus = "Select a log action for this container"
				}
				ui.Button(c, expandLabel).OnClick(func() { w.diagnostic.expanded = !w.diagnostic.expanded })
				ui.Button(c, "Close logs").OnClick(w.closeDiagnostic)
			})
			ui.Row(c).Gap(6).Wrap().Children(func() {
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
			ui.List(c.Key("field.logList"), &w.logList, len(w.logRows), func(i int) { ui.Text(c, w.logRows[i]).SingleLine() }).Label("Log output").Grow(1).MinHeight(80)
			status := w.logStatus
			if w.logDropped > 0 {
				if status != "" {
					status += " / "
				}
				status += fmt.Sprintf("Truncated lines: %d", w.logDropped)
			}
			if status != "" {
				ui.Text(c, status).FontSize(12 * max(1, c.Preferences().TextScale))
			}
		})
	})
}
