package uiworkbench

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/credentialexec"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/testauth"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestCredentialFixture(t *testing.T) { testauth.Run() }
func authServer(t *testing.T) (*rest.Config, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	calls, active := &atomic.Int32{}, &atomic.Int32{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("incorrect credential identity")
			http.Error(w, "denied", 403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("watch") == "true" {
			active.Add(1)
			defer active.Add(-1)
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		if r.URL.Path == "/api/v1/namespaces/team/pods" {
			_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "1"}, "items": []any{modelObject("pod-uid", "private-pod")}})
			return
		}
		http.NotFound(w, r)
	}))
	// CloseClientConnections ensures a failed test cannot hang on its own stream.
	t.Cleanup(func() { server.CloseClientConnections(); server.Close() })
	return &rest.Config{Host: server.URL, TLSClientConfig: rest.TLSClientConfig{CAData: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})}}, calls, active
}
func TestNativeExecAuthenticationTrustExpiryAndNoRefresh(t *testing.T) {
	cfg, calls, active := authServer(t)
	started := filepath.Join(t.TempDir(), "started")
	h := newRelationshipHarness(t, 15*time.Second)
	h.w.path = testauth.ConfigFile(t, cfg, "team", "credential", clientcmdapi.ExecEnvVar{Name: "ASTER_FIXTURE_DURATION", Value: "4"}, clientcmdapi.ExecEnvVar{Name: "ASTER_FIXTURE_TOKEN", Value: "fixture-token"}, clientcmdapi.ExecEnvVar{Name: "ASTER_FIXTURE_STARTED", Value: started})
	h.w.currentContext = "credential-test"
	h.w.contexts = []string{"credential-test"}
	h.tt.Frame()
	h.click("Connect")
	h.pump(func() bool { return h.w.trustRequired && !h.w.connectionPending })
	if _, err := os.Stat(started); !errors.Is(err, os.ErrNotExist) || calls.Load() != 0 {
		t.Fatal("authentication ran before trust")
	}
	h.click("Trust this context and connect")
	h.pump(func() bool { return h.w.status == "Live" && len(h.w.rows) == 1 && active.Load() > 0 })
	oldConfig := h.w.backend.Config()
	if h.w.credentialExpiry.IsZero() || oldConfig.ExecProvider != nil {
		t.Fatal("connection has no bounded credentials")
	}
	saved := h.w.connectionCtx
	h.pump(func() bool { return h.w.status == "Credentials expired" && active.Load() == 0 })
	if h.w.backend != nil || h.w.ops != nil || len(h.w.rows) != 0 || h.w.detail != nil || h.w.plan != nil || saved.Err() == nil {
		t.Fatal("expiry retained privileged connection state")
	}
	client, err := rest.HTTPClientFor(oldConfig)
	if err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	_, err = client.Get(cfg.Host)
	if !errors.Is(err, credentialexec.ErrClosed) && !errors.Is(err, credentialexec.ErrExpired) {
		t.Fatal("old credential config still usable", err)
	}
	if calls.Load() != before {
		t.Fatal("expired credentials reached server")
	}
	if h.w.connectionPending {
		t.Fatal("expiry silently refreshed credentials")
	}
	saveNativeScreenshot(t, h.tt)
}
func TestNativeExecAuthenticationCancellationAndSingleAdmission(t *testing.T) {
	cfg, _, _ := authServer(t)
	started := filepath.Join(t.TempDir(), "started")
	h := newRelationshipHarness(t, 12*time.Second)
	h.w.path = testauth.ConfigFile(t, cfg, "team", "sleep", clientcmdapi.ExecEnvVar{Name: "ASTER_FIXTURE_STARTED", Value: started})
	h.w.currentContext = "credential-test"
	h.w.contexts = []string{"credential-test"}
	_, err := kubeconfig.Load(kubeconfig.Options{Path: h.w.path})
	var trust *kubeconfig.TrustRequiredError
	if !errors.As(err, &trust) {
		t.Fatal(err)
	}
	h.w.trustedFingerprint = trust.Fingerprint
	h.tt.Frame()
	h.click("Connect")
	h.pump(func() bool { _, err := os.Stat(started); return err == nil })
	epoch := h.w.contextEpoch
	h.w.connect()
	if h.w.contextEpoch != epoch {
		t.Fatal("second authentication admitted before first joined")
	}
	h.tt.Frame()
	h.click("Disconnect")
	h.pump(func() bool { return !h.w.connectionPending })
	if h.w.backend != nil || h.w.status != "Disconnected" || h.w.errText != "" {
		t.Fatal("canceled authentication published a late result")
	}
	// Old expiry can neither clear nor change a later workspace identity.
	h.w.status = "new-identity"
	h.w.expireConnection(epoch)
	if h.w.status != "new-identity" {
		t.Fatal("old expiry crossed connection epoch")
	}
}
func TestBackendRejectsUnboundedExecProvider(t *testing.T) {
	cfg := &rest.Config{Host: "https://unused.invalid", ExecProvider: &clientcmdapi.ExecConfig{Command: "never-run"}}
	if _, err := kube.New(cfg); err == nil {
		t.Fatal("unguarded exec configuration accepted")
	}
	if _, err := kube.New(&rest.Config{Host: "https://unused.invalid", AuthProvider: &clientcmdapi.AuthProviderConfig{Name: "legacy"}}); err == nil {
		t.Fatal("legacy provider accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := credentialexec.Resolve(ctx, cfg); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNativeCredentialExpiryStatusAtMinimumWindow(t *testing.T) {
	h := newRelationshipHarness(t, 3*time.Second)
	h.w.backend = &kube.Backend{}
	h.w.status = "Live"
	h.w.activeContext = "production-us-east"
	h.w.activeNamespace = "team"
	h.w.credentialExpiry = time.Now().Add(time.Minute)
	h.tt.SetSize(1100, 700)
	box, ok := h.tt.Find("Connection status")
	if !ok || box.H < 10 || box.X < 0 || box.X+box.W > 1100 || box.Y+box.H > 700 {
		t.Fatal("credential status clipped", box)
	}
	saveNativeScreenshot(t, h.tt)
}
