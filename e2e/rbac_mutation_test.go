//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/testcluster"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func TestNamespaceOnlyRBAC(t *testing.T) {
	cfg := testcluster.Config(t)
	admin, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	const ns = "aster-rbac-e2e"
	_, err = admin.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = admin.CoreV1().Namespaces().Delete(bg, ns, metav1.DeleteOptions{})
	})

	_, err = admin.CoreV1().ServiceAccounts(ns).Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "viewer"}}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	_, err = admin.RbacV1().Roles(ns).Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-reader"},
		Rules:      []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list", "watch"}}},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	_, err = admin.RbacV1().RoleBindings(ns).Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-reader"},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "viewer", Namespace: ns}},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "pod-reader"},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}

	restrictedCfg := *cfg
	restrictedCfg.Impersonate.UserName = "system:serviceaccount:" + ns + ":viewer"
	restricted, err := kubernetes.NewForConfig(&restrictedCfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restricted.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1}); !apierrors.IsForbidden(err) {
		t.Fatalf("namespace list error = %v, want forbidden", err)
	}
	if _, err := restricted.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{Limit: 1}); err != nil {
		t.Fatalf("authorized pod list failed: %v", err)
	}
}

func TestServerDryRunDoesNotPersist(t *testing.T) {
	cfg := testcluster.Config(t)
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const name = "aster-dry-run-e2e"
	obj, err := client.CoreV1().ConfigMaps("default").Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Data:       map[string]string{"validated": "true"},
	}, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
	if err != nil {
		t.Fatal(err)
	}
	if obj.Name != name {
		t.Fatalf("dry-run returned name %q", obj.Name)
	}
	_, err = client.CoreV1().ConfigMaps("default").Get(ctx, name, metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("GET after dry-run = %v, want not found", err)
	}
}
