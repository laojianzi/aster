package kube

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestCallerCannotMutateEffectiveImpersonationThroughConfigAliases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Impersonate-Group"); got != "viewers" {
			t.Errorf("effective group changed to %q", got)
		}
		if got := r.Header.Get("Impersonate-Extra-Scope"); got != "original" {
			t.Errorf("effective scope changed to %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"ConfigMapList","metadata":{"resourceVersion":"1"},"items":[]}`))
	}))
	defer server.Close()
	input := &rest.Config{Host: server.URL, Impersonate: rest.ImpersonationConfig{UserName: "user", Groups: []string{"viewers"}, Extra: map[string][]string{"scope": {"original"}}}}
	backend, err := New(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Impersonate.Groups[0] = "system:masters"
	input.Impersonate.Extra["scope"][0] = "caller-mutated"
	exposed := backend.Config()
	exposed.Impersonate.Groups[0] = "administrators"
	exposed.Impersonate.Extra["scope"][0] = "getter-mutated"
	_, _, err = backend.List(context.Background(), schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, "team", metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestConfigCopiesOwnTLSAndExecutableAuthenticationData(t *testing.T) {
	input := &rest.Config{Host: "https://example.invalid",
		TLSClientConfig: rest.TLSClientConfig{CAData: []byte("ca"), CertData: []byte("cert"), KeyData: []byte("key"), NextProtos: []string{"h2"}},
		ExecProvider:    &clientcmdapi.ExecConfig{Command: "unchanged", Args: []string{"argument"}, Env: []clientcmdapi.ExecEnvVar{{Name: "MODE", Value: "safe"}}, Config: &unstructured.Unstructured{Object: map[string]interface{}{"value": "original"}}},
		AuthProvider:    &clientcmdapi.AuthProviderConfig{Name: "example", Config: map[string]string{"setting": "original"}},
		ContentConfig:   rest.ContentConfig{GroupVersion: &schema.GroupVersion{Version: "v1"}},
	}
	// Use the exported defensive getter without constructing an executable
	// authenticator. The test never starts an authentication process.
	backend := &Backend{config: input}
	copy := backend.Config()
	copy.CAData[0] = 'x'
	copy.CertData[0] = 'x'
	copy.KeyData[0] = 'x'
	copy.NextProtos[0] = "changed"
	copy.ExecProvider.Command = "changed"
	copy.ExecProvider.Args[0] = "changed"
	copy.ExecProvider.Env[0].Value = "changed"
	copy.ExecProvider.Config.(*unstructured.Unstructured).Object["value"] = "changed"
	copy.AuthProvider.Config["setting"] = "changed"
	copy.GroupVersion.Version = "changed"
	if string(input.CAData) != "ca" || string(input.CertData) != "cert" || string(input.KeyData) != "key" || input.NextProtos[0] != "h2" || input.ExecProvider.Command != "unchanged" || input.ExecProvider.Args[0] != "argument" || input.ExecProvider.Env[0].Value != "safe" || input.ExecProvider.Config.(*unstructured.Unstructured).Object["value"] != "original" || input.AuthProvider.Config["setting"] != "original" || input.GroupVersion.Version != "v1" {
		t.Fatal("defensive config copy retained mutable identity/credential aliases")
	}
}
