package testauth

import (
	"os"
	"path/filepath"
	"testing"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// ConfigFile points only to the caller's test executable. Raw secret output is
// not copied to the test log; the resulting temporary kubeconfig is mode 0600.
func ConfigFile(t *testing.T, cfg *rest.Config, ns, mode string, env ...clientcmdapi.ExecEnvVar) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	vars := []clientcmdapi.ExecEnvVar{{Name: "ASTER_CREDENTIAL_FIXTURE", Value: mode}}
	vars = append(vars, env...)
	raw := clientcmdapi.Config{CurrentContext: "credential-test", Contexts: map[string]*clientcmdapi.Context{"credential-test": {Cluster: "fixture", AuthInfo: "fixture", Namespace: ns}}, Clusters: map[string]*clientcmdapi.Cluster{"fixture": {Server: cfg.Host, CertificateAuthorityData: append([]byte(nil), cfg.CAData...), TLSServerName: cfg.ServerName}}, AuthInfos: map[string]*clientcmdapi.AuthInfo{"fixture": {Exec: &clientcmdapi.ExecConfig{APIVersion: "client.authentication.k8s.io/v1", Command: exe, Args: []string{"-test.run=^TestCredentialFixture$"}, Env: vars, InteractiveMode: clientcmdapi.NeverExecInteractiveMode}}}}
	data, err := clientcmd.Write(raw)
	if err != nil {
		t.Fatal("cannot encode fixture config")
	}
	path := filepath.Join(t.TempDir(), "config")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
