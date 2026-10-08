package testcluster

import (
	"path/filepath"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestE2ERequiresExplicitDisposableContextAndNeverRunsExec(t *testing.T) {
	raw := clientcmdapi.NewConfig()
	raw.CurrentContext = "kind-aster-e2e"
	raw.Contexts[raw.CurrentContext] = &clientcmdapi.Context{Cluster: "test", AuthInfo: "test"}
	raw.Clusters["test"] = &clientcmdapi.Cluster{Server: "https://127.0.0.1:6443"}
	raw.AuthInfos["test"] = &clientcmdapi.AuthInfo{Token: "synthetic-test-token"}
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := clientcmd.WriteToFile(*raw, path); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, name, optIn string }{
		{path, raw.CurrentContext, ""}, {"", raw.CurrentContext, "1"}, {"relative-config", raw.CurrentContext, "1"},
		{path, "kind-production", "1"}, {path, "kind-aster-e2e-other", "1"},
	} {
		if _, err := IsolatedConfig(tc.path, tc.name, tc.optIn); err == nil {
			t.Fatalf("unsafe E2E config accepted: %+v", tc)
		}
	}
	cfg, err := IsolatedConfig(path, raw.CurrentContext, "1")
	if err != nil || cfg.Host != "https://127.0.0.1:6443" {
		t.Fatalf("valid dedicated config: %v", err)
	}
	raw.AuthInfos["test"].Exec = &clientcmdapi.ExecConfig{Command: "never-execute-this", APIVersion: "client.authentication.k8s.io/v1", InteractiveMode: clientcmdapi.NeverExecInteractiveMode}
	if err := clientcmd.WriteToFile(*raw, path); err != nil {
		t.Fatal(err)
	}
	if _, err := IsolatedConfig(path, raw.CurrentContext, "1"); err == nil {
		t.Fatal("exec plugin accepted by destructive fixture loader")
	}
}
