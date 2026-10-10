//go:build e2e && oidce2e

package uiworkbench

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/testcluster"
	"github.com/laojianzi/aster/internal/testvault"
	rbac "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Real upstream Dex with disposable static users, driven by a bounded HTML form
// user-agent. Native controls and a real OIDC-enabled API server are exercised;
// this is not a claim of physical system-browser/enterprise-SSO qualification.
func TestNativeOIDCWithDexAndRealKubernetes(t *testing.T) {
	if os.Getenv("ASTER_OIDC_E2E") != "1" {
		t.Fatal("dedicated OIDC fixture required")
	}
	f := testcluster.NewHTTPPod(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, e := f.Client.RbacV1().Roles(f.Pod.Namespace).Create(ctx, &rbac.Role{ObjectMeta: metav1.ObjectMeta{Name: "oidc-read"}, Rules: []rbac.PolicyRule{{APIGroups: []string{""}, Resources: []string{"pods", "pods/log"}, Verbs: []string{"get", "list", "watch"}}}}, metav1.CreateOptions{})
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.Client.RbacV1().RoleBindings(f.Pod.Namespace).Create(ctx, &rbac.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "oidc-reader"}, RoleRef: rbac.RoleRef{APIGroup: rbac.GroupName, Kind: "Role", Name: "oidc-read"}, Subjects: []rbac.Subject{{Kind: "User", Name: "aster:engineer@example.test", APIGroup: rbac.GroupName}}}, metav1.CreateOptions{})
	if e != nil {
		t.Fatal(e)
	}
	h := newRelationshipHarness(t, 90*time.Second)
	h.w.path = testvault.ConfigFile(t, f.Config, f.Pod.Namespace)
	h.w.currentContext = "vault-test"
	h.w.contexts = []string{"vault-test"}
	h.w.oidcIssuer = os.Getenv("ASTER_OIDC_ISSUER")
	h.w.oidcClient = "aster-test"
	h.w.oidcPort = os.Getenv("ASTER_OIDC_PORT")
	ca, e := os.ReadFile(os.Getenv("ASTER_OIDC_CA"))
	if e != nil {
		t.Fatal("missing ephemeral provider CA")
	}
	h.w.oidcCA = string(ca)
	h.w.oidcBrowser = func(link string) error {
		script, _ := filepath.Abs("../../scripts/oidc_test_browser.py")
		c := exec.CommandContext(h.ctx, "python3", script, "--url", link, "--ca", os.Getenv("ASTER_OIDC_CA"))
		if c.Run() != nil {
			return errors.New("disposable provider form flow failed")
		}
		return nil
	}
	before, _ := os.ReadFile(h.w.path)
	h.tt.Frame()
	h.click("Browser sign-in")
	h.click("Review OIDC target")
	h.pump(func() bool { return !h.w.oidcPending })
	if h.w.oidcReview == nil {
		t.Fatal(h.w.oidcStatus)
	}
	h.click("Confirm OIDC context")
	h.tt.Type("vault-test")
	h.click("Sign in with browser")
	h.pump(func() bool { return !h.w.oidcPending })
	if h.w.oidcIdentity == nil || h.w.backend != nil {
		t.Fatal("provider verification failed or connected implicitly", h.w.oidcStatus)
	}
	h.click("Confirm OIDC context")
	h.tt.Type("vault-test")
	h.click("Connect verified identity")
	h.pump(func() bool { return h.w.status == "Live" && containsRowUID(h.w.rows, string(f.Pod.UID)) })
	backend, connection := h.w.backend, h.w.connectionCtx
	_, _, e = backend.List(h.ctx, schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}, "", metav1.ListOptions{})
	if !apierrors.IsForbidden(e) {
		t.Fatal("OIDC identity lost namespace RBAC", e)
	}
	h.click(f.Pod.Name)
	h.pump(func() bool { return h.w.detail != nil })
	h.click("Logs")
	h.click("Follow logs")
	h.pump(func() bool { return len(h.w.logRows) > 0 })
	saveNativeScreenshot(t, h.tt)
	h.pump(func() bool { return h.w.status == "Credentials expired" })
	if connection.Err() == nil || h.w.backend != nil || len(h.w.rows) > 0 || len(h.w.logRows) > 0 || h.w.oidcIdentity != nil {
		t.Fatal("OIDC expiry retained credentials/private data")
	}
	after, _ := os.ReadFile(h.w.path)
	if string(before) != string(after) {
		t.Fatal("OIDC changed profile")
	}
}
