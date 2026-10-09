package uiworkbench

import (
	"context"
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/execsession"
	"github.com/laojianzi/aster/internal/nativeterm"
	"github.com/laojianzi/aster/internal/ttysession"
)

func (w *Workbench) stopTerminal() {
	w.terminalEpoch++
	if w.terminalCancel != nil {
		w.terminalCancel()
		w.terminalCancel = nil
	}
	if w.terminal != nil {
		_ = w.terminal.Close()
		w.terminal = nil
	}
	if w.terminalActive {
		w.terminalStatus = "Disconnected · remote process termination is not confirmed"
	}
	w.terminalActive = false
	w.terminalConfirmation = ""
}
func (w *Workbench) startTerminal() {
	if w.detail == nil || w.backend == nil || w.creating || w.terminalActive || w.terminalJoining || w.detailKind.GVR.Group != "" || w.detailKind.GVR.Resource != "pods" {
		return
	}
	if w.terminalConfirmation != w.detail.GetName() {
		w.terminalStatus = "Confirm the exact Pod name before connecting"
		return
	}
	argv, err := execsession.ParseArgv(w.terminalArgv)
	if err != nil {
		w.terminalStatus = err.Error()
		return
	}
	if err := (execsession.Command{Container: w.terminalContainer, Argv: argv}).Validate(); err != nil {
		w.terminalStatus = err.Error()
		return
	}
	// Verify the packaged native library BEFORE sending any exec request.
	if err := nativeterm.Load(); err != nil {
		w.terminalStatus = err.Error()
		return
	}
	w.stopTerminal()
	epoch, revision := w.detailEpoch, w.terminalEpoch
	backend, target, container, contextName := w.backend, w.target(), w.terminalContainer, w.activeContext
	ctx, cancel := context.WithTimeout(w.operationContext(), ttysession.DefaultLease)
	w.terminalJoining = true
	w.terminalCancel, w.terminalActive, w.terminalStatus = cancel, true, "Connecting · no reconnect or replay"
	w.run(func(context.Context) {
		defer cancel()
		defer w.emit(func() { w.terminalJoining = false })
		session, err := backend.OpenPodTerminal(ctx, target, container, argv, ttysession.DefaultLease)
		if err != nil {
			w.emit(func() {
				if epoch == w.detailEpoch && revision == w.terminalEpoch {
					w.terminalActive = false
					w.terminalCancel = nil
					w.terminalStatus = err.Error()
				}
			})
			return
		}
		defer session.Close()

		term, err := nativeterm.New(session)
		if err != nil {
			session.Close()
			<-session.Done()
			w.emit(func() {
				if epoch == w.detailEpoch && revision == w.terminalEpoch {
					w.terminalActive = false
					w.terminalStatus = err.Error()
				}
			})
			return
		}
		w.emit(func() {
			if ctx.Err() == nil && epoch == w.detailEpoch && revision == w.terminalEpoch {
				w.terminal = term
				w.terminalStatus = "Terminal open · remote output and status pending"
			}
		})

		// The UI owns pointer publication; background cleanup only touches the
		// terminal's synchronized API. A dropped/queued UI callback cannot prevent
		// socket and native-resource cleanup on window/application cancellation.
		stop := context.AfterFunc(ctx, func() { _ = term.Close() })
		defer stop()
		defer term.Close()
		<-session.Done()
		ended := session.Outcome()
		term.IOFinished()
		w.emit(func() {
			if epoch != w.detailEpoch || revision != w.terminalEpoch {
				return
			}
			w.terminalActive = false
			w.terminalStatus = ended.Result.String()
			if err := term.InputError(); err != nil {
				w.terminalStatus = err.Error() + " · " + ended.Result.String()
			}
			w.recordHistory(fmt.Sprintf("[%s] terminal %s/%s (%s) · %s", contextName, target.Namespace, target.Name, container, ended.Result.State))
		})
		// Retain the finished screen until explicit closure, switching detail/tab,
		// or expiry. No terminal bytes/argv are written to application history/disk.
		<-ctx.Done()
	})
}
func (w *Workbench) terminalView(c *ui.Context) {
	ui.Text(c, "Interactive remote TTY · fifteen-minute maximum session").FontSize(11)
	ui.Text(c, "Tab/detail close disconnects; remote termination is not guaranteed").FontSize(11)
	if w.terminalContainer == "" && len(w.containers) > 0 {
		w.terminalContainer = w.containers[0]
	}
	if ui.Select(c, &w.terminalContainer, w.containers).Label("Terminal container").Disabled(w.terminalActive).Changed() {
		w.terminalConfirmation = ""
	}
	if ui.TextArea(c, &w.terminalArgv).Label("Terminal argv JSON").Height(48).ReadOnly(w.terminalActive).Changed() {
		w.terminalConfirmation = ""
	}
	ui.TextInput(c, &w.terminalConfirmation).Label("Confirm Pod for terminal").Placeholder("Type the exact Pod name").Disabled(w.terminalActive)
	ui.Row(c).Gap(8).Children(func() {
		if ui.PrimaryButton(c, "Open terminal").Disabled(w.terminalActive || w.terminalJoining || w.detail == nil || w.terminalConfirmation != w.detail.GetName() || w.terminalContainer == "").Clicked() {
			w.startTerminal()
		}
		if ui.Button(c, "Close terminal").Disabled(w.terminal == nil && !w.terminalActive).Clicked() {
			w.stopTerminal()
		}
	})
	ui.Text(c, w.terminalStatus).FontSize(11).SingleLine()
	if w.terminal != nil {
		nativeterm.View(c, w.terminal).Grow(1)
	} else {
		ui.Text(c, "No terminal session · clipboard writes and link opening blocked").FontSize(11).Grow(1)
	}
}
