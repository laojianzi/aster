// Package rollout follows one immutable resource identity and generation.
// It never mutates, replays a write, rolls back, or follows a replacement object.
package rollout

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/laojianzi/aster/internal/health"
	"github.com/laojianzi/aster/internal/resource"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type Client interface {
	GetObject(context.Context, schema.GroupVersionResource, string, string) (*unstructured.Unstructured, error)
}

type State string

const (
	Watching    State = "Watching"
	Ready       State = "Ready"
	Completed   State = "Completed"
	Failed      State = "Failed"
	Stopped     State = "Stopped"
	TimedOut    State = "Timed out"
	Replaced    State = "Replaced"
	Superseded  State = "Superseded"
	Deleted     State = "Deleted"
	Denied      State = "Denied"
	Unavailable State = "Unavailable"
	Manual      State = "Manual intervention"
	Unsupported State = "Unsupported"
)

type Observation struct {
	State              State
	Message            string
	Health             health.Report
	Target             resource.Identity
	ExpectedGeneration int64
}

func (o Observation) String() string {
	return fmt.Sprintf("Tracking: %s\n%s\n\nExpected generation: %d\n\n%s", o.State, o.Message, o.ExpectedGeneration, o.Health.String())
}

type Options struct {
	// Zero values select 1s polling, 5m overall, and 10s per read.
	Interval, Timeout, ReadTimeout time.Duration
}

type Observer struct {
	client  Client
	options Options
}

func New(client Client, options Options) *Observer {
	if options.Interval <= 0 {
		options.Interval = time.Second
	}
	if options.Timeout <= 0 {
		options.Timeout = 5 * time.Minute
	}
	if options.ReadTimeout <= 0 {
		options.ReadTimeout = 10 * time.Second
	}
	return &Observer{client: client, options: options}
}

// Observe runs synchronously in an application worker. The callback must return
// promptly. Only changed observations are published. Closing a detail panel or
// switching a cluster cancels this observer without undoing an accepted write.
func (o *Observer) Observe(parent context.Context, target resource.Identity, expectedGeneration int64, update func(Observation)) (Observation, error) {
	result := Observation{State: Unavailable, Target: target, ExpectedGeneration: expectedGeneration}
	if err := target.Validate(); err != nil {
		return result, err
	}
	if o.client == nil || target.UID == "" || expectedGeneration < 0 || update == nil {
		return result, errors.New("rollout requires a client, resource UID, nonnegative generation and update callback")
	}
	ctx, cancel := context.WithTimeout(parent, o.options.Timeout)
	defer cancel()
	var last Observation
	sent := false
	publish := func() {
		if !sent || result != last {
			update(result)
			last, sent = result, true
		}
	}
	end := func(state State, message string, err error) (Observation, error) {
		result.State, result.Message = state, message
		publish()
		return result, err
	}
	contextEnd := func() (Observation, error) {
		if parent.Err() != nil {
			return end(Stopped, "Observation stopped; this does not cancel or undo a Kubernetes mutation.", parent.Err())
		}
		return end(TimedOut, "The observation deadline expired. This does not establish that the workload failed or undo the mutation.", context.DeadlineExceeded)
	}
	delay := o.options.Interval
	for {
		if ctx.Err() != nil {
			return contextEnd()
		}
		readCtx, readCancel := context.WithTimeout(ctx, o.options.ReadTimeout)
		obj, err := o.client.GetObject(readCtx, target.GVR, target.Namespace, target.Name)
		readCancel()
		if ctx.Err() != nil {
			return contextEnd()
		}
		if err != nil {
			switch {
			case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
				return end(Denied, "Current credentials cannot read this workload. No automatic credential or transport fallback was attempted.", err)
			case apierrors.IsNotFound(err):
				return end(Deleted, "The original resource no longer exists.", err)
			case !retryableRead(err):
				return end(Unavailable, "Workload observation is unavailable; inspect the API error.", err)
			}
			result.State, result.Message = Watching, "Read temporarily unavailable; the last status is stale. Retrying reads only."
			delay = min(max(delay*2, o.options.Interval), 15*time.Second)
			if seconds, ok := apierrors.SuggestsClientDelay(err); ok {
				delay = max(delay, time.Duration(seconds)*time.Second)
			}
			publish()
		} else {
			delay = o.options.Interval
			if obj == nil {
				return end(Unavailable, "API returned no object.", errors.New("nil workload object"))
			}
			if obj.GetUID() != target.UID {
				return end(Replaced, "An object with the same name has a different UID. Tracking stopped.", nil)
			}
			if expectedGeneration > 0 && obj.GetGeneration() > expectedGeneration {
				return end(Superseded, "A newer desired generation replaced the reviewed change. Tracking stopped.", nil)
			}
			result.Health = health.Assess(obj)
			if expectedGeneration > 0 && obj.GetGeneration() < expectedGeneration {
				result.State, result.Message = Watching, "Waiting to read the expected generation."
				publish()
			} else {
				switch result.Health.State {
				case health.Ready:
					return end(Ready, "The expected generation is ready at this observation.", nil)
				case health.Completed:
					return end(Completed, "The resource reached successful completion.", nil)
				case health.Failed:
					return end(Failed, result.Health.Message, nil)
				case health.Paused, health.Manual:
					return end(Manual, result.Health.Message, nil)
				case health.Terminating:
					return end(Superseded, "Deletion was requested for the tracked resource.", nil)
				case health.Unsupported:
					return end(Unsupported, result.Health.Message, nil)
				case health.Unknown:
					return end(Unavailable, result.Health.Message, nil)
				default:
					result.State, result.Message = Watching, "Waiting for workload readiness; API acceptance alone is not readiness."
					publish()
				}
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return contextEnd()
		case <-timer.C:
		}
	}
}

func retryableRead(err error) bool {
	var network net.Error
	return errors.Is(err, context.DeadlineExceeded) || apierrors.IsTooManyRequests(err) || apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsServiceUnavailable(err) || apierrors.IsInternalError(err) || errors.As(err, &network)
}
