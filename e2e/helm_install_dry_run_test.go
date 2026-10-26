//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/testcluster"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
)

type installServerDryGuard struct {
	next          http.RoundTripper
	namespace     string
	posts         *atomic.Int32
	bad           *atomic.Bool
	before, after func(int) error
}

func (g installServerDryGuard) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodGet {
		return g.next.RoundTrip(r)
	}
	q := r.URL.Query()
	prefix := "/api/v1/namespaces/" + g.namespace + "/"
	if r.Method != http.MethodPost || (r.URL.Path != prefix+"secrets" && r.URL.Path != prefix+"configmaps") ||
		q.Get("dryRun") != "All" || q.Get("fieldValidation") != "Strict" || q.Get("fieldManager") != helmrelease.InstallFieldManager || len(q) != 3 || r.GetBody != nil {
		g.bad.Store(true)
		return nil, errors.New("unexpected install mutation or replay capability")
	}
	n := int(g.posts.Add(1))
	if g.before != nil {
		if err := g.before(n); err != nil {
			return nil, err
		}
	}
	response, err := g.next.RoundTrip(r)
	if err == nil && g.after != nil {
		if hookErr := g.after(n); hookErr != nil {
			response.Body.Close()
			return nil, hookErr
		}
	}
	return response, err
}

// Only independently authorized disposable-fixture clients persist resources.
// The Backend transport is independently guarded before every request.
func TestRealHelmInstallDryRunNonPersistence(t *testing.T) {
	for _, mode := range []string{"admitted", "unacknowledged", "cancelled", "revoked", "deny-secret", "deny-target", "release-race", "target-race", "history-race", "namespace-race"} {
		t.Run(mode, func(t *testing.T) {
			f := testcluster.NewHelmMetadata(t, 0)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cms, secrets := f.Client.CoreV1().ConfigMaps(f.Namespace), f.Client.CoreV1().Secrets(f.Namespace)
			cm, err := cms.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "dry-install-canary"}, Data: map[string]string{"private": "CANARY_UNCHANGED"}}, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			secret, err := secrets.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "dry-install-secret-canary"}, Data: map[string][]byte{"private": []byte("CANARY_SECRET")}}, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			stamp := now.Add(-time.Second).UTC().Format(time.RFC3339Nano)
			raw, _ := json.Marshal(map[string]any{"name": "dry-install", "namespace": f.Namespace, "version": 1, "apply_method": "ssa",
				"info":     map[string]any{"status": "pending-install", "description": "Dry run complete", "first_deployed": stamp, "last_deployed": stamp},
				"chart":    map[string]any{"metadata": map[string]any{"name": "dry-chart", "version": "0.1.0", "apiVersion": "v2"}},
				"manifest": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: dry-install-target\ndata:\n  private: NEVER_PERSISTED\n"})
			c, err := helmrelease.DecodeInstallCapture(ctx, raw, f.Namespace, "dry-install", now)
			clear(raw)
			if err != nil {
				t.Fatal(err)
			}
			cfg := rest.CopyConfig(f.Config)
			if strings.HasPrefix(mode, "deny-") {
				user := "aster-install-" + f.Namespace
				secretVerbs := []string{"get", "list"}
				cmVerbs := []string{"get"}
				if mode == "deny-target" {
					secretVerbs = append(secretVerbs, "create")
				} else {
					cmVerbs = append(cmVerbs, "create")
				}
				_, err = f.Client.RbacV1().Roles(f.Namespace).Create(ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "install-dry-run"}, Rules: []rbacv1.PolicyRule{
					{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: secretVerbs}, {APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: cmVerbs},
				}}, metav1.CreateOptions{})
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.Client.RbacV1().RoleBindings(f.Namespace).Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "install-dry-run"}, Subjects: []rbacv1.Subject{{Kind: "User", APIGroup: rbacv1.GroupName, Name: user}}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: "install-dry-run"}}, metav1.CreateOptions{})
				if err != nil {
					t.Fatal(err)
				}
				roleName := f.Namespace + "-install-read"
				_, err = f.Client.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: roleName}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"namespaces"}, ResourceNames: []string{f.Namespace}, Verbs: []string{"get"}}}}, metav1.CreateOptions{})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
					defer stop()
					_ = f.Client.RbacV1().ClusterRoleBindings().Delete(cleanup, roleName, metav1.DeleteOptions{})
					_ = f.Client.RbacV1().ClusterRoles().Delete(cleanup, roleName, metav1.DeleteOptions{})
				})
				_, err = f.Client.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: roleName}, Subjects: []rbacv1.Subject{{Kind: "User", APIGroup: rbacv1.GroupName, Name: user}}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: roleName}}, metav1.CreateOptions{})
				if err != nil {
					t.Fatal(err)
				}
				// Wait only for fixture RBAC establishment, never retry a product request.
				err = wait.PollUntilContextTimeout(ctx, 20*time.Millisecond, 5*time.Second, true, func(ctx context.Context) (bool, error) {
					for _, a := range []authorizationv1.ResourceAttributes{{Verb: "get", Resource: "namespaces", Name: f.Namespace}, {Verb: "list", Resource: "secrets", Namespace: f.Namespace}, {Verb: "get", Resource: "configmaps", Namespace: f.Namespace}, {Verb: "get", Resource: "secrets", Namespace: f.Namespace}} {
						sar, e := f.Client.AuthorizationV1().SubjectAccessReviews().Create(ctx, &authorizationv1.SubjectAccessReview{Spec: authorizationv1.SubjectAccessReviewSpec{User: user, Groups: []string{"system:authenticated"}, ResourceAttributes: &a}}, metav1.CreateOptions{})
						if e != nil {
							return false, e
						}
						if !sar.Status.Allowed {
							return false, nil
						}
					}
					return true, nil
				})
				if err != nil {
					t.Fatal("RBAC fixture", err)
				}
				cfg.Impersonate = rest.ImpersonationConfig{UserName: user, Groups: []string{"system:authenticated"}}
			}
			var posts atomic.Int32
			var bad atomic.Bool
			var competingSecret *corev1.Secret
			var competingCM *corev1.ConfigMap
			var fixtureErr error
			cfg.Wrap(func(next http.RoundTripper) http.RoundTripper {
				return installServerDryGuard{next: next, namespace: f.Namespace, posts: &posts, bad: &bad,
					before: func(n int) error {
						if mode == "release-race" && n == 1 {
							competingSecret, fixtureErr = secrets.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: c.StorageName()}, Data: map[string][]byte{"private": []byte("DO_NOT_ADOPT")}}, metav1.CreateOptions{})
						}
						if mode == "target-race" && n == 2 {
							competingCM, fixtureErr = cms.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "dry-install-target"}, Data: map[string]string{"private": "DO_NOT_REPLACE"}}, metav1.CreateOptions{})
						}
						return fixtureErr
					}, after: func(n int) error {
						if mode == "history-race" && n == 2 {
							competingSecret, fixtureErr = secrets.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1.dry-install.v2", Labels: map[string]string{"owner": "helm", "name": "dry-install", "version": "2", "status": "failed"}}, Type: "helm.sh/release.v1", Data: map[string][]byte{"release": []byte("NEVER_READ")}}, metav1.CreateOptions{})
						}
						if mode == "namespace-race" && n == 1 {
							ns, e := f.Client.CoreV1().Namespaces().Get(ctx, f.Namespace, metav1.GetOptions{})
							if e != nil {
								return e
							}
							if ns.Annotations == nil {
								ns.Annotations = map[string]string{}
							}
							ns.Annotations["fixture-race"] = "changed"
							_, fixtureErr = f.Client.CoreV1().Namespaces().Update(ctx, ns, metav1.UpdateOptions{})
						}
						return fixtureErr
					}}
			})
			b, err := kube.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			p, err := b.PrepareHelmInstall(ctx, c, "real-install-dry", true)
			if err != nil {
				t.Fatal("preparation", err)
			}
			grant, err := b.AcknowledgeHelmInstallDryRun(ctx, p, c, "real-install-dry", mode != "unacknowledged")
			if mode == "unacknowledged" {
				if !errors.Is(err, kube.ErrHelmDryRunConsent) || grant != nil {
					t.Fatal("acknowledgement", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			opctx := ctx
			if mode == "cancelled" {
				var stop context.CancelFunc
				opctx, stop = context.WithCancel(ctx)
				stop()
			}
			if mode == "revoked" {
				grant.Discard()
			}
			report, err := b.PreflightHelmInstall(opctx, grant, c, "real-install-dry")
			if mode == "admitted" {
				if err != nil || report == nil || !report.Matches(b, c, "real-install-dry", time.Now()) || posts.Load() != 2 {
					t.Fatal("server dry-run", err)
				}
			} else if err == nil || report != nil && report.Complete() {
				t.Fatal("unsafe preflight accepted")
			}
			switch mode {
			case "deny-secret", "deny-target":
				if !errors.Is(err, helmrelease.ErrLiveForbidden) {
					t.Fatal("RBAC denial", err)
				}
			case "release-race", "target-race", "history-race":
				if !errors.Is(err, helmrelease.ErrInstallConflict) {
					t.Fatal("real conflict", err)
				}
			case "namespace-race":
				if !errors.Is(err, helmrelease.ErrInstallStale) {
					t.Fatal("namespace identity", err)
				}
			case "cancelled":
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation", err)
				}
			}
			if bad.Load() || fixtureErr != nil {
				t.Fatal("request or fixture guard", fixtureErr)
			}
			if mode == "deny-target" || mode == "target-race" {
				if report == nil || len(report.Steps()) != 2 || !report.Steps()[0].Verified || report.Steps()[1].Verified {
					t.Fatal("lost partial progress")
				}
			}
			before := posts.Load()
			_, err = b.PreflightHelmInstall(ctx, grant, c, "real-install-dry")
			if !errors.Is(err, kube.ErrHelmDryRunConsent) || posts.Load() != before {
				t.Fatal("consent replay")
			}
			checkCM := func(want *corev1.ConfigMap) {
				t.Helper()
				if want == nil {
					return
				}
				got, e := cms.Get(ctx, want.Name, metav1.GetOptions{})
				if e != nil || !reflect.DeepEqual(got, want) {
					t.Fatal("ConfigMap changed", e)
				}
			}
			checkSecret := func(want *corev1.Secret) {
				t.Helper()
				if want == nil {
					return
				}
				got, e := secrets.Get(ctx, want.Name, metav1.GetOptions{})
				if e != nil || !reflect.DeepEqual(got, want) {
					t.Fatal("Secret changed", e)
				}
			}
			checkCM(cm)
			checkCM(competingCM)
			checkSecret(secret)
			checkSecret(competingSecret)
			absent := func(err error, resource, name string) {
				t.Helper()
				var s *apierrors.StatusError
				if !errors.As(err, &s) || !apierrors.IsNotFound(err) || s.ErrStatus.Code != 404 || s.ErrStatus.Reason != metav1.StatusReasonNotFound || s.ErrStatus.Details == nil || s.ErrStatus.Details.Kind != resource || s.ErrStatus.Details.Group != "" || s.ErrStatus.Details.Name != name {
					t.Fatal(fmt.Sprintf("exact %s absence unproven", resource))
				}
			}
			if competingCM == nil {
				_, e := cms.Get(ctx, "dry-install-target", metav1.GetOptions{})
				absent(e, "configmaps", "dry-install-target")
			}
			if competingSecret == nil || competingSecret.Name != c.StorageName() {
				_, e := secrets.Get(ctx, c.StorageName(), metav1.GetOptions{})
				absent(e, "secrets", c.StorageName())
			}
		})
	}
}
