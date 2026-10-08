//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/resource"
	"github.com/laojianzi/aster/internal/resourcemetrics"
	"github.com/laojianzi/aster/internal/testcluster"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

func TestRealMetricsPodNodeAndIndependentRBAC(t *testing.T) {
	f := testcluster.NewHTTPPod(t)
	b, err := kube.New(f.Config)
	if err != nil {
		t.Fatal(err)
	}
	got := testcluster.AwaitMetrics(t, b, f.Target("metrics-test"))
	if len(got.Entries) != 1 || got.Entries[0].Name != "http" {
		t.Fatalf("wrong container metrics: %+v", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	node, err := f.Client.CoreV1().Nodes().Get(ctx, f.Pod.Spec.NodeName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	target := resource.Identity{SessionID: "metrics-test", GVR: schema.GroupVersionResource{Version: "v1", Resource: "nodes"}, Name: node.Name, UID: node.UID}
	testcluster.AwaitMetrics(t, b, target)
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "pod-only"}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"pods"}, ResourceNames: []string{f.Pod.Name}, Verbs: []string{"get"}}}}
	role, err = f.Client.RbacV1().Roles(f.Pod.Namespace).Create(ctx, role, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	user := "aster-metrics-" + f.Pod.Namespace
	_, err = f.Client.RbacV1().RoleBindings(f.Pod.Namespace).Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "pod-only"}, RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: role.Name}, Subjects: []rbacv1.Subject{{Kind: "User", Name: user, APIGroup: "rbac.authorization.k8s.io"}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := rest.CopyConfig(f.Config)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: user, Groups: []string{"system:authenticated"}}
	limited, err := kube.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Independently prove the core resource is readable before attributing a
	// rejection to metrics RBAC. A not-yet-propagated Pod role is not evidence.
	podTarget := f.Target("restricted")
	for {
		obj, getErr := limited.GetObject(ctx, podTarget.GVR, podTarget.Namespace, podTarget.Name)
		if getErr == nil && obj.GetUID() == podTarget.UID {
			break
		}
		if !apierrors.IsForbidden(getErr) {
			t.Fatalf("core Pod identity prerequisite: %v", getErr)
		}
		select {
		case <-ctx.Done():
			t.Fatal("core Pod grant did not propagate")
		case <-time.After(100 * time.Millisecond):
		}
	}
	// Granting core/v1 Pod access must NOT imply metrics.k8s.io access.
	denied := resourcemetrics.Read(ctx, limited, f.Target("restricted"))
	if denied.State != resourcemetrics.Forbidden || len(denied.Entries) != 0 {
		t.Fatalf("metrics RBAC leaked: %+v", denied)
	}
	role.Rules = append(role.Rules, rbacv1.PolicyRule{APIGroups: []string{"metrics.k8s.io"}, Resources: []string{"pods"}, ResourceNames: []string{f.Pod.Name}, Verbs: []string{"get"}})
	if _, err = f.Client.RbacV1().Roles(f.Pod.Namespace).Update(ctx, role, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	// Short bounded retry accommodates API-server RBAC propagation only.
	for {
		got = resourcemetrics.Read(ctx, limited, f.Target("restricted"))
		if got.State == resourcemetrics.Ready {
			break
		}
		if got.State != resourcemetrics.Forbidden {
			t.Fatalf("unexpected state after grant: %+v", got)
		}
		select {
		case <-ctx.Done():
			t.Fatal("RBAC grant did not propagate")
		case <-time.After(100 * time.Millisecond):
		}
	}
	target.SessionID = "restricted"
	if resourcemetrics.Read(ctx, limited, target).State != resourcemetrics.Forbidden {
		t.Fatal("namespaced metrics role allowed node access")
	}
}
