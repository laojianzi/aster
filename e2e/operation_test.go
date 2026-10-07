//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/operation"
	"github.com/laojianzi/aster/internal/resource"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

func TestOperationPreviewAndExecute(t *testing.T) {
	cfg, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
	if err != nil {
		t.Fatal(err)
	}
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	exec, err := operation.NewExecutor("e2e", client)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	target := resource.Identity{SessionID: "e2e", GVR: gvr, Namespace: "default", Name: "aster-operation-e2e"}
	payload, _ := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind": "ConfigMap",
		"metadata": map[string]any{"name": target.Name, "namespace": target.Namespace},
		"data": map[string]any{"mode": "validated"},
	})
	apply, err := operation.NewPlan(operation.Apply, target, operation.Preconditions{}, payload, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	preview, err := exec.Preview(ctx, apply)
	if err != nil {
		t.Fatal(err)
	}
	if preview.GetName() != target.Name {
		t.Fatalf("preview name = %q", preview.GetName())
	}
	ri := client.Resource(gvr).Namespace("default")
	if _, err := ri.Get(ctx, target.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("object persisted after preview: %v", err)
	}

	created, err := exec.Execute(ctx, apply)
	if err != nil {
		t.Fatal(err)
	}
	deleteTarget := target
	deleteTarget.UID = created.GetUID()
	del, err := operation.NewPlan(operation.Delete, deleteTarget, operation.Preconditions{
		UID: string(created.GetUID()), ResourceVersion: created.GetResourceVersion(),
	}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.Preview(ctx, del); err != nil {
		t.Fatal(err)
	}
	if _, err := ri.Get(ctx, target.Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("object missing after delete preview: %v", err)
	}
	if _, err := exec.Execute(ctx, del); err != nil {
		t.Fatal(err)
	}
	if _, err := ri.Get(ctx, target.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("object remains after delete execute: %v", err)
	}
}
