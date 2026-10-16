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

func TestNativeHelmLiveObservationAgainstRealCluster(t *testing.T) {
	f := testcluster.NewHelmUninstallPreview(t)
	_, current, e := kubeconfig.Contexts("")
	if e != nil {
		t.Fatal(e)
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
	before, e := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(h.ctx, "preview-config", metav1.GetOptions{})
	if e != nil {
		t.Fatal(e)
	}
	startUninstallPreviewUI(h)
	h.pump(func() bool { return !h.w.helmReleases.inspecting })
	startLiveUI(h)
	h.pump(func() bool { return !h.w.helmReleases.inspecting })
	r := &h.w.helmReleases
	if r.live == nil || len(r.live.Items()) != 2 || r.live.Items()[0].State != "present" || r.live.Items()[0].Markers != "missing" || r.live.Items()[1].State != "absent" || r.liveAcknowledged || r.acknowledged || h.w.pendingWrites != 0 || h.w.plan != nil {
		t.Fatal("live UI", r.liveStatus)
	}
	saveNativeScreenshot(t, h.tt)
	after, e := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(h.ctx, "preview-config", metav1.GetOptions{})
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("canary modified", e)
	}
	h.click("Close Helm metadata")
	h.click("Disconnect")
	if h.w.helmReleases.live != nil || h.w.helmReleases.preview != nil || h.w.backend != nil {
		t.Fatal("retained on disconnect")
	}
}
