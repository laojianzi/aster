//go:build e2e

package testcluster

import (
	"k8s.io/client-go/rest"
	"os"
	"testing"
)

func Config(t *testing.T) *rest.Config {
	t.Helper()
	cfg, err := IsolatedConfig(os.Getenv("KUBECONFIG"), os.Getenv("ASTER_E2E_CONTEXT"), os.Getenv("ASTER_E2E_ALLOW_DESTRUCTIVE"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
