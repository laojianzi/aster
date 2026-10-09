//go:build osvault

package credentialvault

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"
)

// This test is NEVER skipped or counted as a unit test. CI runs it only under
// scripts/with_test_vault.py, in an isolated ephemeral OS credential store.
func TestOSCredentialStoreRoundTripAcrossProcesses(t *testing.T) {
	if os.Getenv("ASTER_OS_VAULT_TEST") != "1" {
		t.Fatal("disposable OS-store wrapper required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	keyBytes := make([]byte, 32)
	if _, e := rand.Read(keyBytes); e != nil {
		t.Fatal(e)
	}
	key := hex.EncodeToString(keyBytes)
	otherBytes := make([]byte, 32)
	if _, e := rand.Read(otherBytes); e != nil {
		t.Fatal(e)
	}
	other := hex.EncodeToString(otherBytes)
	store := Client{}
	t.Cleanup(func() {
		c, stop := context.WithTimeout(context.Background(), 25*time.Second)
		defer stop()
		for _, k := range []string{key, other} {
			if e := store.Delete(c, k); e != nil && !errors.Is(e, ErrNotFound) {
				t.Errorf("OS credential cleanup failed: %v", e)
			}
		}
	})
	if _, e := store.Get(ctx, key); !errors.Is(e, ErrNotFound) {
		t.Fatal("new test key not empty", e)
	}
	if e := store.Put(ctx, key, []byte("test-record-one")); e != nil {
		t.Fatal("native put", e)
	}
	if e := store.Put(ctx, other, []byte("test-record-other")); e != nil {
		t.Fatal("native other put", e)
	}
	if e := store.Put(ctx, key, []byte("test-record-replaced")); e != nil {
		t.Fatal("native replace", e)
	}
	got, e := store.Get(ctx, key)
	if e != nil || string(got) != "test-record-replaced" {
		t.Fatal("native process-persistent roundtrip failed", e)
	}
	got, e = store.Get(ctx, other)
	if e != nil || string(got) != "test-record-other" {
		t.Fatal("separate key overwritten", e)
	}
	if e := store.Delete(ctx, key); e != nil {
		t.Fatal("native delete", e)
	}
	if _, e = store.Get(ctx, key); !errors.Is(e, ErrNotFound) {
		t.Fatal("deleted key still retrievable", e)
	}
}
