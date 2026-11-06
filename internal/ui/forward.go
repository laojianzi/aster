package uiworkbench

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

func (w *Workbench) stopForward() {
	w.forwardEpoch++
	if w.forwardCancel != nil {
		w.forwardCancel()
		w.forwardCancel = nil
	}
	w.forwardActive, w.forwardLocal, w.forwardAddress, w.forwardStatus = false, 0, "", "Stopped"
}
func (w *Workbench) startForward() {
	if !w.bindDiagnostic() || w.backend == nil || w.forwardActive {
		return
	}
	port, err := strconv.Atoi(strings.TrimSpace(w.forwardPort))
	if err != nil || port < 1 || port > 65535 {
		w.forwardStatus = "Remote port must be in [1,65535]"
		return
	}
	w.stopForward()
	epoch, revision := w.diagnosticEpoch, w.forwardEpoch
	backend, target := w.backend, w.diagnostic.target
	ctx, cancel := context.WithTimeout(w.operationContext(), 8*time.Hour)
	w.forwardCancel, w.forwardActive, w.forwardStatus = cancel, true, "Connecting"
	w.run(func(context.Context) {
		defer cancel()
		err := backend.ForwardPod(ctx, target, port, func(local uint16) {
			w.emit(func() {
				if epoch != w.diagnosticEpoch || revision != w.forwardEpoch || ctx.Err() != nil {
					return
				}
				w.forwardLocal = local
				w.forwardAddress = fmt.Sprintf("127.0.0.1:%d → %s/%s:%d", local, target.Namespace, target.Name, port)
				w.forwardStatus = "Active; local connections are not individually authenticated"
			})
		})
		w.emit(func() {
			if epoch != w.diagnosticEpoch || revision != w.forwardEpoch {
				return
			}
			w.forwardActive, w.forwardLocal, w.forwardAddress = false, 0, ""
			if err != nil {
				w.forwardStatus = "Closed: " + err.Error()
			} else {
				w.forwardStatus = "Closed"
			}
		})
	})
}
func (w *Workbench) forwardView(c *ui.Context) {
	if !w.bindDiagnostic() {
		return
	}
	ui.Text(c, "IPv4 loopback only; maximum 8 hours").FontSize(12 * max(1, c.Preferences().TextScale)).Tooltip("An available local port is assigned automatically. No automatic reconnect. Closing this diagnostic or disconnecting the cluster closes the listener.")
	if w.forwardPort == "" {
		w.forwardPort = "8080"
	}
	guardedTextInput(c, "field.forwardPort", &w.forwardPort, !w.forwardActive).Label("Remote port").Disabled(w.forwardActive).Width(130)
	ui.Row(c).Gap(8).Children(func() {
		ui.PrimaryButton(c, "Start port forward").Disabled(w.forwardActive).OnClick(func() {
			w.startForward()
		})
		ui.Button(c, "Stop port forward").Disabled(!w.forwardActive).OnClick(func() {
			w.stopForward()
		})
	})
	ui.Text(c, w.forwardStatus).FontSize(12 * max(1, c.Preferences().TextScale))
	if w.forwardAddress != "" {
		ui.TextArea(c.Key("field.forwardAddress"), &w.forwardAddress).Label("Forwarded address").ReadOnly(true).Height(80)
	}
}
