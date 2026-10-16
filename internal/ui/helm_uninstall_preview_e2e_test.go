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

func TestNativeHelmUninstallPreviewAgainstRealCluster(t *testing.T) {
	f := testcluster.NewHelmUninstallPreview(t)
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
	before, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(h.ctx, "preview-config", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	startUninstallPreviewUI(h)
	h.pump(func() bool { return !h.w.helmReleases.inspecting })
	p := h.w.helmReleases.preview
	if p == nil || len(p.Items()) != 2 || !p.Items()[0].Keep || p.Summary().HookCount != 1 || h.w.helmReleases.acknowledged || h.w.plan != nil || h.w.delivery.plan != nil || h.w.pendingWrites != 0 {
		t.Fatal("native preview", h.w.helmReleases.contentStatus)
	}
	saveNativeScreenshot(t, h.tt)
	after, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(h.ctx, "preview-config", metav1.GetOptions{})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("canary modified", err)
	}
	storage, err := f.Client.CoreV1().Secrets(f.Namespace).Get(h.ctx, f.Secrets[0].Name, metav1.GetOptions{})
	if err != nil || storage.UID != f.Secrets[0].UID || storage.ResourceVersion != f.Secrets[0].ResourceVersion || !reflect.DeepEqual(storage.Data, f.Secrets[0].Data) {
		t.Fatal("storage modified", err)
	}
	h.click("Close Helm metadata")
	h.click("Disconnect")
	if h.w.helmReleases.preview != nil || h.w.helmReleases.acknowledged || h.w.backend != nil {
		t.Fatal("teardown retained state")
	}
}
