//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
)

func TestBackendAgainstRealAPIServer(t *testing.T) {
	cfg, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
	if err != nil {
		t.Fatal(err)
	}
	b, err := kube.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	version, err := b.ServerVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version == "" {
		t.Fatal("empty server version")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n, rv, err := b.List(ctx, schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}, "", metav1.ListOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 || rv == "" {
		t.Fatalf("unexpected list result: count=%d resourceVersion=%q", n, rv)
	}
}
