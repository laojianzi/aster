package rollout

import (
	"context"
	"errors"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestTerminalHealthStatesAndMalformedResponses(t *testing.T) {
	for _, state := range []State{Failed, Manual, Superseded, Unsupported, Unavailable, Completed} {
		t.Run(string(state), func(t *testing.T) {
			value := workload(3, 3)
			switch state {
			case Failed:
				value.Object["status"].(map[string]interface{})["conditions"] = []interface{}{map[string]interface{}{"type": "Progressing", "status": "False", "reason": "ProgressDeadlineExceeded"}}
			case Manual:
				value.Object["spec"].(map[string]interface{})["paused"] = true
			case Superseded:
				value.Object["metadata"].(map[string]interface{})["deletionTimestamp"] = "2026-01-01T00:00:00Z"
			case Unsupported:
				value.SetKind("CustomWorkload")
			case Unavailable:
				value.Object["status"] = true
			case Completed:
				value.SetAPIVersion("batch/v1")
				value.SetKind("Job")
				value.Object["status"] = map[string]interface{}{"conditions": []interface{}{map[string]interface{}{"type": "Complete", "status": "True"}}}
			}
			result, err := New(getterFunc(func(context.Context) (*unstructured.Unstructured, error) { return value, nil }), opts()).Observe(context.Background(), target(), 3, func(Observation) {})
			if err != nil || result.State != state {
				t.Fatalf("%+v %v", result, err)
			}
			if !strings.Contains(result.String(), "Tracking: "+string(state)) {
				t.Fatal(result.String())
			}
		})
	}
	for _, tc := range []struct {
		object *unstructured.Unstructured
		err    error
	}{{nil, nil}, {nil, errors.New("invalid protocol response")}} {
		result, err := New(getterFunc(func(context.Context) (*unstructured.Unstructured, error) { return tc.object, tc.err }), opts()).Observe(context.Background(), target(), 3, func(Observation) {})
		if err == nil || result.State != Unavailable {
			t.Fatalf("%+v %v", result, err)
		}
	}
}

func TestOlderGenerationCannotSatisfyExpectedChange(t *testing.T) {
	calls := 0
	var updates []Observation
	result, err := New(getterFunc(func(context.Context) (*unstructured.Unstructured, error) {
		calls++
		if calls == 1 {
			return workload(2, 2), nil
		}
		return workload(3, 3), nil
	}), opts()).Observe(context.Background(), target(), 3, func(o Observation) { updates = append(updates, o) })
	if err != nil || result.State != Ready || calls != 2 || len(updates) != 2 || updates[0].State != Watching {
		t.Fatalf("%+v %v calls=%d updates=%v", result, err, calls, updates)
	}
	invalid := target()
	invalid.Name = ""
	if _, err := New(nil, opts()).Observe(context.Background(), invalid, 3, func(Observation) {}); err == nil {
		t.Fatal("invalid identity accepted")
	}
}
