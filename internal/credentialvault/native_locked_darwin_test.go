//go:build osvault && darwin

package credentialvault

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Lock only the wrapper's disposable default keychain. Never lock a user's
// login keychain, even if this integration test is invoked manually by mistake.
func TestOSVaultLockedDefaultKeychainFailsClosed(t *testing.T) {
	path := os.Getenv("ASTER_TEST_KEYCHAIN_PATH")
	password := os.Getenv("ASTER_TEST_KEYCHAIN_PASSWORD")
	if os.Getenv("ASTER_OS_VAULT_TEST") != "1" || !filepath.IsAbs(path) || filepath.Base(path) != "test.keychain-db" || !strings.HasPrefix(filepath.Base(filepath.Dir(path)), "aster-vault-") || password == "" {
		t.Fatal("explicit disposable keychain wrapper required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	got, err := exec.CommandContext(ctx, "/usr/bin/security", "default-keychain", "-d", "user").Output()
	actual := strings.Trim(strings.TrimSpace(string(got)), "\"")
	if err != nil || !filepath.IsAbs(actual) {
		t.Fatal("unable to establish disposable default keychain")
	}
	// macOS security may canonicalise /var to /private/var. Compare the
	// actual filesystem identity, not spelling, without accepting another file.
	wantInfo, wantErr := os.Stat(path)
	gotInfo, gotErr := os.Stat(actual)
	if wantErr != nil || gotErr != nil || !wantInfo.Mode().IsRegular() || !os.SameFile(wantInfo, gotInfo) {
		t.Fatal("refusing to lock a keychain other than the disposable default")
	}
	if err = exec.CommandContext(ctx, "/usr/bin/security", "lock-keychain", path).Run(); err != nil {
		t.Fatal("failed to lock disposable keychain")
	}
	t.Cleanup(func() {
		c, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if e := exec.CommandContext(c, "/usr/bin/security", "unlock-keychain", "-p", password, path).Run(); e != nil {
			t.Error("failed to restore disposable keychain lock state")
		}
	})
	key := strings.Repeat("d", 64)
	store := Client{}
	for name, operation := range map[string]func() error{
		"get":    func() error { _, e := store.Get(ctx, key); return e },
		"put":    func() error { return store.Put(ctx, key, []byte("test-record")) },
		"delete": func() error { return store.Delete(ctx, key) },
	} {
		t.Run(name, func(t *testing.T) {
			if e := operation(); !errors.Is(e, ErrUnavailable) {
				t.Fatal("locked default store did not fail closed", e)
			}
		})
	}
}
