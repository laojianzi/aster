package uiworkbench

import (
	"github.com/laojianzi/aster/internal/credentialvault"
	"github.com/laojianzi/aster/internal/testvault"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeVaultUncertainStatusAtMinimumWindow(t *testing.T) {
	h := newRelationshipHarness(t, 6*time.Second)
	setupVault(t, h, &rest.Config{Host: "https://example.invalid"})
	h.w.vaultUncertain = true
	h.w.vaultStatus = credentialvault.ErrUncertain.Error()
	h.tt.SetSize(1100, 700)
	h.tt.Frame()
	for _, label := range []string{"Token to store", "Confirm credential context", "Store token", "Connect with stored token", "Forget stored token", "Credential storage status"} {
		box, ok := h.tt.Find(label)
		if !ok || box.H < 8 || box.X < 0 || box.X+box.W > 1100 || box.Y < 0 || box.Y+box.H > 700 {
			t.Fatal("credential error-state control clipped", label, box)
		}
	}
	saveNativeScreenshot(t, h.tt)
}

func TestNativeVaultCAFileChangeInvalidatesReviewedTarget(t *testing.T) {
	cfg, _, _ := authServer(t)
	h := newRelationshipHarness(t, 6*time.Second)
	store := &vaultMemory{}
	h.w.vaultStore = store
	h.w.path = testvault.ConfigFile(t, cfg, "team")
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, cfg.CAData, 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := clientcmd.LoadFromFile(h.w.path)
	if err != nil {
		t.Fatal(err)
	}
	raw.Clusters["fixture"].CertificateAuthorityData = nil
	raw.Clusters["fixture"].CertificateAuthority = ca
	data, err := clientcmd.Write(*raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(h.w.path, data, 0600); err != nil {
		t.Fatal(err)
	}
	h.w.currentContext = "vault-test"
	h.w.contexts = []string{"vault-test"}
	h.tt.Frame()
	h.click("Credentials")
	h.click("Review credential target")
	h.pump(func() bool { return !h.w.vaultPending })
	if h.w.vaultTarget != nil || h.w.vaultTrustPending == "" {
		t.Fatal("CA file loaded without explicit trust")
	}
	h.click("Trust CA file and review")
	h.pump(func() bool { return !h.w.vaultPending })
	if h.w.vaultTarget == nil {
		t.Fatal(h.w.vaultStatus)
	}
	// Even a PEM-preserving byte change needs a new review. Binding is to
	// the snapshotted trust bytes, not only to a mutable filesystem path.
	if err = os.WriteFile(ca, append(append([]byte(nil), cfg.CAData...), '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	h.w.vaultToken, h.w.vaultConfirmation = "must-not-persist", "vault-test"
	h.w.writeVault(false)
	h.pump(func() bool { return !h.w.vaultPending })
	if store.puts != 0 || store.gets != 0 || store.deletes != 0 || !strings.Contains(h.w.vaultStatus, "invalid") {
		t.Fatal("changed CA bytes did not invalidate review")
	}
	if h.w.vaultToken != "" {
		t.Fatal("rejected token input retained")
	}
}
