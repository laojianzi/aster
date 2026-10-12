//go:build e2e

package uiworkbench

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/operation"
	"github.com/laojianzi/aster/internal/testcluster"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

func TestNativeManifestDeliveryAgainstRealCluster(t *testing.T) {
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
	if !h.w.delivery.open {
		t.Fatal("delivery entry did not open")
	}
	importSource := func(source []byte, count int) {
		t.Helper()
		h.click("Delivery manifests")
		h.tt.Key(ui.Cmd, ui.KeyA)
		h.tt.Type(string(source))
		h.click("Import manifests")
		h.pump(func() bool { return h.w.delivery.busy == "" })
		if h.w.delivery.set == nil || h.w.delivery.set.Len() != count || h.w.delivery.set.Namespace() != f.Namespace || h.w.delivery.set.SessionID() != h.w.sessionID {
			t.Fatalf("native import did not bind the actual reviewed target: %s", h.w.delivery.status)
		}
		if h.w.delivery.plan != nil || h.w.pendingWrites != 0 {
			t.Fatal("import prepared or submitted an object automatically")
		}
	}
	preview := func(name, kind string) *operation.Prepared {
		t.Helper()
		h.click("Preview next object")
		h.pump(func() bool { return h.w.delivery.busy == "" })
		p := h.w.delivery.plan
		if p == nil || p.Kind() != kind || p.Target().Name != name || p.Target().Namespace != f.Namespace {
			t.Fatalf("native preview chose a different ordered object: %s", h.w.delivery.status)
		}
		return p
	}
	confirmAndSubmit := func(name string, index int) types.UID {
		t.Helper()
		h.click("Delivery confirmation")
		h.tt.Key(ui.Cmd, ui.KeyA)
		h.tt.Type("incorrect-" + name)
		_ = h.tt.Click("Submit reviewed object")
		if h.w.pendingWrites != 0 || h.w.delivery.plan == nil || h.w.delivery.outcomes[index].State != "Ready" {
			t.Fatal("incorrect resource-name confirmation submitted delivery")
		}
		h.click("Delivery confirmation")
		h.tt.Key(ui.Cmd, ui.KeyA)
		h.tt.Type(name)
		h.click("Submit reviewed object")
		h.pump(func() bool { return h.w.delivery.busy == "" && h.w.pendingWrites == 0 })
		if h.w.delivery.outcomes[index].State != "Succeeded" || h.w.delivery.outcomes[index].UID == "" || h.w.delivery.plan != nil || h.w.delivery.confirmation != "" {
			t.Fatalf("native per-object submission failed or retained approval: %s", h.w.delivery.status)
		}
		return h.w.delivery.outcomes[index].UID
	}

	importSource(f.RenderedApplication(""), 3)
	items := h.w.delivery.set.Items()
	setID := h.w.delivery.set.ID()
	for i, item := range items {
		if item.Namespace != f.Namespace || item.Name != []string{"delivery-config", "delivery-service", "delivery-workload"}[i] {
			t.Fatal("native inventory lost source order or namespace resolution")
		}
		if _, err := reader.GetObject(h.ctx, item.Kind.GVR, item.Namespace, item.Name); !apierrors.IsNotFound(err) {
			t.Fatalf("native import persisted a resource: %v", err)
		}
	}
	// Selecting a later inventory row is for inspection, never permission to
	// bypass source order or turn this UI into an automatic batch executor.
	h.click("3. delivery-workload")
	createdUIDs := make(map[string]types.UID)
	for i, item := range items {
		p := preview(item.Name, "create")
		if p.Target().UID != "" || !strings.Contains(h.w.delivery.diff, "CREATE ONLY") {
			t.Fatal("new resource was not reviewed as create-only")
		}
		for _, pending := range items[i:] {
			if _, err := reader.GetObject(h.ctx, pending.Kind.GVR, pending.Namespace, pending.Name); !apierrors.IsNotFound(err) {
				t.Fatalf("preview or previous submit wrote a later object: %v", err)
			}
		}
		uid := confirmAndSubmit(item.Name, i)
		actual, err := reader.GetObject(h.ctx, item.Kind.GVR, item.Namespace, item.Name)
		if err != nil || actual.GetUID() != uid || h.w.delivery.set.ID() != setID {
			t.Fatalf("native outcome was not the actual accepted incarnation: %v", err)
		}
		createdUIDs[item.Name] = uid
		if len(h.w.delivery.outcomes[i].Attempts) != 1 || h.w.delivery.outcomes[i].Attempts[0].Kind != "create" {
			t.Fatal("native create repeated or lost its per-object attempt")
		}
		for _, pending := range items[i+1:] {
			if _, err := reader.GetObject(h.ctx, pending.Kind.GVR, pending.Namespace, pending.Name); !apierrors.IsNotFound(err) {
				t.Fatalf("submission automatically advanced the next object: %v", err)
			}
		}
	}
	if h.w.delivery.next() != -1 || !strings.Contains(h.w.delivery.status, "readiness is separate") {
		t.Fatal("accepted inventory was conflated with workload readiness")
	}
	cm, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(h.ctx, "delivery-config", metav1.GetOptions{})
	if err != nil || cm.Data["message"] != "created-through-reviewed-delivery" {
		t.Fatalf("native ConfigMap source was not persisted: %v", err)
	}
	svc, err := f.Client.CoreV1().Services(f.Namespace).Get(h.ctx, "delivery-service", metav1.GetOptions{})
	if err != nil || svc.UID != createdUIDs["delivery-service"] || svc.Spec.ClusterIP == "" || svc.Spec.Selector["app"] != "delivery-workload" || len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 8080 {
		t.Fatalf("native Service source/defaults were not persisted: %v", err)
	}
	deployment, err := f.Client.AppsV1().Deployments(f.Namespace).Get(h.ctx, "delivery-workload", metav1.GetOptions{})
	if err != nil || deployment.UID != createdUIDs["delivery-workload"] || deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 0 || len(deployment.Spec.Template.Spec.Containers) != 1 || deployment.Spec.Template.Spec.Containers[0].Image != testcluster.Image {
		t.Fatalf("native Deployment source was not persisted: %v", err)
	}
	h.click("Delivery outcomes")
	if strings.Count(h.w.delivery.outcomeText(), " · Succeeded") != 6 {
		t.Fatal("native outcomes omitted accepted objects or their individual attempts")
	}
	saveNativeScreenshot(t, h.tt)
	// Inspect uses the accepted UID and the existing Health view. This fixture
	// has zero replicas, so neither a running application nor traffic is claimed.
	h.click("3. delivery-workload")
	h.click("Inspect accepted resource")
	h.pump(func() bool { return h.w.detail != nil })
	if h.w.detail.GetUID() != createdUIDs["delivery-workload"] || h.w.detailKind.GVR != (schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}) || h.w.delivery.open {
		t.Fatal("inspection did not pin the accepted Deployment")
	}
	h.click("Health")
	h.pump(func() bool { return h.w.healthText != "" && h.w.healthText != "Loading current status…" })
	if h.w.detail.GetUID() != createdUIDs["delivery-workload"] || h.w.detailMode != "Health" || strings.HasPrefix(h.w.healthText, "Health unavailable:") {
		t.Fatal("existing health inspection escaped the accepted UID")
	}
	h.click("Deliver manifests")
	if h.w.delivery.set == nil || h.w.delivery.set.ID() != setID || h.w.delivery.outcomes[2].UID != createdUIDs["delivery-workload"] {
		t.Fatal("resource inspection lost the paused delivery outcomes")
	}
	h.click("Change manifests")
	// Existing SSA manages a new field, then revises that same owned field. The
	// separate backend case requires a conflict for changing POST-owned data.
	for _, revision := range []string{"native-first-review", "native-second-review"} {
		source := []byte(fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: delivery-config\ndata:\n  reviewed: %s\n", revision))
		importSource(source, 1)
		before, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(h.ctx, "delivery-config", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		h.click("I understand omitted fields may be removed")
		p := preview("delivery-config", "apply")
		if p.Target().UID != before.UID || !strings.Contains(h.w.delivery.diff, "FIELD OWNERSHIP") || !strings.Contains(h.w.delivery.diff, operation.ApplyFieldManager) {
			t.Fatal("native existing-object preview omitted UID or field ownership")
		}
		still, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(h.ctx, "delivery-config", metav1.GetOptions{})
		if err != nil || still.ResourceVersion != before.ResourceVersion || still.Data["reviewed"] != before.Data["reviewed"] {
			t.Fatalf("native apply preview changed the live object: %v", err)
		}
		uid := confirmAndSubmit("delivery-config", 0)
		actual, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(h.ctx, "delivery-config", metav1.GetOptions{})
		if err != nil || actual.UID != createdUIDs["delivery-config"] || actual.UID != uid || actual.Data["reviewed"] != revision || actual.Data["message"] != "created-through-reviewed-delivery" {
			t.Fatalf("native existing SSA lost the actual incarnation or foreign field: %v", err)
		}
		h.click("Delivery outcomes")
		if h.w.delivery.outcomes[0].Attempts[0].Kind != "apply" {
			t.Fatal("existing delivery was not recorded as an explicit apply")
		}
		h.click("Change manifests")
	}
	h.click("Disconnect")
	h.pump(func() bool { return h.w.backend == nil && h.w.pendingWrites == 0 })
	if h.w.delivery.open || h.w.delivery.set != nil || h.w.delivery.plan != nil || h.w.delivery.editor != nil || h.w.detail != nil {
		t.Fatal("disconnect retained delivery input, approval or resource detail")
	}
}
