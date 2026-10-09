// Package testvault constructs disposable credential-free profiles for tests.
package testvault

import (
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	api "k8s.io/client-go/tools/clientcmd/api"
	"os"
	"path/filepath"
	"testing"
)

func ConfigFile(t *testing.T, cfg *rest.Config, ns string) string {
	t.Helper()
	raw := api.Config{CurrentContext: "vault-test", Contexts: map[string]*api.Context{"vault-test": {Cluster: "fixture", AuthInfo: "vault-user", Namespace: ns}}, Clusters: map[string]*api.Cluster{"fixture": {Server: cfg.Host, CertificateAuthorityData: append([]byte(nil), cfg.CAData...), TLSServerName: cfg.ServerName}}, AuthInfos: map[string]*api.AuthInfo{"vault-user": {}}}
	data, e := clientcmd.Write(raw)
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(t.TempDir(), "config")
	if e = os.WriteFile(p, data, 0600); e != nil {
		t.Fatal(e)
	}
	return p
}
