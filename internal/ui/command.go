package uiworkbench

import (
	"context"
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/execsession"
	"sync/atomic"
	"time"
)

func (w *Workbench) stopCommand() {
	w.commandEpoch++
	if w.commandCancel != nil {
		w.commandCancel()
		w.commandCancel = nil
	}
	if w.commandActive {
		w.commandStatus = "Disconnected; remote process termination is not confirmed"
	}
	w.commandActive = false
	w.commandConfirmation = ""
}
func (w *Workbench) startCommand() {
	if !w.bindDiagnostic() || w.backend == nil || w.commandActive {
		return
	}
	if w.commandConfirmation != w.diagnostic.target.Name {
		w.commandStatus = "Confirm the exact Pod name before execution"
		return
	}
	argv, err := execsession.ParseArgv(w.commandArgv)
	if err != nil {
		w.commandStatus = err.Error()
		return
	}
	cmd := execsession.Command{Container: w.commandContainer, Argv: argv, Timeout: execsession.DefaultTimeout}
	if err := cmd.Validate(); err != nil {
		w.commandStatus = err.Error()
		return
	}
	w.stopCommand()
	epoch, revision := w.diagnosticEpoch, w.commandEpoch
	backend, target, contextName := w.backend, w.diagnostic.target, w.diagnostic.context
	ctx, cancel := context.WithCancel(w.operationContext())
	w.commandCancel, w.commandActive, w.commandStatus, w.commandOutput = cancel, true, "Connecting; no automatic replay", ""
	output, err := execsession.NewOutput(execsession.DefaultOutputLimit, nil)
	if err != nil {
		cancel()
		w.commandActive = false
		w.commandStatus = err.Error()
		return
	}
	w.run(func(context.Context) {
		defer cancel()
		type completed struct {
			result execsession.Result
			err    error
		}
		done := make(chan completed, 1)
		go func() { r, e := backend.RunPodCommand(ctx, target, cmd, output); done <- completed{r, e} }()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		var pending atomic.Bool
		var last uint64
		for {
			select {
			case ended := <-done:
				snapshot := output.Snapshot()
				w.emit(func() {
					if epoch != w.diagnosticEpoch || revision != w.commandEpoch {
						return
					}
					w.commandActive, w.commandCancel = false, nil
					w.commandOutput = snapshot.Display()
					w.commandStatus = ended.result.String()
					if ended.err != nil && ended.result.State == execsession.Rejected {
						w.commandStatus += "; " + ended.err.Error()
					}
					// Never retain argv or output in operation history.
					w.recordHistory(fmt.Sprintf("[%s] command %s/%s (%s); %s", contextName, target.Namespace, target.Name, cmd.Container, ended.result.State))
				})
				return
			case <-ticker.C:
				if ctx.Err() != nil || !pending.CompareAndSwap(false, true) {
					continue
				}
				snapshot := output.Snapshot()
				if snapshot.Revision == last {
					pending.Store(false)
					continue
				}
				last = snapshot.Revision
				w.emit(func() {
					defer pending.Store(false)
					if epoch != w.diagnosticEpoch || revision != w.commandEpoch || ctx.Err() != nil {
						return
					}
					w.commandOutput = snapshot.Display()
					w.commandStatus = "Receiving output; remote exit pending"
				})
			}
		}
	})
}
func (w *Workbench) commandView(c *ui.Context) {
	if !w.bindDiagnostic() {
		return
	}
	if w.commandContainer == "" && len(w.diagnostic.containers) > 0 {
		w.commandContainer = w.diagnostic.containers[0]
	}
	ui.Row(c).Gap(8).Wrap().Children(func() {
		if guardedSelect(c, "field.commandContainer", &w.commandContainer, w.diagnostic.containers, !w.commandActive).Label("Command container").Disabled(w.commandActive).Width(180).Changed() && !w.commandActive {
			w.commandConfirmation = ""
		}
		if guardedTextArea(c, "field.commandArgv", &w.commandArgv, !w.commandActive).Label("Command argv JSON").Height(48).Grow(1).MinWidth(200).ReadOnly(w.commandActive).Changed() && !w.commandActive {
			w.commandConfirmation = ""
		}
	})
	ui.Row(c).Gap(8).Wrap().Children(func() {
		guardedTextInput(c, "field.commandConfirmation", &w.commandConfirmation, !w.commandActive).Label("Confirm Pod for command").Placeholder("Type the exact Pod name").Disabled(w.commandActive).Grow(1).MinWidth(180)
		ui.PrimaryButton(c, "Run command").Disabled(w.commandActive || w.diagnostic == nil || w.commandConfirmation != w.diagnostic.target.Name || w.commandContainer == "").OnClick(func() {
			w.startCommand()
		})
		ui.Button(c, "Stop command").Disabled(!w.commandActive).OnClick(func() {
			w.stopCommand()
		})
	})
	ui.Text(c, "Disconnecting may leave the remote process running").FontSize(12 * max(1, c.Preferences().TextScale)).Tooltip("No PTY, stdin or implicit shell. Two-minute deadline. Output limited to 128 KiB.")
	if w.commandStatus != "" {
		ui.Text(c, w.commandStatus).FontSize(12 * max(1, c.Preferences().TextScale))
	}
	ui.TextArea(c.Key("field.commandOutput"), &w.commandOutput).Label("Command output").ReadOnly(true).Grow(1).MinHeight(120)
}
