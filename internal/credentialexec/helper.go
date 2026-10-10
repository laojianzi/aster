package credentialexec

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"time"
)

// RunHelper runs trusted application code, not a user-selected authentication
// plugin. It shares the platform process-tree containment used by Resolve.
// Arguments/environment must contain no credentials; sensitive input goes only
// through stdin. Termination bounds waiting, not remote or OS-store side effects.
func RunHelper(parent context.Context, executable string, args, env []string, input []byte, timeout time.Duration) ([]byte, error) {
	if !filepath.IsAbs(executable) || len(input) > 4096 || timeout <= 0 || timeout > 30*time.Second {
		return nil, errors.New("invalid application helper request")
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	out := &boundedOutput{limit: 4096, retain: true, cancel: cancel}
	stderr := &boundedOutput{limit: 1024, cancel: cancel}
	cmd := exec.Command(executable, args...)
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout = out
	cmd.Stderr = stderr
	cmd.WaitDelay = 500 * time.Millisecond
	kill, release, err := startProcess(ctx, cmd)
	if err != nil {
		return nil, ErrCommandFailed
	}
	defer release()
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { kill(); close(stopped) })
	err = cmd.Wait()
	if !stop() {
		<-stopped
	}
	kill()
	if out.exceeded() || stderr.exceeded() {
		return nil, ErrOutputLimit
	}
	if parent.Err() != nil {
		return nil, parent.Err()
	}
	if ctx.Err() != nil {
		return nil, ErrTimeout
	}
	if err != nil {
		return nil, ErrCommandFailed
	}
	return out.data, nil
}
