//go:build e2e

package uiworkbench

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/relationship"
	"github.com/laojianzi/aster/internal/rollout"
	"github.com/laojianzi/aster/internal/testcluster"
)

func TestNativeRelationshipNavigationToPodForwardAgainstRealCluster(t *testing.T) {
	f := testcluster.NewDeployment(t, 1)
	backend, err := kube.New(f.Config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	state, err := rollout.New(backend, rollout.Options{Interval: 100 * time.Millisecond}).Observe(ctx, f.Target("native-ready"), f.Deployment.Generation, func(rollout.Observation) {})
	if err != nil || state.State != rollout.Ready {
		t.Fatalf("fixture readiness: %+v %v", state, err)
	}
	_, current, err := kubeconfig.Contexts("")
	if err != nil {
		t.Fatal(err)
	}
	h := newRelationshipHarness(t, 90*time.Second)
	h.w.currentContext = current
	h.w.contexts = []string{current}
	h.w.namespace = f.Deployment.Namespace
	h.tt.Frame()
	h.click("Connect")
	h.pump(func() bool { return h.w.status == "Live" })
	h.click("Deployments")
	h.pump(func() bool { return containsRowUID(h.w.rows, string(f.Deployment.UID)) })
	h.click(f.Deployment.Name)
	h.pump(func() bool { return h.w.detail != nil })
	linkTo := func(kind string) relationship.Link {
		t.Helper()
		h.click("Related")
		h.pump(func() bool { return !h.w.relatedActive })
		for _, link := range h.w.relatedResult.Links {
			if link.Relation == "Dependent" && link.Type.Kind == kind && link.Navigable() {
				return link
			}
		}
		t.Fatalf("missing related %s: %+v %s", kind, h.w.relatedResult, h.w.relatedStatus)
		return relationship.Link{}
	}
	rs := linkTo("ReplicaSet")
	saveNativeScreenshot(t, h.tt)
	h.click("Open ReplicaSet/" + rs.Target.Name)
	h.pump(func() bool { return h.w.detail != nil })
	pod := linkTo("Pod")
	h.click("Open Pod/" + pod.Target.Name)
	h.pump(func() bool { return h.w.detail != nil })
	if h.w.detailKind.Kind != "Pod" || h.w.detail.GetUID() != pod.Target.UID || h.w.currentKind.Kind != "Deployment" || h.w.activeNamespace != f.Deployment.Namespace {
		t.Fatal("navigation changed list scope or lost resource identity")
	}
	h.click("Refresh detail")
	h.pump(func() bool { return h.w.detail != nil })
	if h.w.detailKind.Kind != "Pod" {
		t.Fatal("refresh used the wrong resource kind")
	}
	h.click("Port forward")
	h.click("Start port forward")
	h.pump(func() bool { return h.w.forwardLocal != 0 || !h.w.forwardActive })
	if h.w.forwardLocal == 0 {
		t.Fatal(h.w.forwardStatus)
	}
	address := fmt.Sprintf("127.0.0.1:%d", h.w.forwardLocal)
	response, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + address + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	response.Body.Close()
	if err != nil || string(body) != "aster-ready" {
		t.Fatalf("wrong related Pod reached: %q %v", body, err)
	}
	h.click("Close detail")
	if h.w.relatedActive || h.w.forwardActive || len(h.w.relatedResult.Links) != 0 {
		t.Fatal("resource lifecycle leaked after navigation")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if err != nil {
			break
		}
		conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("listener remained open")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
