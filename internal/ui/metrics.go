package uiworkbench

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/resourcemetrics"
)

func (w *Workbench) stopMetrics() {
	w.metricsEpoch++
	if w.metricsCancel != nil {
		w.metricsCancel()
		w.metricsCancel = nil
	}
	w.metricsActive, w.metricsLoading = false, false
}
func (w *Workbench) loadMetrics(live bool) {
	if w.detail == nil || w.backend == nil || w.creating || !resourcemetrics.Supported(w.detailKind.GVR) {
		return
	}
	w.stopMetrics()
	epoch, revision := w.detailEpoch, w.metricsEpoch
	target, backend := w.target(), w.backend
	w.detailMode = "Metrics"
	w.metricsActive = live
	w.metricsLoading = true
	ctx, cancel := context.WithCancel(w.operationContext())
	w.metricsCancel = cancel
	w.run(func(context.Context) {
		defer cancel()
		for {
			snapshot := resourcemetrics.Read(ctx, backend, target)
			if ctx.Err() != nil {
				return
			}
			ack := make(chan struct{})
			w.emit(func() {
				defer close(ack)
				if epoch != w.detailEpoch || revision != w.metricsEpoch || w.detailMode != "Metrics" {
					return
				}
				w.metricsLoading = false
				w.metricsResult = snapshot
				w.metricsHistory.Add(snapshot)
			})
			select {
			case <-ack:
			case <-ctx.Done():
				return
			}
			if !live {
				return
			}
			// Delay after completion rather than a ticker: never overlap slow requests.
			timer := time.NewTimer(15 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	})
}
func (w *Workbench) metricsView(c *ui.Context) {
	ui.Scroll(c.Key("metrics.content")).Label("Metrics content").Grow(1).MinHeight(0).Gap(12).Children(func() { w.metricsContents(c) })
}
func (w *Workbench) metricsContents(c *ui.Context) {
	ui.Row(c).Gap(6).Wrap().Children(func() {
		ui.Button(c, "Refresh metrics").Disabled(w.metricsLoading || w.metricsActive).OnClick(func() {
			w.loadMetrics(false)
		})
		ui.Button(c, "Start sampling").Disabled(w.metricsActive).OnClick(func() {
			w.loadMetrics(true)
		})
		ui.Button(c, "Pause metrics").Disabled(!w.metricsActive && !w.metricsLoading).OnClick(func() {
			w.stopMetrics()
		})
	})
	state := string(w.metricsResult.State)
	if state == "" {
		state = "No metrics requested"
	}
	if w.metricsLoading {
		state = "Reading metrics…"
	}
	if w.metricsActive {
		state += "; sampling"
	} else if !w.metricsLoading {
		state += "; snapshot"
	}
	ui.Text(c, state).Label("Metrics state").Bold().SingleLine()
	ui.Row(c).Gap(24).Wrap().Children(func() {
		for _, metric := range []struct {
			label  string
			memory bool
		}{{"CPU cores", false}, {"Memory MiB", true}} {
			ui.Column(c).Grow(1).Basis(0).MinWidth(280 * max(1, c.Preferences().TextScale)).Gap(6).Children(func() {
				drawMetric(c, metric.label, w.metricsHistory.Points, metric.memory)
			})
		}
	})
	entries := w.metricsResult.Entries
	if len(entries) > 0 {
		scale := max(1, c.Preferences().TextScale)
		columns := []ui.TableColumn{{ID: "name", Title: "Resource"}, {ID: "cpu", Title: "CPU cores", Width: 150 * scale}, {ID: "memory", Title: "Memory MiB", Width: 150 * scale}}
		ui.Table(c.Key("field.metricsList"), &w.metricsList, columns, len(entries), func(i, column int) {
			v := entries[i]
			switch column {
			case 0:
				ui.Text(c, v.Name).Label("Usage " + v.Name).SingleLine().Tooltip(v.Name)
			case 1:
				ui.Text(c, fmt.Sprintf("%.4g", v.CPUCores)).SingleLine()
			case 2:
				ui.Text(c, fmt.Sprintf("%.4g", v.MemoryBytes/(1<<20))).SingleLine()
			}
		}).Height(c.Theme().Space(8) + float32(min(len(entries), 4))*(c.Theme().FontSize*1.5+c.Theme().Space(3))).MinWidth(0)
	}
	details := ui.Column(c.Key("metrics.details"))
	open := ui.Local(details, "open", func() bool { return false })
	details.Children(func() {
		ui.Collapsible(c, "Sample details", open, func() {
			source := "metrics.k8s.io"
			if w.metricsResult.APIVersion != "" {
				source = w.metricsResult.APIVersion
			}
			if w.metricsResult.Window > 0 {
				source += "; window " + w.metricsResult.Window.String()
			}
			ui.Text(c, source)
			if !w.metricsResult.Timestamp.IsZero() {
				ui.Text(c, "Sample: "+w.metricsResult.Timestamp.UTC().Format(time.RFC3339))
				ui.Text(c, "Captured: "+w.metricsResult.ReceivedAt.UTC().Format(time.RFC3339))
			}
			ui.Text(c, "Local observations, up to 60 samples. Partial and stale samples leave gaps. Pause retains the snapshot.").TextColor(c.Theme().TextMuted)
			ui.Text(c, "CPU is measured in cores; memory is the working set in MiB. Metrics without a UID require a separate identity check.").TextColor(c.Theme().TextMuted)
		})
	})
}
func drawMetric(c *ui.Context, label string, points []resourcemetrics.Point, memory bool) {
	theme := c.Theme()
	value := func(p resourcemetrics.Point) float64 {
		if memory {
			return p.MemoryBytes / (1 << 20)
		}
		return p.CPUCores
	}
	maxValue, valid := float64(0), 0
	for _, p := range points {
		if p.Valid {
			maxValue = max(maxValue, value(p))
			valid++
		}
	}
	latest := "Unavailable"
	if len(points) > 0 && points[len(points)-1].Valid {
		latest = fmt.Sprintf("%.4g", value(points[len(points)-1]))
	}
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).Grow(1).Bold()
		ui.Text(c, latest).Label(label + " latest: " + latest).FontSize(theme.FontSize * 1.4).Bold()
	})
	if valid == 0 {
		ui.Text(c, "No samples").Label(label + " no samples").TextColor(theme.TextMuted)
		return
	}
	if len(points) > 1 {
		scale := maxValue
		if scale <= 0 {
			scale = 1
		}
		ui.Text(c, fmt.Sprintf("0–%.4g", scale)).Label(label + " plotted range").FontSize(12 * max(1, c.Preferences().TextScale)).TextColor(theme.TextMuted)
		ui.Box(c).Height(100).MinHeight(100).Label(label + " trend").Draw(func(p *ui.Painter, r ui.Rect) {
			p.Line(r.X, r.Y+r.H-2, r.X+r.W, r.Y+r.H-2, 0.5, theme.Border)
			if len(points) == 0 {
				return
			}
			span := points[len(points)-1].At.Sub(points[0].At).Seconds()
			if span <= 0 {
				span = 1
			}
			var px, py float32
			wasValid := false
			for _, point := range points {
				if !point.Valid {
					wasValid = false
					continue
				}
				x := r.X + 2 + float32(max(0, min(1, point.At.Sub(points[0].At).Seconds()/span)))*(r.W-4)
				y := r.Y + r.H - 3 - float32(value(point)/scale)*(r.H-6)
				if wasValid {
					var path ui.Path
					path.MoveTo(px, py).LineTo(x, y)
					p.StrokePath(&path, 1.5, theme.Accent)
				}
				p.Fill(ui.Rect{X: x - 1.5, Y: y - 1.5, W: 3, H: 3}, theme.Accent, 1)
				px, py, wasValid = x, y, true
			}
		})
	}
	ui.Row(c).Label(label + " time range").Children(func() {
		ui.Text(c, points[0].At.UTC().Format("15:04:05 UTC")).FontSize(12 * max(1, c.Preferences().TextScale)).TextColor(theme.TextMuted).Grow(1)
		if len(points) > 1 {
			ui.Text(c, points[len(points)-1].At.UTC().Format("15:04:05 UTC")).FontSize(12 * max(1, c.Preferences().TextScale)).TextColor(theme.TextMuted)
		}
	})
}
