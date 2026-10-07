//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func TestClusterDiscoverySmoke(t *testing.T) {
	cfg, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Timeout = 10 * time.Second
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	v, err := client.Discovery().ServerVersion()
	if err != nil {
		t.Fatal(err)
	}
	if v.GitVersion == "" {
		t.Fatal("empty server version")
	}
	if _, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1}); err != nil {
		t.Fatal(err)
	}
}
