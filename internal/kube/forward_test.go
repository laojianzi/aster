package kube

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/resource"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	streamhttp "k8s.io/streaming/pkg/httpstream"
)

type roundTripFunc func(*http.Request) (*http.Response, error)
func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStreamingHandshakeCancellationReachesUnderlyingRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background()); defer cancel()
	entered := make(chan struct{}); done := make(chan error, 1)
	rt := sessionHandshake{parent: ctx, base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(entered); <-r.Context().Done(); return nil, r.Context().Err()
	})}
	req, err := http.NewRequest(http.MethodGet, "https://example.invalid/stream", nil)
	if err != nil { t.Fatal(err) }
	go func() { _, err := rt.RoundTrip(req); done <- err }()
	<-entered; cancel()
	select {
	case err := <-done: if !errors.Is(err, context.Canceled) { t.Fatal(err) }
	case <-time.After(time.Second): t.Fatal("cancel did not stop handshake")
	}
}
func TestStreamingNeverFallsBackOnDeniedCredentials(t *testing.T) {
	for _, cause := range []error{apierrors.NewForbidden(schema.GroupResource{Resource:"pods/portforward"},"pod",errors.New("denied")),apierrors.NewUnauthorized("denied"),context.Canceled,context.DeadlineExceeded} {
		wrapped := &streamhttp.UpgradeFailureError{Cause:cause}
		if canFallbackStreaming(wrapped) || streamingCause(wrapped) != cause { t.Fatalf("incorrect handling of %v",cause) }
	}
	if !canFallbackStreaming(&streamhttp.UpgradeFailureError{Cause:errors.New("server does not support WebSocket upgrade")}) { t.Fatal("negotiation fallback unavailable") }
}
func TestForwardPodRejectsInvalidTargetsBeforeNetwork(t *testing.T) {
	base := resource.Identity{SessionID:"one",GVR:schema.GroupVersionResource{Version:"v1",Resource:"pods"},Namespace:"team",Name:"pod",UID:"uid"}
	if err := validatePodTarget(base); err != nil { t.Fatal(err) }
	for _, modify := range []func(*resource.Identity){func(x *resource.Identity){x.UID=""},func(x *resource.Identity){x.Namespace=""},func(x *resource.Identity){x.GVR.Group="custom.example.com"},func(x *resource.Identity){x.GVR.Resource="services"}} {
		bad := base; modify(&bad)
		if err := validatePodTarget(bad); err == nil { t.Fatalf("invalid target accepted: %+v",bad) }
	}
	b := &Backend{}
	for _, port := range []int{0,-1,65536} { if err := b.ForwardPod(context.Background(),base,port,func(uint16){});err==nil{t.Fatal("invalid port accepted")} }
}
