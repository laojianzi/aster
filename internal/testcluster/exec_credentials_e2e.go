//go:build e2e

package testcluster

import (
	"context"
	"testing"
	"time"

	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// ReadOnlyToken is scoped to this fixture's Namespace and deliberately cannot
// mutate resources or open exec sessions. It is used only by disposable E2E.
func ReadOnlyToken(t *testing.T, f Fixture) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const name = "aster-credential-reader"
	if _, err := f.Client.CoreV1().ServiceAccounts(f.Pod.Namespace).Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err := f.Client.RbacV1().Roles(f.Pod.Namespace).Create(ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: name}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"pods", "pods/log"}, Verbs: []string{"get", "list", "watch"}}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Client.RbacV1().RoleBindings(f.Pod.Namespace).Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name}, Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: name, Namespace: f.Pod.Namespace}}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: name}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	seconds := int64(600)
	request, err := f.Client.CoreV1().ServiceAccounts(f.Pod.Namespace).CreateToken(ctx, name, &authv1.TokenRequest{Spec: authv1.TokenRequestSpec{ExpirationSeconds: &seconds}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := rest.AnonymousClientConfig(f.Config)
	cfg.BearerToken = request.Status.Token
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal("cannot create reader client")
	}
	for {
		_, err = client.CoreV1().Pods(f.Pod.Namespace).Get(ctx, f.Pod.Name, metav1.GetOptions{})
		if err == nil {
			return request.Status.Token
		}
		select {
		case <-ctx.Done():
			t.Fatal("fixture authorization did not propagate")
		case <-time.After(30 * time.Millisecond):
		}
	}
}
