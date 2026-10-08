package kube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/laojianzi/aster/internal/resource"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	streamhttp "k8s.io/streaming/pkg/httpstream"
)

// ForwardPod opens one ephemeral IPv4 loopback listener. It never retries an
// established session, binds no public interface, and returns after teardown.
// Kubernetes addresses this subresource by name: the preflight UID check is not
// an atomic server-side UID precondition on the upgraded connection.
func (b *Backend) ForwardPod(ctx context.Context, target resource.Identity, remote int, ready func(uint16)) error {
	if err := validatePodTarget(target); err != nil { return err }
	if remote < 1 || remote > 65535 || ready == nil {
		return errors.New("port forward requires a remote port in [1,65535] and a readiness callback")
	}
	if err := ctx.Err(); err != nil { return err }
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	checkCtx, checkCancel := context.WithTimeout(ctx, 15*time.Second)
	pod, err := b.typed.CoreV1().Pods(target.Namespace).Get(checkCtx, target.Name, metav1.GetOptions{})
	checkCancel()
	if err != nil { return err }
	if pod.UID != target.UID { return errors.New("pod was replaced; refresh before starting a port forward") }
	if pod.Status.Phase != corev1.PodRunning { return fmt.Errorf("pod is %s; port forwarding requires a running pod", pod.Status.Phase) }
	cfg := rest.CopyConfig(b.config)
	// Upstream streaming dialers create requests with Background context.
	cfg.Wrap(func(base http.RoundTripper) http.RoundTripper { return sessionHandshake{parent: ctx, base: base} })
	url := b.typed.CoreV1().RESTClient().Post().Resource("pods").Namespace(target.Namespace).Name(target.Name).SubResource("portforward").URL()
	ws, err := portforward.NewSPDYOverWebsocketDialerForStreaming(url, cfg)
	if err != nil { return err }
	rt, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil { return err }
	legacy := spdy.NewDialerForStreaming(upgrader, &http.Client{Transport: rt}, http.MethodPost, url)
	dialer := &recordingDialer{base: portforward.NewFallbackDialerForStreaming(ws, legacy, canFallbackStreaming)}
	started := make(chan struct{})
	forwarder, err := portforward.NewOnAddressesForStreaming(dialer, []string{"127.0.0.1"}, []string{fmt.Sprintf("0:%d", remote)}, ctx.Done(), started, io.Discard, io.Discard)
	if err != nil { return err }
	done := make(chan error, 1)
	go func() { done <- forwarder.ForwardPorts() }()
	select {
	case err := <-done:
		if ctx.Err() != nil { return ctx.Err() }
		if err == nil { return errors.New("port forward ended before readiness") }
		return dialer.result(err)
	case <-ctx.Done():
		<-done
		return ctx.Err()
	case <-started:
		ports, err := forwarder.GetPorts()
		if err != nil || len(ports) != 1 {
			cancel(); <-done
			return fmt.Errorf("cannot resolve local forwarded port: %v", err)
		}
		if err := ctx.Err(); err != nil { <-done; return err }
		ready(ports[0].Local)
	}
	err = <-done
	if ctx.Err() != nil { return ctx.Err() }
	return dialer.result(err)
}

func validatePodTarget(target resource.Identity) error {
	if err := target.Validate(); err != nil { return err }
	if target.Namespace == "" || target.Namespace == "*" || target.UID == "" || target.GVR.Group != "" || target.GVR.Version != "v1" || target.GVR.Resource != "pods" {
		return errors.New("streaming requires a namespaced core/v1 Pod with a known UID")
	}
	return nil
}

// Authentication and authorization failures are not negotiation failures.
func streamingCause(err error) error {
	var upgrade *streamhttp.UpgradeFailureError
	if errors.As(err, &upgrade) { return upgrade.Cause }
	return err
}
func canFallbackStreaming(err error) bool {
	cause := streamingCause(err)
	if apierrors.IsForbidden(cause) || apierrors.IsUnauthorized(cause) || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) { return false }
	return streamhttp.IsUpgradeFailure(err) || streamhttp.IsHTTPSProxyError(err)
}

type sessionHandshake struct { parent context.Context; base http.RoundTripper }
func (t sessionHandshake) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.parent.Err(); err != nil { return nil, err }
	ctx, cancel := context.WithTimeout(req.Context(), 15*time.Second)
	stop := context.AfterFunc(t.parent, cancel)
	defer stop(); defer cancel()
	return t.base.RoundTrip(req.Clone(ctx))
}

// Upstream PortForwarder formats negotiation errors with %s, erasing types.
// This record is only read after ForwardPorts exits (synchronized by done).
type recordingDialer struct { base streamhttp.Dialer; failure error }
func (d *recordingDialer) Dial(protocols ...string) (streamhttp.Connection, string, error) {
	conn, protocol, err := d.base.Dial(protocols...)
	d.failure = streamingCause(err)
	return conn, protocol, err
}
func (d *recordingDialer) result(err error) error { if d.failure != nil { return d.failure }; return err }
