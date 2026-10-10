package kubeconfig

import (
	"errors"
	"github.com/laojianzi/aster/internal/credentialvault"
	"github.com/laojianzi/aster/internal/testvault"
	"k8s.io/client-go/rest"
	"os"
	"strings"
	"testing"
)

func TestVaultProfileNeverReadsOrOverridesCredentials(t *testing.T) {
	p := testvault.ConfigFile(t, &rest.Config{Host: "https://example.invalid"}, "team")
	c, target, e := LoadVault(Options{Path: p})
	if e != nil || c.ContextName != "vault-test" || c.Namespace != "team" || target.User != "vault-user" {
		t.Fatal("empty user profile rejected", e)
	}
	original, _ := os.ReadFile(p)
	for _, entry := range []string{"token: secret", "tokenFile: /never-read", "client-key: /never-read", "exec:\n      apiVersion: client.authentication.k8s.io/v1\n      command: never-run\n      interactiveMode: Never", "as-groups:\n    - admin"} {
		t.Run(strings.Split(entry, ":")[0], func(t *testing.T) {
			body := strings.Replace(string(original), "user: {}", "user:\n    "+entry, 1)
			if e := os.WriteFile(p, []byte(body), 0600); e != nil {
				t.Fatal(e)
			}
			if _, _, e := LoadVault(Options{Path: p}); !errors.Is(e, credentialvault.ErrInvalid) {
				t.Fatal("mixed user accepted", e)
			}
		})
	}
}
