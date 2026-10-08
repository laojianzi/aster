//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/testcluster"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestBackendAgainstRealAPIServer(t *testing.T) {
	cfg := testcluster.Config(t)
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
