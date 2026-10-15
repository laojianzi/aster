//go:build e2e

package testcluster

import (
	"context"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmfixture"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NewHelmInspection uses synthetic Helm-shaped content in a real disposable API
// server. It deliberately does not claim a Helm install or execute release writes.
func NewHelmInspection(t *testing.T) HelmMetadataFixture {
	t.Helper()
	f := NewHelmMetadata(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	candidate := f.Secrets[0].DeepCopy()
	candidate.Data["release"] = helmfixture.Stored(helmfixture.Payload(f.Namespace, "example", "deployed", 1))
	updated, err := f.Client.CoreV1().Secrets(f.Namespace).Update(ctx, candidate, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.Secrets[0] = updated
	return f
}
