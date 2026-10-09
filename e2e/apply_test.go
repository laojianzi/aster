//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/operation"
	"github.com/laojianzi/aster/internal/resource"
	"github.com/laojianzi/aster/internal/testcluster"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type applyFixture struct {
	cfg    *rest.Config
	ctx    context.Context
	admin  kubernetes.Interface
	b      *kube.Backend
	target resource.Identity
}

func newApplyFixture(t *testing.T) *applyFixture {
	t.Helper()
	cfg := testcluster.Config(t)
	admin, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := kube.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	ns, err := admin.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "aster-apply-"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = admin.CoreV1().Namespaces().Delete(ctx, ns.Name, metav1.DeleteOptions{})
	})
	cm, err := admin.CoreV1().ConfigMaps(ns.Name).Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "reviewed"}, Data: map[string]string{"foreign": "controller-value"}}, metav1.CreateOptions{FieldManager: "fixture-owner"})
	if err != nil {
		t.Fatal(err)
	}
	return &applyFixture{cfg, ctx, admin, b, resource.Identity{SessionID: "apply-e2e", GVR: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, Namespace: ns.Name, Name: cm.Name, UID: cm.UID}}
}
func (f *applyFixture) intent(t *testing.T, data map[string]string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]string{"name": f.target.Name, "namespace": f.target.Namespace}, "data": data})
	if err != nil {
		t.Fatal(err)
	}
	return body
}
func (f *applyFixture) read(t *testing.T) *corev1.ConfigMap {
	t.Helper()
	cm, err := f.admin.CoreV1().ConfigMaps(f.target.Namespace).Get(f.ctx, f.target.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return cm
}
func TestRealApplyOwnershipDryRunOmissionAndConflict(t *testing.T) {
	f := newApplyFixture(t)
	s := operation.NewService(f.b, f.target.SessionID)
	before := f.read(t)
	plan, err := s.PrepareApply(f.ctx, f.target, f.intent(t, map[string]string{"managed": "first", "obsolete": "remove-next"}))
	if err != nil {
		t.Fatal(err)
	}
	previewBefore, previewAfter := plan.Preview()
	review, err := manifest.OwnershipReview(previewBefore, previewAfter)
	if err != nil || !strings.Contains(review, operation.ApplyFieldManager) {
		t.Fatalf("ownership preview absent: %s %v", review, err)
	}
	if got := f.read(t); got.ResourceVersion != before.ResourceVersion || len(got.Data) != 1 {
		t.Fatal("dry-run changed values or ownership")
	}
	r, err := s.Execute(f.ctx, plan)
	if err != nil || r.State != "Succeeded" {
		t.Fatalf("apply: %+v %v", r, err)
	}
	current := f.read(t)
	if current.Data["foreign"] != "controller-value" || current.Data["managed"] != "first" || current.Data["obsolete"] != "remove-next" {
		t.Fatal(current.Data)
	}
	owned := false
	for _, m := range current.ManagedFields {
		if m.Manager == operation.ApplyFieldManager && m.Operation == metav1.ManagedFieldsOperationApply {
			owned = true
		}
	}
	if !owned {
		t.Fatal("API did not record SSA ownership")
	}
	// Omission of our previously managed field must be visible BEFORE execution.
	plan, err = s.PrepareApply(f.ctx, f.target, f.intent(t, map[string]string{"managed": "second"}))
	if err != nil {
		t.Fatal(err)
	}
	a, b := plan.Preview()
	changes, err := manifest.Compare(a, b)
	if err != nil || !strings.Contains(manifest.Summary(changes), "obsolete") || !strings.Contains(manifest.Summary(changes), "<absent>") {
		t.Fatalf("omission not reviewed: %v %v", changes, err)
	}
	if f.read(t).Data["obsolete"] != "remove-next" {
		t.Fatal("omission dry-run persisted")
	}
	if _, err = s.Execute(f.ctx, plan); err != nil {
		t.Fatal(err)
	}
	current = f.read(t)
	if _, ok := current.Data["obsolete"]; ok || current.Data["foreign"] != "controller-value" || current.Data["managed"] != "second" {
		t.Fatal("ownership omission affected foreign field")
	}
	oldRV := current.ResourceVersion
	if p, err := s.PrepareApply(f.ctx, f.target, f.intent(t, map[string]string{"foreign": "steal"})); p != nil || !apierrors.IsConflict(err) {
		t.Fatalf("did not surface ownership conflict: %v", err)
	}
	if current = f.read(t); current.ResourceVersion != oldRV || current.Data["foreign"] != "controller-value" {
		t.Fatal("field conflict was forced or persisted")
	}
	// Same manager is not a substitute for optimistic concurrency.
	plan, err = s.PrepareApply(f.ctx, f.target, f.intent(t, map[string]string{"managed": "third"}))
	if err != nil {
		t.Fatal(err)
	}
	current = f.read(t)
	current.Data["concurrent"] = "preserved"
	if _, err = f.admin.CoreV1().ConfigMaps(current.Namespace).Update(f.ctx, current, metav1.UpdateOptions{FieldManager: "other-writer"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Execute(f.ctx, plan); !errors.Is(err, operation.ErrConflict) {
		t.Fatalf("stale apply: %v", err)
	}
	if got := f.read(t); got.Data["managed"] != "second" || got.Data["concurrent"] != "preserved" {
		t.Fatal("stale apply overwrote current data")
	}
}

type applyRaceClient struct {
	*kube.Backend
	beforePatch func()
	sent        int
}

func (c *applyRaceClient) PatchObject(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, pt types.PatchType, body []byte, opts metav1.PatchOptions) (*unstructured.Unstructured, error) {
	if len(opts.DryRun) == 0 {
		c.sent++
		c.beforePatch()
	}
	return c.Backend.PatchObject(ctx, gvr, ns, name, pt, body, opts)
}
func TestRealApplyPreconditionsPreventRecreationAndReplacementWrites(t *testing.T) {
	for _, replace := range []bool{false, true} {
		name := "deleted"
		if replace {
			name = "replaced"
		}
		t.Run(name, func(t *testing.T) {
			f := newApplyFixture(t)
			client := &applyRaceClient{Backend: f.b}
			client.beforePatch = func() {
				if err := f.admin.CoreV1().ConfigMaps(f.target.Namespace).Delete(f.ctx, f.target.Name, metav1.DeleteOptions{}); err != nil {
					t.Fatal(err)
				}
				if replace {
					cm, err := f.admin.CoreV1().ConfigMaps(f.target.Namespace).Create(f.ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: f.target.Name}, Data: map[string]string{"guard": "new-incarnation"}}, metav1.CreateOptions{FieldManager: "replacement-owner"})
					if err != nil || cm.UID == f.target.UID {
						t.Fatalf("replacement failed: %v", err)
					}
				}
			}
			s := operation.NewService(client, f.target.SessionID)
			p, err := s.PrepareApply(f.ctx, f.target, f.intent(t, map[string]string{"managed": "must-not-land"}))
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.Execute(f.ctx, p)
			if err == nil || result.State == "Succeeded" || client.sent != 1 {
				t.Fatalf("stale apply succeeded: %+v %v", result, err)
			}
			if _, err = s.Execute(f.ctx, p); err == nil || client.sent != 1 {
				t.Fatal("failed apply was replayed")
			}
			if replace {
				got := f.read(t)
				if len(got.Data) != 1 || got.Data["guard"] != "new-incarnation" {
					t.Fatal("replacement was mutated")
				}
			} else {
				if _, err = f.admin.CoreV1().ConfigMaps(f.target.Namespace).Get(f.ctx, f.target.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
					t.Fatalf("apply recreated deleted resource: %v", err)
				}
			}
		})
	}
}
func TestRealApplyCommittedResponseLossIsUnknownWithoutReplay(t *testing.T) {
	f := newApplyFixture(t)
	var writes atomic.Int32
	var injected atomic.Bool
	cfg := rest.CopyConfig(f.cfg)
	cfg.Wrap(func(base http.RoundTripper) http.RoundTripper {
		return afterCommitFailure{base, http.MethodPatch, &writes, &injected}
	})
	backend, err := kube.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := operation.NewService(backend, f.target.SessionID)
	p, err := s.PrepareApply(f.ctx, f.target, f.intent(t, map[string]string{"managed": "committed"}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Execute(f.ctx, p)
	if err == nil || result.State != "Unknown" || writes.Load() != 1 || !injected.Load() {
		t.Fatalf("%+v %v writes=%d", result, err, writes.Load())
	}
	if got := f.read(t); got.Data["managed"] != "committed" {
		t.Fatal("test did not actually commit the apply")
	}
	if _, err = s.Execute(f.ctx, p); err == nil || writes.Load() != 1 {
		t.Fatal("ambiguous apply replayed")
	}
}
func TestRealApplyRejectsReadOnlyIdentity(t *testing.T) {
	f := newApplyFixture(t)
	user := "aster-apply-reader-" + f.target.Namespace
	_, err := f.admin.RbacV1().Roles(f.target.Namespace).Create(f.ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "reader"}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"get"}}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.admin.RbacV1().RoleBindings(f.target.Namespace).Create(f.ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "reader"}, Subjects: []rbacv1.Subject{{Kind: "User", APIGroup: rbacv1.GroupName, Name: user}}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: "reader"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := rest.CopyConfig(f.cfg)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: user}
	b, err := kube.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	err = wait.PollUntilContextTimeout(f.ctx, 100*time.Millisecond, 10*time.Second, true, func(ctx context.Context) (bool, error) {
		_, err := b.GetObject(ctx, f.target.GVR, f.target.Namespace, f.target.Name)
		return err == nil, nil
	})
	if err != nil {
		t.Fatal("read permission did not become usable", err)
	}
	before := f.read(t)
	s := operation.NewService(b, f.target.SessionID)
	if p, err := s.PrepareApply(f.ctx, f.target, f.intent(t, map[string]string{"managed": "forbidden"})); p != nil || !apierrors.IsForbidden(err) {
		t.Fatalf("readonly identity could prepare apply: %v", err)
	}
	if after := f.read(t); after.ResourceVersion != before.ResourceVersion || len(after.Data) != 1 {
		t.Fatal("denied apply changed the object")
	}
}
