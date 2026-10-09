package credentialexec

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/testauth"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const version = "client.authentication.k8s.io/v1"
const payload = `{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","status":{"token":"test-opaque-token"}}`

func TestCredentialFixture(t *testing.T) { testauth.Run() }
func configuration(t *testing.T, mode string, extra ...clientcmdapi.ExecEnvVar) *rest.Config {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := []clientcmdapi.ExecEnvVar{{Name: "ASTER_CREDENTIAL_FIXTURE", Value: mode}, {Name: "ASTER_FIXTURE_PAYLOAD", Value: payload}}
	env = append(env, extra...)
	return &rest.Config{Host: "https://example.invalid:6443", ExecProvider: &clientcmdapi.ExecConfig{Command: exe, Args: []string{"-test.run=^TestCredentialFixture$"}, APIVersion: version, InteractiveMode: clientcmdapi.NeverExecInteractiveMode, Env: env}}
}
func TestExecCredentialResolveIsolatedEnvironmentAndLiteralArguments(t *testing.T) {
	t.Setenv("ASTER_PARENT_SECRET", "parent-credential-must-not-leak")
	cfg := configuration(t, "inspect", clientcmdapi.ExecEnvVar{Name: "ASTER_EXPLICIT_SETTING", Value: "chosen"})
	cfg.ExecProvider.ProvideClusterInfo = true
	cfg.ExecProvider.Args = append(cfg.ExecProvider.Args, "--", "; touch NOT-A-SHELL")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, err := Resolve(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.ExecProvider != nil || result.Config.BearerToken != "test-opaque-token" || result.ExpiresAt.IsZero() || time.Until(result.ExpiresAt) > MaxConnectionLifetime {
		t.Fatal("invalid resolved snapshot")
	}
	if cfg.ExecProvider == nil || cfg.BearerToken != "" {
		t.Fatal("source config mutated")
	}
}
func TestExecCredentialTransportExpiresAndNeverReruns(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-opaque-token" {
			t.Error("missing resolved identity")
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	cfg := configuration(t, "credential")
	cfg.Host = server.URL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, err := resolve(ctx, cfg, 5*time.Second, 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	client, err := rest.HTTPClientFor(result.Config)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	time.Sleep(time.Until(result.ExpiresAt) + 10*time.Millisecond)
	if _, err = client.Get(server.URL); !errors.Is(err, ErrExpired) {
		t.Fatal("expired credentials were not rejected", err)
	}
	cancel()
	if _, err = client.Get(server.URL); !errors.Is(err, ErrClosed) {
		t.Fatal("closed credentials were not rejected", err)
	}
	if calls.Load() != 1 {
		t.Fatal("expired connection reached API server")
	}
}
func TestExecCredentialInvalidResponsesAreRejectedWithoutSecrets(t *testing.T) {
	now := time.Now()
	valid := map[string]any{"apiVersion": version, "kind": "ExecCredential", "status": map[string]any{"token": "sensitive-token"}}
	data := func(status map[string]any) string {
		obj := map[string]any{"apiVersion": version, "kind": "ExecCredential", "status": status}
		b, _ := json.Marshal(obj)
		return string(b)
	}
	good, _ := json.Marshal(valid)
	cases := map[string]string{
		"empty": `{}`, "unknown": strings.Replace(payload, `"status":`, `"extra":"sensitive-token","status":`, 1),
		"version":      strings.Replace(payload, version, "client.authentication.k8s.io/v1beta1", 1),
		"missing-kind": strings.Replace(payload, `"kind":"ExecCredential",`, "", 1),
		"second-json":  string(good) + string(good), "duplicate": strings.Replace(payload, `"token":`, `"token":"first","token":`, 1),
		"empty-status": data(map[string]any{}), "expired": data(map[string]any{"token": "sensitive-token", "expirationTimestamp": now.Add(-time.Minute).UTC().Format(time.RFC3339)}),
		"newline": data(map[string]any{"token": "sensitive\ntoken"}), "oversized-token": data(map[string]any{"token": strings.Repeat("s", (16<<10)+1)}),
		"cert-missing-key": data(map[string]any{"clientCertificateData": "sensitive-token"}),
		"token-and-cert":   data(map[string]any{"token": "sensitive-token", "clientCertificateData": "x", "clientKeyData": "y"}),
		"invalid-pair":     data(map[string]any{"clientCertificateData": "sensitive-token", "clientKeyData": "secret"}),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := decode([]byte(raw), version, now, time.Minute)
			if !errors.Is(err, ErrInvalidCredential) || strings.Contains(err.Error(), "sensitive-token") {
				t.Fatal("invalid credential not safely rejected", err)
			}
		})
	}
}
func TestExecCredentialCertificateAndLifetimeValidation(t *testing.T) {
	now := time.Now()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	makePair := func(start, end time.Time) (string, string) {
		t.Helper()
		template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture"}, NotBefore: start, NotAfter: end, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
		der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		secret, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: secret}))
	}
	cert, secret := makePair(now.Add(-time.Minute), now.Add(2*time.Minute))
	raw, _ := json.Marshal(map[string]any{"apiVersion": version, "kind": "ExecCredential", "status": map[string]any{"clientCertificateData": cert, "clientKeyData": secret}})
	st, expires, err := decode(raw, version, now, 15*time.Minute)
	if err != nil || st.ClientKeyData != secret || expires.After(now.Add(2*time.Minute)) {
		t.Fatal("valid certificate failed or exceeded its lifetime", err)
	}
	cfg := configuration(t, "credential")
	cfg.ExecProvider.Env[1].Value = string(raw)
	resolved, err := Resolve(context.Background(), cfg)
	if err != nil || len(resolved.Config.CertData) == 0 || len(resolved.Config.KeyData) == 0 || resolved.Config.ExecProvider != nil {
		t.Fatal("certificate snapshot not installed", err)
	}
	// Verify that the resolved certificate actually authenticates a mutual-TLS
	// connection, not only that its PEM happened to parse.
	block, _ := pem.Decode([]byte(cert))
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	var authenticated atomic.Bool
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil && len(r.TLS.VerifiedChains) > 0 && r.TLS.PeerCertificates[0].Subject.CommonName == "fixture" {
			authenticated.Store(true)
		}
		w.WriteHeader(200)
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}
	server.StartTLS()
	defer server.Close()
	resolved.Config.Host = server.URL
	resolved.Config.CAData = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	client, err := rest.HTTPClientFor(resolved.Config)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal("mTLS authentication failed", err)
	}
	response.Body.Close()
	if !authenticated.Load() {
		t.Fatal("resolved certificate did not authenticate")
	}
	for _, future := range []bool{true, false} {
		start, end := now.Add(-2*time.Minute), now.Add(-time.Minute)
		if future {
			start, end = now.Add(time.Minute), now.Add(2*time.Minute)
		}
		cert, secret = makePair(start, end)
		raw, _ = json.Marshal(map[string]any{"apiVersion": version, "kind": "ExecCredential", "status": map[string]any{"clientCertificateData": cert, "clientKeyData": secret}})
		if _, _, err = decode(raw, version, now, time.Minute); !errors.Is(err, ErrInvalidCredential) {
			t.Fatal("invalid validity window accepted")
		}
	}
	beta := strings.Replace(payload, version, "client.authentication.k8s.io/v1beta1", 1)
	if _, _, err := decode([]byte(beta), "client.authentication.k8s.io/v1beta1", now, time.Minute); err != nil {
		t.Fatal(err)
	}
}
func TestExecCredentialPoliciesPreventProcessLaunch(t *testing.T) {
	for _, name := range []string{"always-interactive", "missing-mode", "policy-deny", "policy-list", "mixed-auth", "reserved-environment", "invalid-version"} {
		t.Run(name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "started")
			cfg := configuration(t, "credential", clientcmdapi.ExecEnvVar{Name: "ASTER_FIXTURE_STARTED", Value: marker})
			switch name {
			case "always-interactive":
				cfg.ExecProvider.InteractiveMode = clientcmdapi.AlwaysExecInteractiveMode
			case "missing-mode":
				cfg.ExecProvider.InteractiveMode = ""
			case "policy-deny":
				cfg.ExecProvider.PluginPolicy.PolicyType = clientcmdapi.PluginPolicyDenyAll
			case "policy-list":
				cfg.ExecProvider.PluginPolicy.PolicyType = clientcmdapi.PluginPolicyAllowlist
			case "mixed-auth":
				cfg.BearerToken = "other"
			case "reserved-environment":
				cfg.ExecProvider.Env = append(cfg.ExecProvider.Env, clientcmdapi.ExecEnvVar{Name: "KUBERNETES_EXEC_INFO", Value: "override"})
			case "invalid-version":
				cfg.ExecProvider.APIVersion = "v0"
			}
			if _, err := Resolve(context.Background(), cfg); err == nil {
				t.Fatal("unsafe policy accepted")
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("untrusted process executed")
			}
		})
	}
}
func TestExecCredentialOutputBudgetsDeadlineAndCancellation(t *testing.T) {
	for _, mode := range []string{"stdout", "stderr", "failure", "sleep", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			kind := mode
			if mode == "cancel" {
				kind = "sleep"
			}
			cfg := configuration(t, kind)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := ErrOutputLimit
			timeout := 5 * time.Second
			if mode == "failure" {
				want = ErrCommandFailed
			}
			if mode == "sleep" {
				timeout = 150 * time.Millisecond
				want = ErrTimeout
			}
			if mode == "cancel" {
				time.AfterFunc(150*time.Millisecond, cancel)
				want = context.Canceled
			}
			start := time.Now()
			_, err := resolve(ctx, cfg, timeout, time.Minute)
			if !errors.Is(err, want) {
				t.Fatalf("%s: %v, expected %v", mode, err, want)
			}
			if time.Since(start) > 6*time.Second {
				t.Fatal("process did not join")
			}
			if strings.Contains(err.Error(), "DO-NOT-EXPOSE") {
				t.Fatal("credential output leaked")
			}
		})
	}
}
func TestExecCredentialProcessTreeIsReclaimed(t *testing.T) {
	for _, mode := range []string{"tree", "tree-wait"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "marker")
			spawned := filepath.Join(dir, "spawned")
			cfg := configuration(t, mode, clientcmdapi.ExecEnvVar{Name: "ASTER_FIXTURE_MARKER", Value: marker}, clientcmdapi.ExecEnvVar{Name: "ASTER_FIXTURE_SPAWNED", Value: spawned})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := Resolve(ctx, cfg); done <- err }()
			limit := time.After(8 * time.Second)
			for {
				if _, err := os.Stat(spawned); err == nil {
					break
				}
				select {
				case <-limit:
					t.Fatal("child not spawned")
				case <-time.After(10 * time.Millisecond):
				}
			}
			if mode == "tree-wait" {
				cancel()
			}
			select {
			case err := <-done:
				if mode == "tree" && err != nil {
					t.Fatal(err)
				}
				if mode == "tree-wait" && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("authentication process did not join")
			}
			time.Sleep(2 * time.Second)
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("descendant survived authentication completion")
			}
		})
	}
}
func TestExecCredentialEnvironmentAndArgumentValidation(t *testing.T) {
	for _, env := range [][]clientcmdapi.ExecEnvVar{
		{{Name: "X", Value: "a"}, {Name: "X", Value: "b"}}, {{Name: "1BAD", Value: "x"}}, {{Name: "OK", Value: "\x00"}}, {{Name: "LD_PRELOAD", Value: "file"}}, {{Name: "NODE_OPTIONS", Value: "file"}}, {{Name: "OK", Value: strings.Repeat("s", 25<<10)}},
	} {
		if _, err := environment(env, "{}"); err == nil {
			t.Fatal("invalid env accepted")
		}
	}
	for _, cmd := range []string{"", "\x00", "./does-not-exist"} {
		if _, err := run(context.Background(), cmd, nil, nil, time.Second); err == nil {
			t.Fatal("invalid command accepted")
		}
	}
	exe, _ := os.Executable()
	if _, err := run(context.Background(), exe, []string{strings.Repeat("s", 17<<10)}, nil, time.Second); err == nil {
		t.Fatal("oversized args accepted")
	}
	w := &boundedOutput{limit: 3, retain: true, cancel: func() {}}
	_, _ = w.Write([]byte("abc"))
	if _, err := w.Write([]byte("d")); !errors.Is(err, ErrOutputLimit) || len(w.data) != 3 {
		t.Fatal("buffer unbounded")
	}
	if _, err := Resolve(nil, &rest.Config{}); err == nil {
		t.Fatal("nil context")
	}
	if _, err := Resolve(context.Background(), nil); err == nil {
		t.Fatal("nil config")
	}
	cfg := &rest.Config{Host: "https://example.invalid"}
	r, err := Resolve(context.Background(), cfg)
	if err != nil || r.Config != cfg || !r.ExpiresAt.IsZero() {
		t.Fatal("non-exec regression")
	}
}
