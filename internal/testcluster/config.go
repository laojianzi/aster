package testcluster

import (
	"errors"
	"path/filepath"
	"strings"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// IsolatedConfig cannot fall back to ~/.kube/config or in-cluster credentials.
// This opt-in prevents accidental destructive tests against the developer's
// current cluster; it is not a proof that an endpoint is disposable.
func IsolatedConfig(path, expectedContext, optIn string) (*rest.Config, error) {
	if optIn != "1" {
		return nil, errors.New("destructive E2E is disabled: use scripts/e2e.sh or explicitly set ASTER_E2E_ALLOW_DESTRUCTIVE=1 for a disposable kind cluster")
	}
	if path == "" || !filepath.IsAbs(path) || len(filepath.SplitList(path)) != 1 {
		return nil, errors.New("E2E requires one explicit absolute KUBECONFIG file")
	}
	if expectedContext != "kind-aster-e2e" && !strings.HasPrefix(expectedContext, "kind-aster-e2e-") {
		return nil, errors.New("ASTER_E2E_CONTEXT must name the dedicated kind-aster-e2e context")
	}
	raw, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return nil, err
	}
	if raw.CurrentContext != expectedContext || raw.Contexts[expectedContext] == nil {
		return nil, errors.New("kubeconfig current context does not match the explicit E2E context")
	}
	selected := raw.Contexts[expectedContext]
	if auth := raw.AuthInfos[selected.AuthInfo]; auth != nil && (auth.Exec != nil || auth.AuthProvider != nil) {
		return nil, errors.New("E2E fixtures do not allow external authentication plugins")
	}
	cfg, err := clientcmd.NewNonInteractiveClientConfig(*raw, expectedContext, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		return nil, err
	}
	cfg.UserAgent = "aster-isolated-e2e"
	return cfg, nil
}
