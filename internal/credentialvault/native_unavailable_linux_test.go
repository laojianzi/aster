//go:build osvault && linux

package credentialvault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOSVaultUnavailableSessionBusFailsClosed(t *testing.T) {
	if os.Getenv("ASTER_OS_VAULT_TEST") != "1" {
		t.Fatal("disposable OS-store wrapper required")
	}
	// Point only this test and its child helpers to a nonexistent private bus.
	// No real provider is stopped, locked or reconfigured by this probe.
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "missing-bus"))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := Client{}
	key := strings.Repeat("e", 64)
	for name, operation := range map[string]func() error{
		"get":    func() error { _, e := store.Get(ctx, key); return e },
		"put":    func() error { return store.Put(ctx, key, []byte("test-record")) },
		"delete": func() error { return store.Delete(ctx, key) },
	} {
		t.Run(name, func(t *testing.T) {
			if e := operation(); !errors.Is(e, ErrUnavailable) {
				t.Fatal("unavailable provider did not fail closed", e)
			}
		})
	}
}
