package kubeconfig

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUntrustedExecAndFileAreRejectedBeforeAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	text := "apiVersion: v1\nkind: Config\ncurrent-context: dangerous\nclusters:\n- name: cluster\n  cluster:\n    server: https://127.0.0.1:6443\ncontexts:\n- name: dangerous\n  context:\n    cluster: cluster\n    user: user\n    namespace: team\nusers:\n- name: user\n  user:\n    tokenFile: /nonexistent/never-read-this\n    exec:\n      apiVersion: client.authentication.k8s.io/v1\n      command: never-execute-this\n      interactiveMode: Never\n"
	if err := os.WriteFile(path, []byte(text), 0600); err != nil { t.Fatal(err) }
	items, current, err := Contexts(path)
	if err != nil || current != "dangerous" || len(items) != 1 || !items[0].RequiresTrust { t.Fatalf("%v %s %v", items, current, err) }
	_, err = Load(Options{Path: path})
	var trust *TrustRequiredError
	if !errors.As(err, &trust) { t.Fatalf("expected trust rejection, got %v", err) }
}

func TestEmbeddedTokenConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	text := "apiVersion: v1\nkind: Config\ncurrent-context: local\nclusters:\n- name: cluster\n  cluster:\n    server: https://127.0.0.1:6443\ncontexts:\n- name: local\n  context:\n    cluster: cluster\n    user: user\n    namespace: team\nusers:\n- name: user\n  user:\n    token: test-token-not-a-secret\n"
	if err := os.WriteFile(path, []byte(text), 0600); err != nil { t.Fatal(err) }
	c, err := Load(Options{Path: path})
	if err != nil || c.Namespace != "team" || c.Config.BearerToken != "test-token-not-a-secret" { t.Fatalf("load: %v", err) }
	if _, err = Load(Options{Path: path, Context: "missing"}); err == nil { t.Fatal("missing context accepted") }
}
