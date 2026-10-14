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

func TestNativeHelmReleaseMetadataAgainstRealCluster(t *testing.T) {
	f := testcluster.NewHelmMetadata(t, 3)
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
	result := h.w.helmReleases.snapshot
	if len(result.Revisions) != 3 || result.Namespace != f.Namespace || result.Revisions[0].Number != 3 || h.w.pendingWrites != 0 || h.w.plan != nil || h.w.delivery.plan != nil {
		t.Fatalf("native metadata flow: %+v %s", result, h.w.helmReleases.status)
	}
	saveNativeScreenshot(t, h.tt)
	for _, before := range f.Secrets {
		after, err := f.Client.CoreV1().Secrets(f.Namespace).Get(h.ctx, before.Name, metav1.GetOptions{})
		if err != nil || after.UID != before.UID || after.ResourceVersion != before.ResourceVersion || !reflect.DeepEqual(after.Data, before.Data) {
			t.Fatal("native metadata flow changed Secret", err)
		}
	}
	h.click("Close Helm metadata")
	h.click("Disconnect")
	if h.w.backend != nil || h.w.helmReleases.open || len(h.w.helmReleases.snapshot.Revisions) != 0 {
		t.Fatal("teardown retained metadata")
	}
}
