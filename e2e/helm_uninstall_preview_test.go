//go:build e2e

package e2e

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/testcluster"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
)

func TestRealHelmUninstallPreviewReadOnlyAndStaleIdentity(t *testing.T) {
	f := testcluster.NewHelmUninstallPreview(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cfg := rest.CopyConfig(f.Config)
	observed := &helmInspectionReadTransport{namespace: f.Namespace, storage: f.Secrets[0].Name}
	cfg.Wrap(func(base http.RoundTripper) http.RoundTripper { observed.base = base; return observed })
	b, err := kube.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := b.HelmReleaseMetadata(ctx, f.Namespace)
	if err != nil || len(snapshot.Revisions) != 1 {
		t.Fatal(err)
	}
	row := snapshot.Revisions[0]
	before, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, "preview-config", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if p, e := b.PreviewHelmUninstall(ctx, f.Namespace, row, "session", false); p != nil || !errors.Is(e, helmrelease.ErrInspectionConsent) || observed.reads.Load() != 0 {
		t.Fatal("unconsented GET", e)
	}
	p, err := b.PreviewHelmUninstall(ctx, f.Namespace, row, "session", true)
	if err != nil || len(p.Items()) != 2 || !p.Items()[0].Keep || p.Summary().HookCount != 1 || !p.Matches("session", f.Namespace, row, time.Now()) || observed.reads.Load() != 1 {
		t.Fatal("preview", err)
	}
	after, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, "preview-config", metav1.GetOptions{})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("live canary changed", err)
	}
	secret, err := f.Client.CoreV1().Secrets(f.Namespace).Get(ctx, row.StorageName, metav1.GetOptions{})
	if err != nil || secret.UID != f.Secrets[0].UID || secret.ResourceVersion != row.ResourceVersion || !reflect.DeepEqual(secret.Data, f.Secrets[0].Data) {
		t.Fatal("storage changed", err)
	}
	// An admin-only fixture creates a list-only identity; production never grants
	// RBAC or interprets consent as get permission.
	user := "preview-list-only-" + f.Namespace
	_, err = f.Client.RbacV1().Roles(f.Namespace).Create(ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "preview-list-only"}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"list"}}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Client.RbacV1().RoleBindings(f.Namespace).Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "preview-list-only"}, Subjects: []rbacv1.Subject{{Kind: "User", Name: user, APIGroup: rbacv1.GroupName}}, RoleRef: rbacv1.RoleRef{Kind: "Role", Name: "preview-list-only", APIGroup: rbacv1.GroupName}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	restricted := rest.CopyConfig(f.Config)
	restricted.Impersonate = rest.ImpersonationConfig{UserName: user}
	limited, err := kube.New(restricted)
	if err != nil {
		t.Fatal(err)
	}
	err = wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 10*time.Second, true, func(context.Context) (bool, error) {
		s, e := limited.HelmReleaseMetadata(ctx, f.Namespace)
		return e == nil && len(s.Revisions) == 1, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if p, e := limited.PreviewHelmUninstall(ctx, f.Namespace, row, "limited", true); p != nil || !errors.Is(e, helmrelease.ErrInspectionForbidden) {
		t.Fatal("LIST acquired content", e)
	}
	changed := secret.DeepCopy()
	changed.Annotations = map[string]string{"changed": "fixture"}
	changed, err = f.Client.CoreV1().Secrets(f.Namespace).Update(ctx, changed, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if p, e := b.PreviewHelmUninstall(ctx, f.Namespace, row, "session", true); p != nil || !errors.Is(e, helmrelease.ErrInspectionStale) {
		t.Fatal("stale version accepted", e)
	}
	fresh, err := b.HelmReleaseMetadata(ctx, f.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Client.CoreV1().Secrets(f.Namespace).Delete(ctx, row.StorageName, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	changed.UID = ""
	changed.ResourceVersion = ""
	changed.ManagedFields = nil
	changed.CreationTimestamp = metav1.Time{}
	changed, err = f.Client.CoreV1().Secrets(f.Namespace).Create(ctx, changed, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if p, e := b.PreviewHelmUninstall(ctx, f.Namespace, fresh.Revisions[0], "session", true); p != nil || !errors.Is(e, helmrelease.ErrInspectionStale) {
		t.Fatal("recreated identity accepted", e)
	}
	if observed.rejected.Load() != 0 {
		t.Fatal("production attempted write or unrelated read")
	}
}
