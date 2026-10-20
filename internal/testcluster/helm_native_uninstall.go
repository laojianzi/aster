//go:build e2e

package testcluster

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmfixture"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HelmNativeUninstallFixture is deliberately limited to Aster's reviewed
// ConfigMap-only profile. Admin setup and verification use Client, independently
// of the application's connection and transport. It does not install Helm or
// stand in for the existing pinned-Helm characterization tests.
type HelmNativeUninstallFixture struct {
	HelmMetadataFixture
	Storage *corev1.Secret
	Targets []*corev1.ConfigMap
	Canary  *corev1.ConfigMap
}

// NewHelmNativeUninstall first reaches the existing explicit disposable-cluster
// guard through NewHelmMetadata. No default or in-cluster credential fallback is
// admitted. Only this unique fixture namespace is populated or later removed.
func NewHelmNativeUninstall(t *testing.T) HelmNativeUninstallFixture {
	t.Helper()
	f := HelmNativeUninstallFixture{HelmMetadataFixture: NewHelmMetadata(t, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var manifest strings.Builder
	for i := range 2 {
		name := fmt.Sprintf("native-uninstall-target-%d", i+1)
		fmt.Fprintf(&manifest, "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: %s\ndata:\n  private: PRIVATE_STORED\n", name)
		cm, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"app.kubernetes.io/managed-by": "Helm"},
				Annotations: map[string]string{"meta.helm.sh/release-name": "example", "meta.helm.sh/release-namespace": f.Namespace}},
			Data: map[string]string{"private": "PRIVATE_LIVE"},
		}, metav1.CreateOptions{})
		if err != nil {
			t.Fatal("native uninstall fixture target", err)
		}
		f.Targets = append(f.Targets, cm)
	}
	payload, err := json.Marshal(map[string]any{
		"name": "example", "namespace": f.Namespace, "version": 1,
		"info":     map[string]any{"status": "deployed", "description": "fixture installation"},
		"chart":    map[string]any{"metadata": map[string]any{"name": "example-chart", "version": "1.2.3", "apiVersion": "v2"}},
		"manifest": manifest.String(), "config": map[string]any{"private": "PRIVATE_VALUE"},
	})
	if err != nil {
		t.Fatal("native uninstall fixture payload", err)
	}
	s := f.Secrets[0].DeepCopy()
	s.Data = map[string][]byte{"release": helmfixture.Stored(payload)}
	s.Annotations = map[string]string{"aster.test/retained": "unrelated-metadata"}
	clear(payload)
	f.Storage, err = f.Client.CoreV1().Secrets(f.Namespace).Update(ctx, s, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal("native uninstall fixture release", err)
	}
	f.Secrets[0] = f.Storage
	f.Canary, err = f.Client.CoreV1().ConfigMaps(f.Namespace).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "native-uninstall-unrelated-canary"}, Data: map[string]string{"guard": "unchanged"},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal("native uninstall fixture canary", err)
	}
	return f
}
