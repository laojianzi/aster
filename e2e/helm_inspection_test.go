//go:build e2e

package e2e

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync/atomic"
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

type helmInspectionReadTransport struct {
	base               http.RoundTripper
	namespace, storage string
	reads, rejected    atomic.Int32
}

func (r *helmInspectionReadTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	prefix := "/api/v1/namespaces/" + r.namespace + "/secrets"
	if req.Method != "GET" || !(req.URL.Path == prefix && req.Header.Get("Accept") == helmrelease.Accept || req.URL.Path == prefix+"/"+r.storage && req.Header.Get("Accept") == "application/json") {
		r.rejected.Add(1)
		return nil, errors.New("fixture blocked a non-inspection request")
	}
	if req.URL.Path == prefix+"/"+r.storage {
		r.reads.Add(1)
	}
	return r.base.RoundTrip(req)
}
func TestRealHelmInspectionReadOnlyIdentityAndRBAC(t *testing.T) {
	f := testcluster.NewHelmInspection(t)
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
		t.Fatal("inventory", err)
	}
	row := snapshot.Revisions[0]
	if row.UID != string(f.Secrets[0].UID) || row.ResourceVersion != f.Secrets[0].ResourceVersion {
		t.Fatal("object identity omitted")
	}
	if _, err := b.InspectHelmRelease(ctx, f.Namespace, row, false); !errors.Is(err, helmrelease.ErrInspectionConsent) || observed.reads.Load() != 0 {
		t.Fatal("unconsented read", err)
	}
	summary, err := b.InspectHelmRelease(ctx, f.Namespace, row, true)
	if err != nil || summary.ChartName != "example-chart" || summary.HookCount != 1 || summary.SuppliedValues != 2 || observed.reads.Load() != 1 || observed.rejected.Load() != 0 {
		t.Fatal("selected read", err)
	}
	after, err := f.Client.CoreV1().Secrets(f.Namespace).Get(ctx, row.StorageName, metav1.GetOptions{})
	if err != nil || after.UID != f.Secrets[0].UID || after.ResourceVersion != f.Secrets[0].ResourceVersion || !reflect.DeepEqual(after.Data, f.Secrets[0].Data) {
		t.Fatal("inspection changed storage", err)
	}
	// LIST authorization must not become GET authorization. The test administrator
	// creates fixture-only RBAC; the production inspector never grants permissions.
	user := "aster-inspection-list-only-" + f.Namespace
	_, err = f.Client.RbacV1().Roles(f.Namespace).Create(ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "inspection-list-only"}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"list"}}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Client.RbacV1().RoleBindings(f.Namespace).Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "inspection-list-only"}, Subjects: []rbacv1.Subject{{Kind: "User", Name: user, APIGroup: rbacv1.GroupName}}, RoleRef: rbacv1.RoleRef{Kind: "Role", Name: "inspection-list-only", APIGroup: rbacv1.GroupName}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cfg2 := rest.CopyConfig(f.Config)
	cfg2.Impersonate = rest.ImpersonationConfig{UserName: user}
	listOnly, err := kube.New(cfg2)
	if err != nil {
		t.Fatal(err)
	}
	err = wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 10*time.Second, true, func(context.Context) (bool, error) {
		s, e := listOnly.HelmReleaseMetadata(ctx, f.Namespace)
		return e == nil && len(s.Revisions) == 1, nil
	})
	if err != nil {
		t.Fatal("fixture RBAC not observed", err)
	}
	if out, err := listOnly.InspectHelmRelease(ctx, f.Namespace, row, true); !errors.Is(err, helmrelease.ErrInspectionForbidden) || out != (helmrelease.Inspection{}) {
		t.Fatal("LIST identity acquired GET content", err)
	}
	// A concurrent metadata edit invalidates the precise object version.
	changed := after.DeepCopy()
	changed.Annotations = map[string]string{"changed": "fixture"}
	changed, err = f.Client.CoreV1().Secrets(f.Namespace).Update(ctx, changed, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := b.InspectHelmRelease(ctx, f.Namespace, row, true); !errors.Is(err, helmrelease.ErrInspectionStale) || out != (helmrelease.Inspection{}) {
		t.Fatal("old version admitted", err)
	}
	fresh, err := b.HelmReleaseMetadata(ctx, f.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.InspectHelmRelease(ctx, f.Namespace, fresh.Revisions[0], true); err != nil {
		t.Fatal("fresh version rejected", err)
	}
	// Deletion/recreation of the same canonical key must not inherit old consent.
	if err := f.Client.CoreV1().Secrets(f.Namespace).Delete(ctx, row.StorageName, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	replacement := changed.DeepCopy()
	replacement.UID = ""
	replacement.ResourceVersion = ""
	replacement.ManagedFields = nil
	replacement.CreationTimestamp = metav1.Time{}
	replacement, err = f.Client.CoreV1().Secrets(f.Namespace).Create(ctx, replacement, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.UID == changed.UID {
		t.Fatal("fixture did not recreate")
	}
	if out, err := b.InspectHelmRelease(ctx, f.Namespace, fresh.Revisions[0], true); !errors.Is(err, helmrelease.ErrInspectionStale) || out != (helmrelease.Inspection{}) {
		t.Fatal("recreated object admitted", err)
	}
	if observed.rejected.Load() != 0 {
		t.Fatal("inspector attempted a write or unrelated read")
	}
}
