package uiworkbench

import (
	"context"
	"io"
	"strings"
	"sync"

	"github.com/laojianzi/aster/internal/nativeterm"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// The real-OS smoke fixture feeds the same native emulator as a Pod terminal,
// but never reads kubeconfig, invokes a shell or opens a network connection.
func (w *Workbench) prepareTerminalSmoke() error {
	ctx, cancel := context.WithCancel(w.ctx)
	conn := &smokeTerminalConn{reader: strings.NewReader("\x1b[32mASTER NATIVE TERMINAL\x1b[0m\r\nUnicode: 中文\r\nNo cluster credentials or processes in this fixture.\r\n"), done: make(chan struct{})}
	term, err := nativeterm.New(conn)
	if err != nil {
		cancel()
		conn.Close()
		return err
	}
	w.terminal, w.terminalCancel, w.terminalActive = term, cancel, true
	w.terminalStatus = "Native renderer smoke fixture"
	w.detail = &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]interface{}{"name": "native-terminal-fixture", "namespace": "fixture", "uid": "fixture"}}}
	w.detailKind = catalog()[0]
	w.detailMode = "Terminal"
	w.containers = []string{"fixture"}
	w.terminalContainer = "fixture"
	w.activeContext = "fixture"
	w.run(func(context.Context) { <-ctx.Done(); term.Close(); term.IOFinished() })
	return nil
}

type smokeTerminalConn struct {
	reader *strings.Reader
	done   chan struct{}
	once   sync.Once
}

func (c *smokeTerminalConn) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	if n > 0 {
		return n, nil
	}
	if err != nil {
		<-c.done
		return 0, io.EOF
	}
	return n, err
}
func (c *smokeTerminalConn) Write(p []byte) (int, error) {
	select {
	case <-c.done:
		return 0, io.ErrClosedPipe
	default:
		return len(p), nil
	}
}
func (c *smokeTerminalConn) Close() error { c.once.Do(func() { close(c.done) }); return nil }
