package kube

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/laojianzi/aster/internal/execsession"
	"github.com/laojianzi/aster/internal/resource"
	"github.com/laojianzi/aster/internal/ttysession"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	protocol "k8s.io/apimachinery/pkg/util/remotecommand"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// OpenPodTerminal preflights the selected Pod UID/container, then opens exactly
// one WebSocket v5 TTY session. Kubernetes exec is name-addressed: this check
// cannot atomically pin UID or prevent container replacement during upgrade.
// There is no fallback, replay, implicit shell or promise that Close kills the
// process. The caller must keep consuming output or Close to release backpressure.
func (b *Backend) OpenPodTerminal(parent context.Context, target resource.Identity, container string, argv []string, lease time.Duration) (*ttysession.Session, error) {
	if err := validatePodTarget(target); err != nil {
		return nil, err
	}
	if err := (execsession.Command{Container: container, Argv: argv}).Validate(); err != nil {
		return nil, err
	}
	if lease < 0 || lease > ttysession.MaxLease {
		return nil, errors.New("invalid terminal lease")
	}
	argv = append([]string(nil), argv...)
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	pod, err := b.typed.CoreV1().Pods(target.Namespace).Get(ctx, target.Name, metav1.GetOptions{})
	cancel()
	if err != nil {
		return nil, errors.New("cannot read the selected Pod before terminal connection")
	}
	if pod.UID != target.UID || pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning {
		return nil, errors.New("Pod changed or is not running; refresh before connecting")
	}
	running := false
	for _, statuses := range [][]corev1.ContainerStatus{pod.Status.ContainerStatuses, pod.Status.InitContainerStatuses, pod.Status.EphemeralContainerStatuses} {
		for _, s := range statuses {
			if s.Name == container && s.State.Running != nil {
				running = true
			}
		}
	}
	if !running {
		return nil, errors.New("selected container is not running")
	}
	cfg := copyConnectionConfig(b.config)
	cfg.Timeout = 0
	url := b.typed.CoreV1().RESTClient().Post().Resource("pods").Namespace(target.Namespace).Name(target.Name).SubResource("exec").VersionedParams(&corev1.PodExecOptions{Container: container, Command: argv, Stdin: true, Stdout: true, TTY: true}, scheme.ParameterCodec).URL()
	return ttysession.New(parent, lease, func(ctx context.Context, in io.Reader, out io.Writer, next func() *ttysession.Size) (execsession.Result, error) {
		var upgraded atomic.Bool
		cfg.Wrap(func(base http.RoundTripper) http.RoundTripper {
			return execHandshake{sessionHandshake{parent: ctx, base: base}, &upgraded}
		})
		executor, err := remotecommand.NewWebSocketExecutorForProtocols(cfg, http.MethodGet, url.String(), protocol.StreamProtocolV5Name)
		if err != nil {
			return execsession.Result{State: execsession.Rejected, ExitCode: -1}, errors.New("terminal transport initialization failed")
		}
		err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdin: in, Stdout: out, Tty: true, TerminalSizeQueue: ttySizes{next}})
		return commandResult(ctx, false, upgraded.Load(), err)
	})
}

type ttySizes struct{ next func() *ttysession.Size }

func (q ttySizes) Next() *remotecommand.TerminalSize {
	s := q.next()
	if s == nil {
		return nil
	}
	return &remotecommand.TerminalSize{Width: s.Cols, Height: s.Rows}
}
