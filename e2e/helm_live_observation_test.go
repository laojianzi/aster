//go:build e2e

package e2e

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmfixture"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/testcluster"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
)

type liveObservationTransport struct {
	base         http.RoundTripper
	ns, storage  string
	live         atomic.Bool
	bad, objects atomic.Int32
}

func (a *liveObservationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	allowed := false
	if r.Method == "GET" {
		switch r.URL.Path {
		case "/api/v1":
			allowed = r.Header.Get("Accept") == "application/json"
		case "/api/v1/namespaces/" + a.ns + "/secrets":
			allowed = r.Header.Get("Accept") == helmrelease.Accept && r.URL.Query().Get("labelSelector") == "owner=helm"
		case "/api/v1/namespaces/" + a.ns + "/secrets/" + a.storage:
			allowed = !a.live.Load()
		case "/api/v1/namespaces/" + a.ns + "/configmaps/preview-config", "/api/v1/namespaces/" + a.ns + "/secrets/preview-secret":
			allowed = r.Header.Get("Accept") == "application/json;as=PartialObjectMetadata;g=meta.k8s.io;v=v1"
			a.objects.Add(1)
		}
	}
	if !allowed {
		a.bad.Add(1)
		return nil, errors.New("unapproved test request")
	}
	return a.base.RoundTrip(r)
}
func TestRealHelmLiveObservationMetadataAndIdentity(t *testing.T) {
	f := testcluster.NewHelmUninstallPreview(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg := rest.CopyConfig(f.Config)
	audit := &liveObservationTransport{ns: f.Namespace, storage: f.Secrets[0].Name}
	cfg.Wrap(func(base http.RoundTripper) http.RoundTripper { audit.base = base; return audit })
	b, e := kube.New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	s, e := b.HelmReleaseMetadata(ctx, f.Namespace)
	if e != nil || len(s.Revisions) != 1 {
		t.Fatal(e)
	}
	p, e := b.PreviewHelmUninstall(ctx, f.Namespace, s.Revisions[0], "session", true)
	if e != nil {
		t.Fatal(e)
	}
	audit.live.Store(true)
	before, e := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, "preview-config", metav1.GetOptions{})
	if e != nil {
		t.Fatal(e)
	}
	o, e := b.ObserveHelmPreview(ctx, p, "session", true)
	if e != nil || o == nil || len(o.Items()) != 2 {
		t.Fatal("live observation", e)
	}
	rows := o.Items()
	if rows[0].State != "present" || rows[0].Markers != "missing" || rows[0].UID != string(before.UID) || rows[0].ResourceVersion != before.ResourceVersion || rows[1].State != "absent" {
		t.Fatal("projection", rows)
	}
	after, e := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, "preview-config", metav1.GetOptions{})
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("canary changed", e)
	}
	// Admin-only setup changes markers; the production check must observe but never
	// repair them, and its previous immutable result must not change.
	after.Labels = map[string]string{"app.kubernetes.io/managed-by": "Helm"}
	after.Annotations = map[string]string{"meta.helm.sh/release-name": "example", "meta.helm.sh/release-namespace": f.Namespace}
	after, e = f.Client.CoreV1().ConfigMaps(f.Namespace).Update(ctx, after, metav1.UpdateOptions{})
	if e != nil {
		t.Fatal(e)
	}
	matched, e := b.ObserveHelmPreview(ctx, p, "session", true)
	if e != nil || matched.Items()[0].Markers != "match" || o.Items()[0].Markers != "missing" {
		t.Fatal("marker sample", e)
	}
	// An object recreated before a new GET gets a new sampled UID; this does NOT
	// prove identity with the original Helm-installed object.
	if e = f.Client.CoreV1().ConfigMaps(f.Namespace).Delete(ctx, after.Name, metav1.DeleteOptions{}); e != nil {
		t.Fatal(e)
	}
	recreated := after.DeepCopy()
	recreated.UID = ""
	recreated.ResourceVersion = ""
	recreated.ManagedFields = nil
	recreated.CreationTimestamp = metav1.Time{}
	recreated, e = f.Client.CoreV1().ConfigMaps(f.Namespace).Create(ctx, recreated, metav1.CreateOptions{})
	if e != nil {
		t.Fatal(e)
	}
	changed, e := b.ObserveHelmPreview(ctx, p, "session", true)
	if e != nil || changed.Items()[0].UID != string(recreated.UID) || changed.Items()[0].UID == matched.Items()[0].UID {
		t.Fatal("recreated live identity", e)
	}
	secret, e := f.Client.CoreV1().Secrets(f.Namespace).Get(ctx, p.Revision().StorageName, metav1.GetOptions{})
	if e != nil || !reflect.DeepEqual(secret.Data, f.Secrets[0].Data) || secret.ResourceVersion != p.Revision().ResourceVersion {
		t.Fatal("storage changed", e)
	}
	// Independent restricted identity can list/get Secrets but NOT get ConfigMaps.
	user := "live-observer-" + f.Namespace
	_, e = f.Client.RbacV1().Roles(f.Namespace).Create(ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "live-observer"}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get", "list"}}}}, metav1.CreateOptions{})
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.Client.RbacV1().RoleBindings(f.Namespace).Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "live-observer"}, Subjects: []rbacv1.Subject{{Kind: "User", Name: user, APIGroup: rbacv1.GroupName}}, RoleRef: rbacv1.RoleRef{Kind: "Role", Name: "live-observer", APIGroup: rbacv1.GroupName}}, metav1.CreateOptions{})
	if e != nil {
		t.Fatal(e)
	}
	limitedCfg := rest.CopyConfig(f.Config)
	limitedCfg.Impersonate = rest.ImpersonationConfig{UserName: user}
	limited, e := kube.New(limitedCfg)
	if e != nil {
		t.Fatal(e)
	}
	e = wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 10*time.Second, true, func(context.Context) (bool, error) {
		_, e := limited.HelmReleaseMetadata(ctx, f.Namespace)
		return e == nil, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if x, e := limited.ObserveHelmPreview(ctx, p, "session", true); x != nil || !errors.Is(e, helmrelease.ErrLiveForbidden) {
		t.Fatal("metadata read permission boundary", e)
	}
	newer := helmfixture.Secret(f.Namespace, "example", "pending-upgrade", 2)
	newer.UID = ""
	newer.ResourceVersion = ""
	_, e = f.Client.CoreV1().Secrets(f.Namespace).Create(ctx, newer, metav1.CreateOptions{})
	if e != nil {
		t.Fatal(e)
	}
	n := audit.objects.Load()
	if x, e := b.ObserveHelmPreview(ctx, p, "session", true); x != nil || !errors.Is(e, helmrelease.ErrLiveStale) || audit.objects.Load() != n {
		t.Fatal("new revision not rejected before live reads", e)
	}
	if audit.bad.Load() != 0 {
		t.Fatal("write/full object/unapproved request")
	}
	if strings.Contains(o.Items()[0].Markers, "PRIVATE") {
		t.Fatal("private metadata projected")
	}
}
