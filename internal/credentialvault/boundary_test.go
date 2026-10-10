package credentialvault

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestVaultMaximumTokenEnvelopeFitsNativeAndPipeBudgets(t *testing.T) {
	t.Setenv("ASTER_TEST_KEYCHAIN_PASSWORD", "must-not-enter-product-helper")
	for _, item := range helperEnvironment() {
		if strings.Contains(item, "must-not-enter-product-helper") {
			t.Fatal("test setup password inherited by product helper")
		}
	}
	v := target(t)
	store := &memoryStore{}
	if err := v.Put(context.Background(), store, strings.Repeat("A", MaxTokenBytes), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	raw := store.data[v.Key()]
	if len(raw) > MaxRecordBytes {
		t.Fatal("token envelope exceeds native blob budget")
	}
	payload, err := json.Marshal(request{"put", v.Key(), raw})
	if err != nil || len(payload) > 4096 {
		t.Fatal("maximum token envelope exceeds helper pipe budget")
	}
	resolved, err := v.Resolve(context.Background(), store)
	if err != nil || len(resolved.Config.BearerToken) != MaxTokenBytes {
		t.Fatal("maximum token did not resolve", err)
	}
}
