//go:build e2e

package uiworkbench

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/operation"
	"github.com/laojianzi/aster/internal/testcluster"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func TestNativeApplyReviewAgainstRealCluster(t *testing.T) {
	cfg := testcluster.Config(t)
	admin, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ns, err := admin.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "aster-native-apply-"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		_ = admin.CoreV1().Namespaces().Delete(bg, ns.Name, metav1.DeleteOptions{})
	})
	cm, err := admin.CoreV1().ConfigMaps(ns.Name).Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "native-applied"}, Data: map[string]string{"foreign": "keep-controller-value"}}, metav1.CreateOptions{FieldManager: "fixture-controller"})
	if err != nil {
		t.Fatal(err)
	}
	_, current, err := kubeconfig.Contexts("")
	if err != nil {
		t.Fatal(err)
	}
	h := newRelationshipHarness(t, 90*time.Second)
	h.w.currentContext = current
	h.w.contexts = []string{current}
	h.w.namespace = ns.Name
	h.tt.Frame()
	h.click("Connect")
	h.pump(func() bool { return h.w.status == "Live" })
	h.click("ConfigMaps")
	h.pump(func() bool { return containsRowUID(h.w.rows, string(cm.UID)) })
	h.click(cm.Name)
	h.pump(func() bool { return h.w.detail != nil })
	h.click("Owners")
	if !strings.Contains(h.w.ownersText, "fixture-controller") {
		t.Fatal("actual field manager was not displayed")
	}
	h.click("Edit")
	h.click("Apply")
	if strings.Contains(h.w.applyDraft, "keep-controller-value") {
		t.Fatal("live document silently copied into apply intent")
	}
	h.click("Apply intent YAML")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: %s\n  namespace: %s\ndata:\n  managed: applied-through-native-ui\n", cm.Name, ns.Name))
	_ = h.tt.Click("Preview apply")
	if h.w.preparing || h.w.plan != nil {
		t.Fatal("unacknowledged apply started")
	}
	h.click("I understand omitted fields may be removed")
	h.click("Preview apply")
	h.pump(func() bool { return !h.w.preparing })
	if h.w.plan == nil || h.w.plan.Kind() != "apply" || !strings.Contains(h.w.diff, "FIELD OWNERSHIP") || !strings.Contains(h.w.diff, operation.ApplyFieldManager) {
		t.Fatal("bad SSA review: " + h.w.detailMessage)
	}
	actual, err := admin.CoreV1().ConfigMaps(ns.Name).Get(ctx, cm.Name, metav1.GetOptions{})
	if err != nil || actual.ResourceVersion != cm.ResourceVersion || len(actual.Data) != 1 {
		t.Fatalf("preview wrote: %v", err)
	}
	h.click("Confirm resource name")
	h.tt.Type("wrong-name")
	_ = h.tt.Click("Execute reviewed change")
	if h.w.pendingWrites != 0 {
		t.Fatal("wrong confirmation submitted apply")
	}
	h.click("Confirm resource name")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(cm.Name)
	h.click("Execute reviewed change")
	h.pump(func() bool { return h.w.pendingWrites == 0 && len(h.w.history) > 0 })
	actual, err = admin.CoreV1().ConfigMaps(ns.Name).Get(ctx, cm.Name, metav1.GetOptions{})
	if err != nil || actual.Data["managed"] != "applied-through-native-ui" || actual.Data["foreign"] != "keep-controller-value" {
		t.Fatalf("apply not persisted safely: %v UI=%s", err, h.w.detailMessage)
	}
	h.click("Owners")
	if !strings.Contains(h.w.ownersText, operation.ApplyFieldManager) || h.w.applyDraft != "" || h.w.applyAcknowledged {
		t.Fatal("ownership or post-execution lifecycle incorrect")
	}
	saveNativeScreenshot(t, h.tt)
}
