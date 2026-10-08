//go:build e2e

package e2e

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/testcluster"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

func TestWatchResourceObservesLifecycle(t *testing.T) {
	cfg := testcluster.Config(t)
	backend, err := kube.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	typed, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	const name = "aster-watch-e2e"

	var mu sync.Mutex
	seen := map[watch.EventType]bool{}
	ready := make(chan struct{})
	var readyOnce sync.Once
	done := make(chan error, 1)
	go func() {
		done <- backend.WatchWithStatus(ctx, schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, "default",
			metav1.ListOptions{FieldSelector: "metadata.name=" + name},
			func(event kube.ResourceEvent) error {
				mu.Lock()
				seen[event.Type] = true
				mu.Unlock()
				return nil
			}, func(status string) {
				if status == "Live" {
					readyOnce.Do(func() { close(ready) })
				}
			})
	}()

	// Wait for the real LIST-to-WATCH handshake; elapsed time is not readiness.
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("watch exited before becoming live: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	cm, err := typed.CoreV1().ConfigMaps("default").Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Data:       map[string]string{"version": "1"},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cm.Data["version"] = "2"
	if _, err := typed.CoreV1().ConfigMaps("default").Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := typed.CoreV1().ConfigMaps("default").Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		mu.Lock()
		ok := seen[watch.Added] && seen[watch.Modified] && seen[watch.Deleted]
		mu.Unlock()
		if ok {
			cancel()
			<-done
			return
		}
		select {
		case err := <-done:
			if err != nil && ctx.Err() == nil {
				t.Fatal(err)
			}
			t.Fatal("watch ended before lifecycle was observed")
		case <-deadline.C:
			mu.Lock()
			defer mu.Unlock()
			t.Fatalf("timed out; events=%v", seen)
		case <-time.After(25 * time.Millisecond):
		}
	}
}
