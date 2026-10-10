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
	if w.detail == nil || w.backend == nil || w.creating || w.detailKind.GVR.Group != "" || w.detailKind.GVR.Resource != "pods" || w.forwardActive {
		return
	}
	port, err := strconv.Atoi(strings.TrimSpace(w.forwardPort))
	if err != nil || port < 1 || port > 65535 {
		w.forwardStatus = "Remote port must be in [1,65535]"
		return
	}
	w.stopForward()
	epoch, revision := w.detailEpoch, w.forwardEpoch
	backend, target := w.backend, w.target()
	ctx, cancel := context.WithTimeout(w.operationContext(), 8*time.Hour)
	w.forwardCancel, w.forwardActive, w.forwardStatus = cancel, true, "Connecting"
	w.run(func(context.Context) {
		defer cancel()
		err := backend.ForwardPod(ctx, target, port, func(local uint16) {
			w.emit(func() {
				if epoch != w.detailEpoch || revision != w.forwardEpoch || ctx.Err() != nil {
					return
				}
				w.forwardLocal = local
				w.forwardAddress = fmt.Sprintf("127.0.0.1:%d → %s/%s:%d", local, target.Namespace, target.Name, port)
				w.forwardStatus = "Active · local connections are not individually authenticated"
			})
		})
		w.emit(func() {
			if epoch != w.detailEpoch || revision != w.forwardEpoch {
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
	ui.Text(c, "Pod port forward").Bold()
	ui.Text(c, "IPv4 loopback only · ephemeral local port · no automatic reconnect").FontSize(11)
	ui.Text(c, "Closing this resource or disconnecting the cluster closes the listener. Maximum session: 8 hours.").FontSize(11)
	if w.forwardPort == "" {
		w.forwardPort = "8080"
	}
	ui.TextInput(c.Key("field.forwardPort"), &w.forwardPort).Label("Remote port").Disabled(w.forwardActive).Width(130)
	ui.Row(c).Gap(8).Children(func() {
		ui.PrimaryButton(c, "Start port forward").Disabled(w.forwardActive).OnClick(func() {
			w.startForward()
		})
		ui.Button(c, "Stop port forward").Disabled(!w.forwardActive).OnClick(func() {
			w.stopForward()
		})
	})
	ui.Text(c, w.forwardStatus).FontSize(12)
	ui.TextArea(c.Key("field.forwardAddress"), &w.forwardAddress).Label("Forwarded address").ReadOnly(true).Height(80)
}
