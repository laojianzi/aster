package rollout

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/resource"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type getterFunc func(context.Context) (*unstructured.Unstructured, error)

func (f getterFunc) GetObject(ctx context.Context, _ schema.GroupVersionResource, _, _ string) (*unstructured.Unstructured, error) {
	return f(ctx)
}
func target() resource.Identity {
	return resource.Identity{SessionID: "one", GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, Namespace: "team", Name: "web", UID: "original"}
}
func workload(generation, observed int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]interface{}{"name": "web", "namespace": "team", "uid": "original", "generation": generation},
		"spec":     map[string]interface{}{"replicas": int64(1)},
		"status":   map[string]interface{}{"observedGeneration": observed, "replicas": int64(1), "updatedReplicas": int64(1), "readyReplicas": int64(1), "availableReplicas": int64(1)},
	}}
}
func opts() Options {
	return Options{Interval: time.Millisecond, Timeout: time.Second, ReadTimeout: 100 * time.Millisecond}
}
func TestWaitsForTheControllerAndPublishesOnlyChangedObservations(t *testing.T) {
	calls := 0
	client := getterFunc(func(context.Context) (*unstructured.Unstructured, error) {
		calls++
		if calls < 3 {
			return workload(3, 2), nil
		}
		return workload(3, 3), nil
	})
	var updates []Observation
	result, err := New(client, opts()).Observe(context.Background(), target(), 3, func(o Observation) { updates = append(updates, o) })
	if err != nil || result.State != Ready || calls != 3 || len(updates) != 2 {
		t.Fatalf("result=%+v err=%v calls=%d updates=%d", result, err, calls, len(updates))
	}
	if updates[0].State != Watching || updates[1].State != Ready {
		t.Fatal(updates)
	}
}
func TestReplacementOrNewerGenerationCannotBeReportedAsReviewedSuccess(t *testing.T) {
	cases := []struct {
		name   string
		object *unstructured.Unstructured
		want   State
	}{
		{"new-generation", workload(4, 4), Superseded},
		{"new-uid", workload(3, 3), Replaced},
	}
	cases[1].object.SetUID("replacement")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := New(getterFunc(func(context.Context) (*unstructured.Unstructured, error) { return tc.object, nil }), opts()).Observe(context.Background(), target(), 3, func(Observation) {})
			if err != nil || result.State != tc.want {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
}
func TestReadDenialsAndDeletionStopWithoutRetries(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want State
	}{
		{apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "web", errors.New("denied")), Denied},
		{apierrors.NewUnauthorized("expired"), Denied},
		{apierrors.NewNotFound(schema.GroupResource{Group: "apps", Resource: "deployments"}, "web"), Deleted},
	} {
		calls := 0
		result, err := New(getterFunc(func(context.Context) (*unstructured.Unstructured, error) { calls++; return nil, tc.err }), opts()).Observe(context.Background(), target(), 3, func(Observation) {})
		if !errors.Is(err, tc.err) || calls != 1 || result.State != tc.want {
			t.Fatalf("%+v %v calls=%d", result, err, calls)
		}
	}
}
func TestCancelReachesInFlightReadAndDoesNotWaitForReadDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, done := make(chan struct{}), make(chan struct{})
	var result Observation
	var resultErr error
	client := getterFunc(func(ctx context.Context) (*unstructured.Unstructured, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	go func() {
		defer close(done)
		result, resultErr = New(client, Options{ReadTimeout: time.Minute}).Observe(ctx, target(), 3, func(Observation) {})
	}()
	<-entered
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("in-flight GET survived cancellation")
	}
	if !errors.Is(resultErr, context.Canceled) || result.State != Stopped {
		t.Fatalf("%+v %v", result, resultErr)
	}
}
func TestTransientReadRecoversButTimeoutIsNotFailureOrSuccess(t *testing.T) {
	calls := 0
	result, err := New(getterFunc(func(context.Context) (*unstructured.Unstructured, error) {
		calls++
		if calls == 1 {
			return nil, apierrors.NewServiceUnavailable("restart")
		}
		return workload(3, 3), nil
	}), opts()).Observe(context.Background(), target(), 3, func(Observation) {})
	if err != nil || result.State != Ready || calls != 2 {
		t.Fatalf("%+v %v calls=%d", result, err, calls)
	}
	result, err = New(getterFunc(func(context.Context) (*unstructured.Unstructured, error) { return workload(3, 2), nil }), Options{Interval: time.Millisecond, Timeout: 20 * time.Millisecond}).Observe(context.Background(), target(), 3, func(Observation) {})
	if !errors.Is(err, context.DeadlineExceeded) || result.State != TimedOut {
		t.Fatalf("%+v %v", result, err)
	}
}
func TestRetryAfterCanBeCanceledWithoutAnotherRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	result, err := New(getterFunc(func(context.Context) (*unstructured.Unstructured, error) {
		calls.Add(1)
		return nil, apierrors.NewTooManyRequests("slow down", 60)
	}), opts()).Observe(ctx, target(), 3, func(Observation) { cancel() })
	if !errors.Is(err, context.Canceled) || result.State != Stopped || calls.Load() != 1 {
		t.Fatalf("%+v %v calls=%d", result, err, calls.Load())
	}
}
func TestInvalidObservationNeverStartsNetwork(t *testing.T) {
	calls := 0
	client := getterFunc(func(context.Context) (*unstructured.Unstructured, error) { calls++; return workload(3, 3), nil })
	invalid := target()
	invalid.UID = ""
	if _, err := New(client, opts()).Observe(context.Background(), invalid, 3, func(Observation) {}); err == nil || calls != 0 {
		t.Fatal("invalid target was accepted")
	}
	if _, err := New(client, opts()).Observe(context.Background(), target(), 3, nil); err == nil || calls != 0 {
		t.Fatal("nil callback was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := New(client, opts()).Observe(ctx, target(), 3, func(Observation) {})
	if !errors.Is(err, context.Canceled) || result.State != Stopped || calls != 0 {
		t.Fatal("canceled request started network work")
	}
}
