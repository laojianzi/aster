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
		w.terminalStatus = "Disconnected; remote process termination is not confirmed"
	}
	w.terminalActive = false
	w.terminalConfirmation = ""
}
func (w *Workbench) startTerminal() {
	if !w.bindDiagnostic() || w.backend == nil || w.terminalActive || w.terminalJoining {
		return
	}
	if w.terminalConfirmation != w.diagnostic.target.Name {
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
	epoch, revision := w.diagnosticEpoch, w.terminalEpoch
	backend, target, container, contextName := w.backend, w.diagnostic.target, w.terminalContainer, w.diagnostic.context
	ctx, cancel := context.WithTimeout(w.operationContext(), ttysession.DefaultLease)
	w.terminalJoining = true
	w.terminalCancel, w.terminalActive, w.terminalStatus = cancel, true, "Connecting; no reconnect or replay"
	w.run(func(context.Context) {
		defer cancel()
		defer w.emit(func() { w.terminalJoining = false })
		session, err := backend.OpenPodTerminal(ctx, target, container, argv, ttysession.DefaultLease)
		if err != nil {
			w.emit(func() {
				if epoch == w.diagnosticEpoch && revision == w.terminalEpoch {
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
				if epoch == w.diagnosticEpoch && revision == w.terminalEpoch {
					w.terminalActive = false
					w.terminalStatus = err.Error()
				}
			})
			return
		}
		w.emit(func() {
			if ctx.Err() == nil && epoch == w.diagnosticEpoch && revision == w.terminalEpoch {
				w.terminal = term
				w.terminalStatus = "Terminal open"
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
			if epoch != w.diagnosticEpoch || revision != w.terminalEpoch {
				return
			}
			w.terminalActive = false
			w.terminalStatus = ended.Result.String()
			if err := term.InputError(); err != nil {
				w.terminalStatus = err.Error() + "; " + ended.Result.String()
			}
			w.recordHistory(fmt.Sprintf("[%s] terminal %s/%s (%s); %s", contextName, target.Namespace, target.Name, container, ended.Result.State))
		})
		// Retain the finished screen until explicit closure, switching diagnostic tools,
		// or expiry. No terminal bytes/argv are written to application history/disk.
		<-ctx.Done()
	})
}
func (w *Workbench) terminalView(c *ui.Context) {
	if !w.bindDiagnostic() {
		return
	}
	if w.terminalContainer == "" && len(w.diagnostic.containers) > 0 {
		w.terminalContainer = w.diagnostic.containers[0]
	}
	ui.Row(c).Gap(8).Wrap().Hidden(w.terminalActive).Children(func() {
		if guardedSelect(c, "field.terminalContainer", &w.terminalContainer, w.diagnostic.containers, !w.terminalActive).Label("Terminal container").Disabled(w.terminalActive).Width(180).Changed() && !w.terminalActive {
			w.terminalConfirmation = ""
		}
		if guardedTextArea(c, "field.terminalArgv", &w.terminalArgv, !w.terminalActive).Label("Terminal argv JSON").Height(48).Grow(1).MinWidth(200).ReadOnly(w.terminalActive).Changed() && !w.terminalActive {
			w.terminalConfirmation = ""
		}
	})
	ui.Row(c).Gap(8).Wrap().Children(func() {
		guardedTextInput(c, "field.terminalConfirmation", &w.terminalConfirmation, !w.terminalActive).Label("Confirm Pod for terminal").Hidden(w.terminalActive).Placeholder("Type the exact Pod name").Disabled(w.terminalActive).Grow(1).MinWidth(180)
		ui.Text(c, w.terminalContainer).Label("Terminal session").Tooltip(w.terminalArgv).Hidden(!w.terminalActive).Grow(1).MinWidth(0).SingleLine()
		ui.PrimaryButton(c, "Open terminal").Hidden(w.terminalActive).Disabled(w.terminalActive || w.terminalJoining || w.diagnostic == nil || w.terminalConfirmation != w.diagnostic.target.Name || w.terminalContainer == "").OnClick(func() {
			w.startTerminal()
		})
		ui.Button(c, "Close terminal").Disabled(w.terminal == nil && !w.terminalActive).OnClick(func() {
			w.stopTerminal()
		})
	})
	ui.Text(c, "Disconnecting may leave the remote process running").FontSize(12 * max(1, c.Preferences().TextScale)).Tooltip("Interactive TTY. Maximum session: 15 minutes. Switching diagnostic tools or closing the resource disconnects.")
	if w.terminalStatus != "" {
		ui.Text(c, w.terminalStatus).FontSize(12 * max(1, c.Preferences().TextScale))
	}
	if w.terminal != nil {
		nativeterm.View(c, w.terminal).Grow(1).MinHeight(120)
	} else {
		ui.Text(c, "No terminal session").Label("Terminal output").Tooltip("Clipboard writes and link opening are blocked").FontSize(12 * max(1, c.Preferences().TextScale)).Grow(1).MinHeight(120)
	}
}
