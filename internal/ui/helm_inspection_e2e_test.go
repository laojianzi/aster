//go:build e2e

package uiworkbench

import (
	"reflect"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/testcluster"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNativeHelmInspectionAgainstRealCluster(t *testing.T) {
	f := testcluster.NewHelmInspection(t)
	_, current, err := kubeconfig.Contexts("")
	if err != nil {
		t.Fatal(err)
	}
	h := newRelationshipHarness(t, 90*time.Second)
	h.w.currentContext = current
	h.w.contexts = []string{current}
	h.w.namespace = f.Namespace
	h.tt.Frame()
	h.click("Connect")
	h.pump(func() bool { return h.w.status == "Live" })
	h.click("Helm releases")
	h.click("Refresh Helm metadata")
	h.pump(func() bool { return !h.w.helmReleases.busy })
	h.click("Select " + f.Secrets[0].Name)
	if h.w.helmReleases.inspection != nil || h.w.helmReleases.inspecting {
		t.Fatal("selection fetched content")
	}
	startInspectionUI(h)
	h.pump(func() bool { return !h.w.helmReleases.inspecting })
	result := h.w.helmReleases.inspection
	if result == nil || result.ChartName != "example-chart" || result.SuppliedValues != 2 || h.w.pendingWrites != 0 || h.w.plan != nil || h.w.delivery.plan != nil {
		t.Fatal("native content flow", h.w.helmReleases.contentStatus)
	}
	saveNativeScreenshot(t, h.tt)
	before := f.Secrets[0]
	after, err := f.Client.CoreV1().Secrets(f.Namespace).Get(h.ctx, before.Name, metav1.GetOptions{})
	if err != nil || after.UID != before.UID || after.ResourceVersion != before.ResourceVersion || !reflect.DeepEqual(after.Data, before.Data) {
		t.Fatal("native inspection changed Secret", err)
	}
	h.click("Close Helm metadata")
	h.click("Disconnect")
	if h.w.backend != nil || h.w.helmReleases.inspection != nil || h.w.helmReleases.acknowledged {
		t.Fatal("teardown retained content")
	}
}
