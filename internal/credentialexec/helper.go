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
	return runHelper(parent, executable, args, env, "", input, timeout, 4096, 1024)
}

// RunRenderHelper runs only the caller-verified Helm distribution. It reuses
// process-tree containment, with fixed rendering-specific pipe/time bounds.
func RunRenderHelper(parent context.Context, executable string, args, env []string, dir string, input []byte) ([]byte, error) {
	return runRenderHelper(parent, executable, args, env, dir, input, nil)
}

// RunRenderHelperWithDiagnostics preserves RunRenderHelper's results and fixed
// limits, while separately returning bounded, private stage observations. It
// never retains the request, output, or operating-system error text in them.
func RunRenderHelperWithDiagnostics(parent context.Context, executable string, args, env []string, dir string, input []byte) ([]byte, HelperDiagnostics, error) {
	trace := newHelperTrace()
	out, err := runRenderHelper(parent, executable, args, env, dir, input, trace)
	return out, trace.snapshot(), err
}

func runRenderHelper(parent context.Context, executable string, args, env []string, dir string, input []byte, trace *helperTrace) ([]byte, error) {
	if parent == nil || !filepath.IsAbs(executable) || !filepath.IsAbs(dir) || len(input) > 256<<10 {
		trace.finish(parent, nil, helperInvalidRequest)
		return nil, errors.New("invalid render helper request")
	}
	return runHelperWithTrace(parent, executable, args, env, dir, input, 10*time.Second, 2<<20, 16<<10, trace)
}

func runHelper(parent context.Context, executable string, args, env []string, dir string, input []byte, timeout time.Duration, stdoutLimit, stderrLimit int) ([]byte, error) {
	return runHelperWithTrace(parent, executable, args, env, dir, input, timeout, stdoutLimit, stderrLimit, nil)
}

func runHelperWithTrace(parent context.Context, executable string, args, env []string, dir string, input []byte, timeout time.Duration, stdoutLimit, stderrLimit int, trace *helperTrace) (data []byte, resultErr error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	// Run after deferred platform release but before our own cancel, so a
	// successful call cannot acquire a synthetic "canceled" terminal context.
	defer func() { trace.finishResult(parent, ctx, resultErr) }()
	out := &boundedOutput{limit: stdoutLimit, retain: true, cancel: cancel}
	stderr := &boundedOutput{limit: stderrLimit, cancel: cancel}
	cmd := exec.Command(executable, args...)
	cmd.Env = env
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout = out
	cmd.Stderr = stderr
	cmd.WaitDelay = 500 * time.Millisecond
	kill, release, err := startProcessWithDiagnostics(ctx, cmd, trace)
	if err != nil {
		return nil, ErrCommandFailed
	}
	defer release()
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { kill(); close(stopped) })
	waitStarted := trace.begin()
	err = cmd.Wait()
	trace.recordError(helperWait, waitStarted, err)
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
