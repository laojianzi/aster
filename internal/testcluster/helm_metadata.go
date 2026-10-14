//go:build e2e

package testcluster

import (
	"context"
	"strconv"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type HelmMetadataFixture struct {
	ManifestDeliveryFixture
	Secrets []*corev1.Secret
}

// NewHelmMetadata uses only the explicitly opted-in disposable test cluster.
// Payloads are deliberately NOT Helm releases: the read path must never decode
// them or claim the editable labels authenticate release state.
func NewHelmMetadata(t *testing.T, count int) HelmMetadataFixture {
	t.Helper()
	f := HelmMetadataFixture{ManifestDeliveryFixture: NewManifestDelivery(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := 1; i <= count; i++ {
		version := strconv.Itoa(i)
		secret, err := f.Client.CoreV1().Secrets(f.Namespace).Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1.example.v" + version, Namespace: f.Namespace, Labels: map[string]string{"owner": "helm", "name": "example", "version": version, "status": "deployed"}},
			Type:       "helm.sh/release.v1", Data: map[string][]byte{"release": []byte("PRIVATE_NOT_A_HELM_PAYLOAD")},
		}, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		f.Secrets = append(f.Secrets, secret)
	}
	return f
}
