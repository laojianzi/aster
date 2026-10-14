//go:build e2e

package uiworkbench

import (
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/testcluster"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestNativeHelmRenderedDeliveryAgainstRealCluster(t *testing.T) {
	distribution, chart := helmTestChart(t, helmConfigTemplate)
	f := testcluster.NewManifestDelivery(t)
	reader, err := kube.New(f.Config)
	if err != nil {
		t.Fatal(err)
	}
	_, current, err := kubeconfig.Contexts("")
	if err != nil {
		t.Fatal(err)
	}
	h := newRelationshipHarness(t, 3*time.Minute)
	h.w.currentContext = current
	h.w.contexts = []string{current}
	h.w.namespace = f.Namespace
	h.tt.Frame()
	h.click("Connect")
	h.pump(func() bool { return h.w.status == "Live" })
	h.click("Deliver manifests")
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	absent := func(names ...string) {
		t.Helper()
		for _, name := range names {
			if _, err := reader.GetObject(h.ctx, gvr, f.Namespace, name); !apierrors.IsNotFound(err) {
				t.Fatalf("render/import/preview wrote %s: %v", name, err)
			}
		}
	}
	absent("helm-real-one", "helm-real-two")
	configureHelmUI(h, distribution, chart, "helm-real")
	captureHelmUI(h)
	absent("helm-real-one", "helm-real-two")
	if h.w.delivery.set != nil || h.w.delivery.plan != nil || h.w.pendingWrites != 0 {
		t.Fatal("render created approval or pending writes")
	}
	acceptHelmUI(h)
	absent("helm-real-one", "helm-real-two")
	importDeliveryUI(h)
	if h.w.delivery.set.Len() != 2 || h.w.delivery.set.Namespace() != f.Namespace || h.w.delivery.set.SessionID() != h.w.sessionID {
		t.Fatal("rendered inventory lost target/session")
	}
	absent("helm-real-one", "helm-real-two")
	previewDeliveryUI(h, "Preview next object")
	absent("helm-real-one", "helm-real-two")
	submitDeliveryUI(h, "wrong")
	absent("helm-real-one", "helm-real-two")
	submitDeliveryUI(h, "helm-real-one")
	absent("helm-real-two")
	cm, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(h.ctx, "helm-real-one", metav1.GetOptions{})
	if err != nil || cm.Data["message"] != "中文 🚢 é" || cm.UID != h.w.delivery.outcomes[0].UID {
		t.Fatalf("accepted real Helm object differs: %v", err)
	}
	if h.w.delivery.plan != nil || h.w.delivery.outcomes[1].State == "Succeeded" {
		t.Fatal("first object authorized a later write")
	}
	previewDeliveryUI(h, "Preview next object")
	absent("helm-real-two")
	submitDeliveryUI(h, "helm-real-two")
	second, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(h.ctx, "helm-real-two", metav1.GetOptions{})
	if err != nil || second.UID != h.w.delivery.outcomes[1].UID || second.Data["message"] != "second" {
		t.Fatalf("second explicit confirmation failed: %v", err)
	}
	saveNativeScreenshot(t, h.tt)
	h.click("Back to resources")
	h.click("Disconnect")
	h.pump(func() bool { return h.w.backend == nil })
	if h.w.delivery.helm.result != nil || h.w.delivery.origin != "" {
		t.Fatal("disconnect retained private Helm result")
	}
}
