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
	"k8s.io/client-go/rest"
)

// Real upstream Dex with disposable static users, driven by a bounded HTML form
// user-agent. Native controls and a real OIDC-enabled API server are exercised;
// this is not a claim of physical system-browser/enterprise-SSO qualification.
func newDexWorkbench(t *testing.T) (testcluster.Fixture, *relationshipHarness) {
	t.Helper()
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
	return f, h
}

func TestNativeOIDCWithDexAndRealKubernetes(t *testing.T) {
	f, h := newDexWorkbench(t)
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
	_, _, e := backend.List(h.ctx, schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}, "", metav1.ListOptions{})
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

// The independent upstream provider enforces real refresh rotation, and the
// OIDC-enabled API server, rather than our fixture signer, authorizes both IDs.
func TestNativeOIDCRenewalWithDexAndRealKubernetes(t *testing.T) {
	f, h := newDexWorkbench(t)
	before, _ := os.ReadFile(h.w.path)
	h.tt.Frame()
	h.click("Browser sign-in")
	h.click("Allow memory-only renewal (requests offline access and consent)")
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
		t.Fatal("opted-in provider verification failed", h.w.oidcStatus)
	}
	subject := h.w.oidcIdentity.Info().Subject
	h.click("Confirm OIDC context")
	h.tt.Type("vault-test")
	h.click("Connect verified identity")
	h.pump(func() bool { return h.w.status == "Live" && containsRowUID(h.w.rows, string(f.Pod.UID)) })
	if h.w.oidcRenewal == nil {
		t.Fatal("independent provider did not grant rotation")
	}
	old, oldCtx, oldSession := h.w.backend, h.w.connectionCtx, h.w.sessionID
	h.click(f.Pod.Name)
	h.pump(func() bool { return h.w.detail != nil })
	h.click("Logs")
	h.click("Follow logs")
	h.pump(func() bool { return len(h.w.logRows) > 0 })
	h.click("Browser sign-in")
	h.click("Renew identity once")
	h.pump(func() bool { return !h.w.oidcPending })
	if h.w.oidcIdentity == nil || h.w.oidcIdentity.Info().Subject != subject {
		t.Fatal("real Dex rotation failed", h.w.oidcStatus)
	}
	if h.w.backend != old || oldCtx.Err() != nil {
		t.Fatal("unconfirmed renewal changed session")
	}
	h.click("Replace verified connection")
	if h.w.connectionPending || oldCtx.Err() != nil {
		t.Fatal("second context confirmation bypassed")
	}
	h.tt.SetSize(1100, 700)
	h.tt.Frame()
	saveNativeScreenshot(t, h.tt)
	h.click("Confirm OIDC replacement context")
	h.tt.Type("vault-test")
	h.click("Replace verified connection")
	h.pump(func() bool { return h.w.status == "Live" && containsRowUID(h.w.rows, string(f.Pod.UID)) })
	if h.w.backend == old || oldCtx.Err() == nil || h.w.sessionID == oldSession || len(h.w.logRows) != 0 || h.w.oidcRenewal == nil {
		t.Fatal("renewal did not replace/clean old session")
	}
	client, e := rest.HTTPClientFor(old.Config())
	if e != nil {
		t.Fatal(e)
	}
	_, e = client.Get(f.Config.Host + "/api/v1/namespaces/" + f.Pod.Namespace + "/pods")
	if e == nil {
		t.Fatal("old OIDC transport still authorized locally")
	}
	_, _, e = h.w.backend.List(h.ctx, schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}, "", metav1.ListOptions{})
	if !apierrors.IsForbidden(e) {
		t.Fatal("renewed real identity escaped namespace authorization", e)
	}
	// A second rotation proves continuation uses the replacement capability. Its
	// pending identity may then be discarded without altering the active client.
	active := h.w.connectionCtx
	h.click("Browser sign-in")
	h.click("Renew identity once")
	h.pump(func() bool { return !h.w.oidcPending })
	if h.w.oidcIdentity == nil {
		t.Fatal("second independent rotation failed", h.w.oidcStatus)
	}
	h.click("Forget renewal")
	if active.Err() != nil || h.w.oidcRenewal != nil || h.w.oidcIdentity != nil {
		t.Fatal("forget altered connection or kept refresh result")
	}
	h.click("Disconnect")
	if active.Err() == nil || h.w.backend != nil {
		t.Fatal("disconnect retained rotated session")
	}
	after, _ := os.ReadFile(h.w.path)
	if string(before) != string(after) {
		t.Fatal("rotation rewrote kubeconfig")
	}
}
