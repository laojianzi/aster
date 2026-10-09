//go:build e2e && multicluster

package uiworkbench

import (
	"context"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/testcluster"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Both endpoints must be separate disposable kind control planes. This suite
// fails when either config is absent; it never falls back to ~/.kube/config.
func multiConfig(t *testing.T, variable, expected string) (*rest.Config, string) {
	t.Helper()
	path := os.Getenv(variable)
	cfg, err := testcluster.IsolatedConfig(path, expected, os.Getenv("ASTER_E2E_ALLOW_DESTRUCTIVE"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(cfg.Host)
	if err != nil || u.Scheme != "https" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || cfg.TLSClientConfig.Insecure {
		t.Fatal("multi-cluster tests require verified TLS loopback endpoints")
	}
	return cfg, path
}
func TestNativeIndependentWorkspacesAgainstTwoRealClusters(t *testing.T) {
	cfgA, pathA := multiConfig(t, "ASTER_MULTI_KUBECONFIG_A", "kind-aster-e2e-east")
	cfgB, pathB := multiConfig(t, "ASTER_MULTI_KUBECONFIG_B", "kind-aster-e2e-west")
	if cfgA.Host == cfgB.Host || pathA == pathB {
		t.Fatal("two configs resolve to the same cluster")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var clients []*kubernetes.Clientset
	var roots []*corev1.Namespace
	var maps []*corev1.ConfigMap
	nsName := ""
	for i, cfg := range []*rest.Config{cfgA, cfgB} {
		client, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, client)
		root, err := client.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		roots = append(roots, root)
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "aster-multi-"}}
		if i == 1 {
			ns.Name = nsName
			ns.GenerateName = ""
		}
		ns, err = client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		nsName = ns.Name
		t.Cleanup(func() {
			cleanup, done := context.WithTimeout(context.Background(), 20*time.Second)
			defer done()
			uid := ns.UID
			_ = client.CoreV1().Namespaces().Delete(cleanup, ns.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
		})
		site := []string{"east", "west"}[i]
		cm, err := client.CoreV1().ConfigMaps(ns.Name).Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "shared-name"}, Data: map[string]string{"site": site}}, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		maps = append(maps, cm)
	}
	if roots[0].UID == roots[1].UID || maps[0].UID == maps[1].UID {
		t.Fatal("control planes or resources are not independent")
	}
	t.Logf("Independent control-plane identities: %s / %s", roots[0].UID, roots[1].UID)
	a, b := newRelationshipHarness(t, 2*time.Minute), newRelationshipHarness(t, 2*time.Minute)
	for i, h := range []*relationshipHarness{a, b} {
		h.w.path = []string{pathA, pathB}[i]
		h.w.currentContext = []string{"kind-aster-e2e-east", "kind-aster-e2e-west"}[i]
		h.w.contexts = []string{h.w.currentContext}
		h.w.namespace = nsName
		h.w.workspaceNumber = i + 1
		h.tt.Frame()
		h.click("Connect")
	}
	for i, h := range []*relationshipHarness{a, b} {
		h.pump(func() bool { return h.w.status == "Live" })
		h.click("ConfigMaps")
		h.pump(func() bool { return containsRowUID(h.w.rows, string(maps[i].UID)) })
		h.click("shared-name")
		h.pump(func() bool { return h.w.detail != nil })
		site, _, _ := unstructured.NestedString(h.w.detail.Object, "data", "site")
		if site != []string{"east", "west"}[i] {
			t.Fatal("same-name resource crossed workspaces")
		}
	}
	if a.w.sessionID == b.w.sessionID || a.w.backend == b.w.backend || a.w.ops == b.w.ops {
		t.Fatal("identity/session objects were shared")
	}
	a.click("Edit")
	a.click("Manifest editor")
	next := a.w.detail.DeepCopy()
	_ = unstructured.SetNestedField(next.Object, "east-reviewed", "data", "site")
	text, err := manifest.Display(next, false)
	if err != nil {
		t.Fatal(err)
	}
	a.tt.Key(ui.Cmd, ui.KeyA)
	a.tt.Type(text)
	a.click("Preview edit")
	a.pump(func() bool { return a.w.plan != nil || (!a.w.preparing && a.w.detailMessage != "") })
	if a.w.plan == nil {
		t.Fatalf("preview failed: %s", a.w.detailMessage)
	}
	// Even a misplaced in-process plan must be rejected by the other issuer.
	result, err := b.w.ops.Execute(ctx, a.w.plan)
	if err == nil || result.State != "Rejected" {
		t.Fatal("a plan from east was accepted by west")
	}
	for i, client := range clients {
		current, getErr := client.CoreV1().ConfigMaps(nsName).Get(ctx, "shared-name", metav1.GetOptions{})
		if getErr != nil || current.Data["site"] != []string{"east", "west"}[i] {
			t.Fatal("preview or cross-session rejection mutated a cluster")
		}
	}
	a.click("Confirm resource name")
	a.tt.Type("shared-name")
	a.click("Execute reviewed change")
	a.pump(func() bool { return a.w.pendingWrites == 0 && len(a.w.history) > 0 })
	if !strings.Contains(a.w.history[len(a.w.history)-1], "Succeeded") {
		t.Fatal(a.w.history)
	}
	for i, client := range clients {
		current, getErr := client.CoreV1().ConfigMaps(nsName).Get(ctx, "shared-name", metav1.GetOptions{})
		if getErr != nil || current.Data["site"] != []string{"east-reviewed", "west"}[i] {
			t.Fatal("reviewed change crossed cluster boundary")
		}
	}
	a.w.Close() // close only the first workspace, not the application
	// Prove the surviving watch still receives a new real event after A joins.
	current, err := clients[1].CoreV1().ConfigMaps(nsName).Get(ctx, "shared-name", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	current.Data["site"] = "west-survived"
	current, err = clients[1].CoreV1().ConfigMaps(nsName).Update(ctx, current, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b.pump(func() bool {
		for _, row := range b.w.rows {
			if row.UID == string(current.UID) && row.RV == current.ResourceVersion {
				return true
			}
		}
		return false
	})
	b.click("Refresh detail")
	b.pump(func() bool { return b.w.detail != nil })
	site, _, _ := unstructured.NestedString(b.w.detail.Object, "data", "site")
	if site != "west-survived" || b.w.ctx.Err() != nil {
		t.Fatal("surviving workspace no longer functional")
	}
	saveNativeScreenshot(t, b.tt)
	b.click("Disconnect")
	if b.w.backend != nil {
		t.Fatal("surviving workspace failed to disconnect")
	}
}
