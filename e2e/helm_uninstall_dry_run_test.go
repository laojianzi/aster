//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmfixture"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/testcluster"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
)

// Persistent operations are isolated fixture setup/changes/teardown only.
// Product preflight must leave exact storage and both ConfigMaps unchanged.
func TestRealHelmUninstallDryRunNonPersistence(t *testing.T) {
	f := testcluster.NewHelmMetadata(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	payload, err := json.Marshal(map[string]any{"name": "example", "namespace": f.Namespace, "version": 1, "info": map[string]any{"status": "deployed"},
		"chart":    map[string]any{"metadata": map[string]any{"name": "example-chart", "version": "1.2.3", "apiVersion": "v2"}},
		"manifest": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: dry-run-target\ndata:\n  private: PRIVATE_STORED\n", "config": map[string]any{"private": "PRIVATE_VALUE"}})
	if err != nil {
		t.Fatal(err)
	}
	secrets := f.Client.CoreV1().Secrets(f.Namespace)
	configs := f.Client.CoreV1().ConfigMaps(f.Namespace)
	s := f.Secrets[0].DeepCopy()
	s.Data = map[string][]byte{"release": helmfixture.Stored(payload)}
	clear(payload)
	s, err = secrets.Update(ctx, s, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cm, err := configs.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "dry-run-target", Labels: map[string]string{"app.kubernetes.io/managed-by": "Helm"},
		Annotations: map[string]string{"meta.helm.sh/release-name": "example", "meta.helm.sh/release-namespace": f.Namespace}}, Data: map[string]string{"private": "PRIVATE_LIVE"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	canary, err := configs.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "dry-run-canary"}, Data: map[string]string{"untouched": "canary"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := kube.New(f.Config)
	if err != nil {
		t.Fatal(err)
	}
	check := func(t *testing.T) {
		t.Helper()
		got, e := secrets.Get(ctx, s.Name, metav1.GetOptions{})
		target, te := configs.Get(ctx, cm.Name, metav1.GetOptions{})
		other, oe := configs.Get(ctx, canary.Name, metav1.GetOptions{})
		if e != nil || te != nil || oe != nil || !reflect.DeepEqual(s, got) || !reflect.DeepEqual(cm, target) || !reflect.DeepEqual(canary, other) {
			t.Fatal("preflight persisted or changed fixture", e, te, oe)
		}
	}
	prepare := func(t *testing.T, backend *kube.Backend) (*kube.HelmUninstallPreparation, helmrelease.Revision) {
		t.Helper()
		snapshot, e := backend.HelmReleaseMetadata(ctx, f.Namespace)
		if e != nil || len(snapshot.Revisions) != 1 {
			t.Fatal("metadata", e)
		}
		row := snapshot.Revisions[0]
		p, e := backend.PrepareHelmUninstall(ctx, f.Namespace, row, "dry-run-session", true)
		if e != nil {
			t.Fatal("preparation", e)
		}
		return p, row
	}
	t.Run("admitted-and-replay-denied", func(t *testing.T) {
		p, row := prepare(t, b)
		c, e := b.AcknowledgeHelmUninstallDryRun(p, "dry-run-session", f.Namespace, row, true)
		if e != nil {
			t.Fatal(e)
		}
		r, e := b.PreflightHelmUninstall(ctx, c, "dry-run-session", f.Namespace, row)
		if e != nil || r == nil || !r.Matches(b, "dry-run-session", f.Namespace, row, time.Now()) {
			t.Fatal("server preflight", e)
		}
		check(t)
		if out, e := b.PreflightHelmUninstall(ctx, c, "dry-run-session", f.Namespace, row); !errors.Is(e, kube.ErrHelmDryRunConsent) || out != nil {
			t.Fatal("replay", e)
		}
		check(t)
	})
	for _, mode := range []string{"deny-patch", "deny-delete"} {
		t.Run(mode, func(t *testing.T) {
			user := "aster-dry-run-" + mode + "-" + f.Namespace
			verbs := []string{"get", "list"}
			if mode == "deny-delete" {
				verbs = append(verbs, "patch")
			}
			_, e := f.Client.RbacV1().Roles(f.Namespace).Create(ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: mode}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: verbs}, {APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"get"}}}}, metav1.CreateOptions{})
			if e != nil {
				t.Fatal(e)
			}
			_, e = f.Client.RbacV1().RoleBindings(f.Namespace).Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: mode}, Subjects: []rbacv1.Subject{{Kind: "User", APIGroup: rbacv1.GroupName, Name: user}}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: mode}}, metav1.CreateOptions{})
			if e != nil {
				t.Fatal(e)
			}
			cfg := rest.CopyConfig(f.Config)
			cfg.Impersonate = rest.ImpersonationConfig{UserName: user, Groups: []string{"system:authenticated"}}
			limited, e := kube.New(cfg)
			if e != nil {
				t.Fatal(e)
			}
			p, row := prepare(t, limited)
			c, e := limited.AcknowledgeHelmUninstallDryRun(p, "dry-run-session", f.Namespace, row, true)
			if e != nil {
				t.Fatal(e)
			}
			r, e := limited.PreflightHelmUninstall(ctx, c, "dry-run-session", f.Namespace, row)
			if !errors.Is(e, helmrelease.ErrLiveForbidden) || r != nil {
				t.Fatal("dry-run permission not enforced", e)
			}
			check(t)
		})
	}
	for _, mode := range []string{"uid", "rv"} {
		t.Run("server-precondition-"+mode, func(t *testing.T) {
			uid, rv := cm.UID, cm.ResourceVersion
			if mode == "uid" {
				uid = types.UID("different-uid")
			} else {
				rv = "0"
			}
			policy := metav1.DeletePropagationOrphan
			e := configs.Delete(ctx, cm.Name, metav1.DeleteOptions{DryRun: []string{metav1.DryRunAll}, PropagationPolicy: &policy, Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}})
			if !apierrors.IsConflict(e) && !apierrors.IsInvalid(e) {
				t.Fatal("server ignored condition", e)
			}
			check(t)
		})
	}
	t.Run("changed-target", func(t *testing.T) {
		p, row := prepare(t, b)
		c, e := b.AcknowledgeHelmUninstallDryRun(p, "dry-run-session", f.Namespace, row, true)
		if e != nil {
			t.Fatal(e)
		}
		updated := cm.DeepCopy()
		updated.Annotations["concurrent"] = "fixture"
		cm, e = configs.Update(ctx, updated, metav1.UpdateOptions{})
		if e != nil {
			t.Fatal(e)
		}
		if r, e := b.PreflightHelmUninstall(ctx, c, "dry-run-session", f.Namespace, row); e == nil || r != nil {
			t.Fatal("changed target admitted")
		}
		check(t)
	})
	t.Run("changed-storage", func(t *testing.T) {
		p, row := prepare(t, b)
		c, e := b.AcknowledgeHelmUninstallDryRun(p, "dry-run-session", f.Namespace, row, true)
		if e != nil {
			t.Fatal(e)
		}
		updated := s.DeepCopy()
		updated.Annotations = map[string]string{"concurrent": "fixture"}
		s, e = secrets.Update(ctx, updated, metav1.UpdateOptions{})
		if e != nil {
			t.Fatal(e)
		}
		if r, e := b.PreflightHelmUninstall(ctx, c, "dry-run-session", f.Namespace, row); e == nil || r != nil {
			t.Fatal("changed source admitted")
		}
		check(t)
	})
}
