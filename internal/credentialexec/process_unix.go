//go:build linux || darwin

package credentialexec

import (
	"context"
	"os/exec"
	"syscall"
)

func startProcess(ctx context.Context, cmd *exec.Cmd) (func(), func(), error) {
	return startProcessWithDiagnostics(ctx, cmd, nil)
}

func startProcessWithDiagnostics(ctx context.Context, cmd *exec.Cmd, trace *helperTrace) (func(), func(), error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	started := trace.begin()
	err := cmd.Start()
	trace.recordError(helperStart, started, err)
	if err != nil {
		return nil, nil, err
	}
	pid := cmd.Process.Pid
	return func() {
		started := trace.begin()
		err := syscall.Kill(-pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			trace.record(helperReclaim, started, helperPhaseAlreadyGone)
		} else {
			trace.recordError(helperReclaim, started, err)
		}
	}, func() {}, nil
}
