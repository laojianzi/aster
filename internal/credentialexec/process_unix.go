//go:build linux || darwin

package credentialexec

import (
	"context"
	"os/exec"
	"syscall"
)

func startProcess(ctx context.Context, cmd *exec.Cmd) (func(), func(), error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	pid := cmd.Process.Pid
	return func() { _ = syscall.Kill(-pid, syscall.SIGKILL) }, func() {}, nil
}
