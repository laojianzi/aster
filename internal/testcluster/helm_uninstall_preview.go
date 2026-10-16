//go:build e2e

package testcluster

import (
	"context"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmfixture"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NewHelmUninstallPreview creates synthetic Helm storage and a live canary
// resource in a disposable namespace. It does not invoke Helm or grant the
// production preview any permission. Admin setup/teardown are separate.
func NewHelmUninstallPreview(t *testing.T) HelmMetadataFixture {
	t.Helper()
	f := NewHelmMetadata(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	candidate := f.Secrets[0].DeepCopy()
	candidate.Data = helmfixture.WithManifest(f.Namespace, "example", "deployed", 1, helmfixture.PreviewManifest(f.Namespace)).Data
	updated, err := f.Client.CoreV1().Secrets(f.Namespace).Update(ctx, candidate, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.Secrets[0] = updated
	_, err = f.Client.CoreV1().ConfigMaps(f.Namespace).Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "preview-config"}, Data: map[string]string{"token": "LIVE_CANARY"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
