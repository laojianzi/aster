package operation

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestExecutorRejectsCrossSessionBeforeRequest(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	exec, err := NewExecutor("cluster-b", client)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewPlan(Apply, resource.Identity{
		SessionID: "cluster-a",
		GVR: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"},
		Namespace: "default",
		Name: "x",
	}, Preconditions{}, []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"x"}}`), time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	_, err = exec.Preview(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "cluster-a") {
		t.Fatalf("error = %v, want session mismatch", err)
	}
	if actions := client.Actions(); len(actions) != 0 {
		t.Fatalf("cross-session plan reached Kubernetes client: %v", actions)
	}
}

func TestExecutorRejectsEmptyApplyPayload(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	exec, _ := NewExecutor("cluster-a", client)
	plan, _ := NewPlan(Apply, resource.Identity{
		SessionID: "cluster-a",
		GVR: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"},
		Namespace: "default",
		Name: "x",
	}, Preconditions{}, nil, time.Unix(1, 0))
	if _, err := exec.Preview(context.Background(), plan); err == nil {
		t.Fatal("expected empty payload error")
	}
}
