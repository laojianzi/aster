//go:build e2e

package testcluster

import (
	"context"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/resource"
	"github.com/laojianzi/aster/internal/resourcemetrics"
	"testing"
	"time"
)

// AwaitMetrics uses the application's real reader, not fixtures pretending to
// be a metrics service. Missing prerequisites fail E2E rather than skip it.
func AwaitMetrics(t *testing.T, b *kube.Backend, target resource.Identity) resourcemetrics.Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var got resourcemetrics.Snapshot
	for {
		got = resourcemetrics.Read(ctx, b, target)
		if got.State == resourcemetrics.Ready {
			if got.Total.MemoryBytes <= 0 || got.Total.CPUCores < 0 || got.Window <= 0 || got.Timestamp.IsZero() {
				t.Fatalf("invalid real usage: %+v", got)
			}
			return got
		}
		if got.State != resourcemetrics.NoSample && got.State != resourcemetrics.NotInstalled && got.State != resourcemetrics.Unavailable {
			t.Fatalf("unexpected metrics state: %+v", got)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("real metrics-server never provided metrics: %+v", got)
		case <-time.After(2 * time.Second):
		}
	}
}
