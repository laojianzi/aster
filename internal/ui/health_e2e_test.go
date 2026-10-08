//go:build e2e

package uiworkbench

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/rollout"
	"github.com/laojianzi/aster/internal/testcluster"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNativeScaleTracksActualReadiness(t *testing.T) {
	f := testcluster.NewDeployment(t, 0)
	backend, err := kube.New(f.Config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	initial, err := rollout.New(backend, rollout.Options{Interval: 100 * time.Millisecond}).Observe(ctx, f.Target("fixture-ready"), f.Deployment.Generation, func(rollout.Observation) {})
	if err != nil || initial.State != rollout.Ready {
		t.Fatalf("initial readiness %+v %v", initial, err)
	}
	_, current, err := kubeconfig.Contexts("")
	if err != nil {
		t.Fatal(err)
	}
	updates := make(chan func(), 128)
	w := New()
	w.currentContext = current
	w.contexts = []string{current}
	w.namespace = f.Deployment.Namespace
	w.attach(ctx, func(fn func()) {
		select {
		case updates <- fn:
		case <-ctx.Done():
		}
	})
	t.Cleanup(func() { cancel(); w.Close() })
	tt := ui.NewTester(w.View, 1440, 1000)
	pump := func(done func() bool) {
		t.Helper()
		for {
			select {
			case fn := <-updates:
				fn()
			default:
			}
			tt.Frame()
			if done() {
				return
			}
			select {
			case <-ctx.Done():
				saveNativeScreenshot(t, tt)
				t.Fatalf("UI timeout: status=%s err=%s detail=%s health=%s", w.status, w.errText, w.detailMessage, w.healthText)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			saveNativeScreenshot(t, tt)
			t.Fatal(err)
		}
	}
	click("Connect")
	pump(func() bool { return w.status == "Live" })
	click("Deployments")
	pump(func() bool { return containsRowUID(w.rows, string(f.Deployment.UID)) })
	click(f.Deployment.Name)
	pump(func() bool { return w.detail != nil })
	click("Replica count")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("1")
	click("Preview scale")
	pump(func() bool { return !w.preparing })
	if w.plan == nil {
		t.Fatal(w.detailMessage)
	}
	live, err := f.Client.AppsV1().Deployments(f.Deployment.Namespace).Get(ctx, f.Deployment.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if *live.Spec.Replicas != 0 {
		t.Fatal("native preview changed desired state")
	}
	click("Confirm resource name")
	tt.Type(f.Deployment.Name)
	click("Execute reviewed change")
	pump(func() bool { return w.pendingWrites == 0 })
	if w.detailMode != "Health" {
		t.Fatalf("accepted workload change did not open health tracking: %s %s", w.detailMode, w.detailMessage)
	}
	pump(func() bool { return !w.healthActive })
	if !strings.Contains(w.healthText, "Tracking: Ready") {
		t.Fatal(w.healthText)
	}
	live, err = f.Client.AppsV1().Deployments(f.Deployment.Namespace).Get(ctx, f.Deployment.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if live.Status.AvailableReplicas != 1 || live.Status.ObservedGeneration < live.Generation {
		t.Fatalf("ready UI without controller readiness: %+v", live.Status)
	}
	if len(w.history) < 2 {
		t.Fatal("write acceptance and readiness must be separate history entries")
	}
	saveNativeScreenshot(t, tt)
	click("Close detail")
	if w.healthActive || w.healthText != "" {
		t.Fatal("health state survived closing the resource")
	}
}
