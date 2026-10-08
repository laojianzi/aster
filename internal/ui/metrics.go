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
	w.stopLogs()
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
	ui.Row(c).Gap(6).Children(func() {
		if ui.Button(c, "Refresh metrics").Disabled(w.metricsLoading || w.metricsActive).Clicked() {
			w.loadMetrics(false)
		}
		if ui.Button(c, "Start sampling").Disabled(w.metricsActive).Clicked() {
			w.loadMetrics(true)
		}
		if ui.Button(c, "Pause metrics").Disabled(!w.metricsActive && !w.metricsLoading).Clicked() {
			w.stopMetrics()
		}
	})
	state := string(w.metricsResult.State)
	if state == "" {
		state = "No metrics requested"
	}
	if w.metricsLoading {
		state = "Reading metrics…"
	}
	if w.metricsActive {
		state += " · sampling"
	} else if !w.metricsLoading {
		state += " · snapshot"
	}
	ui.Text(c, state).Label("Metrics state").Bold().SingleLine()
	source := "metrics.k8s.io · CPU cores / memory working set"
	if w.metricsResult.APIVersion != "" {
		source = w.metricsResult.APIVersion + " · window " + w.metricsResult.Window.String()
	}
	ui.Text(c, source).FontSize(11).SingleLine()
	stamp := "No valid sample timestamp"
	if !w.metricsResult.Timestamp.IsZero() {
		stamp = "Sample UTC " + w.metricsResult.Timestamp.UTC().Format("15:04:05") + " · captured " + w.metricsResult.ReceivedAt.UTC().Format("15:04:05")
	}
	ui.Text(c, stamp).FontSize(11).SingleLine()
	entries := w.metricsResult.Entries
	ui.List(c, &w.metricsList, len(entries), func(i int) {
		v := entries[i]
		ui.Text(c, fmt.Sprintf("%s  ·  %.4g cores  ·  %.4g MiB", v.Name, v.CPUCores, v.MemoryBytes/(1<<20))).Label("Usage " + v.Name).FontSize(12).SingleLine()
	}).Height(78)
	if len(entries) > 0 {
		ui.Text(c, fmt.Sprintf("Reported total: %.4g cores · %.4g MiB", w.metricsResult.Total.CPUCores, w.metricsResult.Total.MemoryBytes/(1<<20))).FontSize(12).SingleLine()
	}
	points := w.metricsHistory.Points
	drawMetric(c, "CPU cores", points, false)
	drawMetric(c, "Memory MiB", points, true)
	note := "Local observations only · 60 points max · gaps are not zero"
	ui.Text(c, note).FontSize(10).SingleLine()
	ui.Text(c, "Partial/stale samples are excluded from trends; pause freezes this snapshot.").FontSize(10).SingleLine()
	ui.Text(c, "No UID in metrics? Live identity is rechecked; no atomic cross-API guarantee.").FontSize(10).SingleLine()
}
func drawMetric(c *ui.Context, label string, points []resourcemetrics.Point, memory bool) {
	theme := c.Theme()
	value := func(p resourcemetrics.Point) float64 {
		if memory {
			return p.MemoryBytes / (1 << 20)
		}
		return p.CPUCores
	}
	maxValue := float64(0)
	for _, p := range points {
		if p.Valid {
			maxValue = max(maxValue, value(p))
		}
	}
	ui.Text(c, fmt.Sprintf("%s · observed range 0–%.4g", label, maxValue)).FontSize(11).SingleLine()
	scale := maxValue
	if scale <= 0 {
		scale = 1
	}
	ui.Box(c).Height(48).Label(label + " trend").Draw(func(p *ui.Painter, r ui.Rect) {
		p.Line(r.X, r.Y+r.H-2, r.X+r.W, r.Y+r.H-2, 1, theme.TextMuted)
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
				p.Line(px, py, x, y, 1.5, theme.Accent)
			}
			p.Fill(ui.Rect{X: x - 1.5, Y: y - 1.5, W: 3, H: 3}, theme.Accent, 1)
			px, py, wasValid = x, y, true
		}
	})
}
