//go:build e2e && osvault

package uiworkbench

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/credentialvault"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/testcluster"
	"github.com/laojianzi/aster/internal/testvault"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestNativeOSStoredTokenIdentityAndExpiryAgainstRealCluster(t *testing.T) {
	if os.Getenv("ASTER_OS_VAULT_TEST") != "1" {
		t.Fatal("disposable OS vault required")
	}
	f := testcluster.NewHTTPPod(t)
	token := testcluster.ReadOnlyToken(t, f)
	h := newRelationshipHarness(t, 50*time.Second)
	h.w.path = testvault.ConfigFile(t, f.Config, f.Pod.Namespace)
	h.w.currentContext = "vault-test"
	h.w.contexts = []string{"vault-test"}
	before, e := os.ReadFile(h.w.path)
	if e != nil {
		t.Fatal(e)
	}
	_, target, e := kubeconfig.LoadVault(kubeconfig.Options{Path: h.w.path})
	if e != nil {
		t.Fatal(e)
	}
	store := credentialvault.Client{}
	h.w.vaultStore = store
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if e := target.Forget(ctx, store); e != nil {
			t.Error("OS token cleanup failed", e)
		}
	})
	h.tt.Frame()
	h.click("Credentials")
	h.click("Review credential target")
	h.pump(func() bool { return !h.w.vaultPending })
	if h.w.vaultTarget == nil {
		t.Fatal(h.w.vaultStatus)
	}
	h.click("Token to store")
	h.tt.Type(token)
	h.click("Confirm credential context")
	h.tt.Type("vault-test")
	h.click("Store token")
	if h.w.vaultToken != "" {
		t.Fatal("token input retained after submission")
	}
	h.pump(func() bool { return !h.w.vaultPending })
	if h.w.backend != nil {
		t.Fatal("store automatically connected")
	}
	if !strings.Contains(h.w.vaultStatus, "Token stored in OS credentials") {
		t.Fatal("native store did not report success", h.w.vaultStatus)
	}
	saved, err := target.Resolve(h.ctx, store)
	if err != nil || saved.Config == nil || saved.Config.BearerToken != token {
		t.Fatal("native Store failed independent OS readback; token contents omitted")
	}
	saved.Config.BearerToken = ""
	// Shorten the local lifetime through the same public storage API for this
	// fixture. The UI deliberately exposes minutes/hours, not test-only seconds.
	if e = target.Put(h.ctx, store, token, time.Now().Add(12*time.Second)); e != nil {
		t.Fatal(e)
	}
	h.click("Confirm credential context")
	h.tt.Type("vault-test")
	h.click("Connect with stored token")
	h.pump(func() bool { return h.w.status == "Live" && containsRowUID(h.w.rows, string(f.Pod.UID)) })
	old, connection := h.w.backend, h.w.connectionCtx
	_, _, e = old.List(h.ctx, schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}, "", metav1.ListOptions{})
	if !apierrors.IsForbidden(e) {
		t.Fatal("stored token did not preserve namespace-only RBAC", e)
	}
	h.click(f.Pod.Name)
	h.pump(func() bool { return h.w.detail != nil })
	h.click("Logs")
	h.click("Follow logs")
	h.pump(func() bool { return len(h.w.logRows) > 0 })
	h.pump(func() bool { return h.w.status == "Credentials expired" })
	if connection.Err() == nil || h.w.backend != nil || h.w.ops != nil || h.w.detail != nil || len(h.w.rows) > 0 || len(h.w.logRows) > 0 || h.w.vaultToken != "" {
		t.Fatal("expiry retained private data")
	}
	after, e := os.ReadFile(h.w.path)
	if e != nil || string(before) != string(after) {
		t.Fatal("vault flow rewrote kubeconfig")
	}
	saveNativeScreenshot(t, h.tt)
}
