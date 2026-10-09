//go:build e2e

package e2e

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/credentialexec"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/testauth"
	"github.com/laojianzi/aster/internal/testcluster"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestCredentialFixture(t *testing.T) { testauth.Run() }
func TestRealExecCredentialTrustRBACAndExpiry(t *testing.T) {
	f := testcluster.NewHTTPPod(t)
	token := testcluster.ReadOnlyToken(t, f)
	marker := filepath.Join(t.TempDir(), "auth-started")
	path := testauth.ConfigFile(t, f.Config, f.Pod.Namespace, "credential", clientcmdapi.ExecEnvVar{Name: "ASTER_FIXTURE_TOKEN", Value: token}, clientcmdapi.ExecEnvVar{Name: "ASTER_FIXTURE_DURATION", Value: "4"}, clientcmdapi.ExecEnvVar{Name: "ASTER_FIXTURE_STARTED", Value: marker})
	_, err := kubeconfig.Load(kubeconfig.Options{Path: path})
	var trust *kubeconfig.TrustRequiredError
	if !errors.As(err, &trust) {
		t.Fatal("exec credential did not require trust")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("untrusted helper ran")
	}
	conn, err := kubeconfig.Load(kubeconfig.Options{Path: path, TrustToken: trust.Fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	// Counting sits inside the credential guard: an expired call must not reach
	// the network, not merely be rejected later by the API server.
	var calls atomic.Int32
	conn.Config.Wrap(func(base http.RoundTripper) http.RoundTripper { return authCountTransport{base, &calls} })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resolved, err := credentialexec.Resolve(ctx, conn.Config)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := kube.New(resolved.Config)
	if err != nil {
		t.Fatal(err)
	}
	if backend.Config().ExecProvider != nil {
		t.Fatal("backend retains an unbounded authenticator")
	}
	pods := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	if count, _, err := backend.List(ctx, pods, f.Pod.Namespace, metav1.ListOptions{}); err != nil || count != 1 {
		t.Fatal("resolved identity could not read fixture", err)
	}
	cms := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	_, err = backend.CreateObject(ctx, cms, f.Pod.Namespace, &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "must-not-exist", "namespace": f.Pod.Namespace}}}, metav1.CreateOptions{})
	if !apierrors.IsForbidden(err) {
		t.Fatal("reader unexpectedly has write access", err)
	}
	if _, err = f.Client.CoreV1().ConfigMaps(f.Pod.Namespace).Get(ctx, "must-not-exist", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatal("denied write created an object")
	}
	time.Sleep(time.Until(resolved.ExpiresAt) + 20*time.Millisecond)
	before := calls.Load()
	if _, _, err = backend.List(ctx, pods, f.Pod.Namespace, metav1.ListOptions{}); !errors.Is(err, credentialexec.ErrExpired) {
		t.Fatal("expired credentials reused", err)
	}
	if calls.Load() != before {
		t.Fatal("expired API operation reached network or refreshed itself")
	}
}

type authCountTransport struct {
	base  http.RoundTripper
	calls *atomic.Int32
}

func (t authCountTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.calls.Add(1)
	return t.base.RoundTrip(r)
}
func (t authCountTransport) WrappedRoundTripper() http.RoundTripper { return t.base }
