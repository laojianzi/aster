package credentialexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// These are operating-system location/locale variables, not a blanket copy of
// the desktop environment. Cloud-specific credentials/settings must be explicit
// in the trusted kubeconfig or obtained by the trusted program from its files.
var inherited = []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "PATH", "SystemRoot", "WINDIR", "TEMP", "TMP", "TMPDIR", "LANG", "LC_ALL", "LC_CTYPE"}

func environment(extra []clientcmdapi.ExecEnvVar, info string) ([]string, error) {
	if len(extra) > 128 {
		return nil, errors.New("too many authentication environment entries")
	}
	values := map[string]string{}
	names := map[string]string{}
	normalized := func(s string) string {
		if runtime.GOOS == "windows" {
			return strings.ToUpper(s)
		}
		return s
	}
	set := func(name, value string) { key := normalized(name); values[key] = value; names[key] = name }
	for _, name := range inherited {
		if v, ok := os.LookupEnv(name); ok {
			set(name, v)
		}
	}
	seen := map[string]bool{}
	for _, item := range extra {
		k := normalized(item.Name)
		u := strings.ToUpper(item.Name)
		if !validName(item.Name) || seen[k] || strings.ContainsRune(item.Value, 0) {
			return nil, errors.New("invalid or duplicate authentication environment entry")
		}
		seen[k] = true
		if u == "KUBERNETES_EXEC_INFO" || strings.HasPrefix(u, "LD_") || strings.HasPrefix(u, "DYLD_") || u == "GODEBUG" || u == "GOTRACEBACK" || u == "PYTHONPATH" || u == "PYTHONHOME" || u == "NODE_OPTIONS" || u == "RUBYOPT" || u == "PERL5OPT" || u == "JAVA_TOOL_OPTIONS" || u == "_JAVA_OPTIONS" {
			return nil, errors.New("unsafe authentication environment override")
		}
		set(item.Name, item.Value)
	}
	set("KUBERNETES_EXEC_INFO", info)
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	size := 0
	for _, k := range keys {
		v := names[k] + "=" + values[k]
		size += len(v) + 1
		if size > 24<<10 {
			return nil, errors.New("authentication environment exceeds its budget")
		}
		out = append(out, v)
	}
	return out, nil
}
func validName(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range []byte(s) {
		if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

type boundedOutput struct {
	mu           sync.Mutex
	data         []byte
	count, limit int
	overflow     bool
	retain       bool
	cancel       context.CancelFunc
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(p) > w.limit-w.count {
		w.overflow = true
		w.cancel()
		return 0, ErrOutputLimit
	}
	w.count += len(p)
	if w.retain {
		w.data = append(w.data, p...)
	}
	return len(p), nil
}
func (w *boundedOutput) exceeded() bool { w.mu.Lock(); defer w.mu.Unlock(); return w.overflow }

func run(parent context.Context, command string, args, env []string, timeout time.Duration) ([]byte, error) {
	if len(args) > 128 || command == "" || strings.ContainsRune(command, 0) {
		return nil, errors.New("invalid authentication command")
	}
	size := len(command)
	for _, s := range args {
		size += len(s)
		if strings.ContainsRune(s, 0) {
			return nil, errors.New("invalid authentication argument")
		}
	}
	if size > 16<<10 {
		return nil, errors.New("authentication arguments exceed their budget")
	}
	// A relative PATH result (including Go's ErrDot case) is not trusted merely
	// because the current working directory happens to contain a program.
	path, err := exec.LookPath(command)
	if err != nil || !filepath.IsAbs(path) {
		return nil, errors.New("authentication executable must resolve to an absolute installed path")
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return nil, errors.New("authentication executable is not a regular file")
	}
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(path), ".exe") {
		return nil, errors.New("Windows authentication requires an explicit executable, not a command script")
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := &boundedOutput{limit: MaxStdoutBytes, retain: true, cancel: cancel}
	stderr := &boundedOutput{limit: MaxStderrBytes, cancel: cancel}
	cmd := exec.Command(path, args...)
	cmd.Env = env
	cmd.Stdout = out
	cmd.Stderr = stderr
	cmd.WaitDelay = 500 * time.Millisecond
	if home, e := os.UserHomeDir(); e == nil {
		cmd.Dir = home
	}
	kill, release, err := startProcess(ctx, cmd)
	if err != nil {
		if parent.Err() != nil {
			return nil, parent.Err()
		}
		if ctx.Err() != nil {
			return nil, ErrTimeout
		}
		return nil, ErrCommandFailed
	}
	defer release()
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { kill(); close(stopped) })
	waitErr := cmd.Wait()
	if !stop() {
		<-stopped
	}
	kill() // Reclaim descendants even when the direct child returned successfully.
	if out.exceeded() || stderr.exceeded() {
		return nil, ErrOutputLimit
	}
	if parent.Err() != nil {
		return nil, parent.Err()
	}
	if ctx.Err() != nil {
		return nil, ErrTimeout
	}
	if waitErr != nil {
		return nil, ErrCommandFailed
	}
	return out.data, nil
}
