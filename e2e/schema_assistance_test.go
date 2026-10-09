//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/schemaassist"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
)

func TestRealSchemaBuiltinsAndReadOnlyAssistance(t *testing.T) {
	f := newApplyFixture(t)
	before := f.read(t)
	for _, tc := range []struct {
		gvk       schema.GroupVersionKind
		path, typ string
	}{
		{schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, "/spec/containers/0/image", "string"},
		{schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}, "/spec/replicas", "integer"},
		{schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}, "/data/example", "string"},
	} {
		d, err := f.b.SchemaDocument(f.ctx, tc.gvk)
		if err != nil {
			t.Fatalf("%v: %v", tc.gvk, err)
		}
		h, err := d.Help(f.ctx, tc.path)
		if err != nil || h.Type != tc.typ {
			t.Fatalf("%v: %+v %v", tc.gvk, h, err)
		}
	}
	d, err := f.b.SchemaDocument(f.ctx, schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := d.Check(f.ctx, map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": before.Name}, "data": map[string]any{"bad": int64(42)}})
	if err != nil || !strings.Contains(r.Text(), "/data/bad · type") {
		t.Fatalf("%+v %v", r, err)
	}
	after := f.read(t)
	if after.ResourceVersion != before.ResourceVersion {
		t.Fatal("read-only schema changed resource")
	}
}
func TestRealSchemaDiscoveryAuthorizationIsIndependent(t *testing.T) {
	f := newApplyFixture(t)
	name := "schema-" + f.target.Namespace
	cfg := rest.CopyConfig(f.cfg)
	// Explicit unauthenticated group prevents automatic membership of the
	// default discovery grant. This is an impersonated isolated-fixture user,
	// not an anonymous or unauthenticated HTTP request from the desktop.
	cfg.Impersonate = rest.ImpersonationConfig{UserName: name, Groups: []string{"system:unauthenticated"}}
	b, err := kube.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	gvk := schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}
	if _, err = b.SchemaDocument(f.ctx, gvk); !errors.Is(err, schemaassist.ErrForbidden) {
		t.Fatalf("expected discovery denial, got %v", err)
	}
	role, err := f.admin.RbacV1().ClusterRoles().Create(f.ctx, &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: name}, Rules: []rbacv1.PolicyRule{{Verbs: []string{"get"}, NonResourceURLs: []string{"/openapi/v3", "/openapi/v3/api/v1"}}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = f.admin.RbacV1().ClusterRoleBindings().Delete(ctx, name, metav1.DeleteOptions{})
		_ = f.admin.RbacV1().ClusterRoles().Delete(ctx, name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &role.UID}})
	})
	_, err = f.admin.RbacV1().ClusterRoleBindings().Create(f.ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name}, RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: name}, Subjects: []rbacv1.Subject{{APIGroup: "rbac.authorization.k8s.io", Kind: "User", Name: name}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	err = wait.PollUntilContextTimeout(f.ctx, 100*time.Millisecond, 20*time.Second, true, func(ctx context.Context) (bool, error) {
		_, e := b.SchemaDocument(ctx, gvk)
		if errors.Is(e, schemaassist.ErrForbidden) {
			return false, nil
		}
		return e == nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.GetObject(f.ctx, f.target.GVR, f.target.Namespace, f.target.Name); !apierrors.IsForbidden(err) {
		t.Fatalf("schema access granted object access: %v", err)
	}
	if _, err = b.SchemaDocument(f.ctx, schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}); !errors.Is(err, schemaassist.ErrForbidden) {
		t.Fatalf("core discovery grant escaped group: %v", err)
	}
}
func TestRealSchemaStructuralCRDAndPreservedFields(t *testing.T) {
	f := newApplyFixture(t)
	gvr := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	group := f.target.Namespace + ".aster.test"
	raw := fmt.Sprintf(`{"apiVersion":"apiextensions.k8s.io/v1","kind":"CustomResourceDefinition","metadata":{"name":"schemathings.%s"},"spec":{"group":"%s","scope":"Namespaced","names":{"plural":"schemathings","singular":"schemathing","kind":"SchemaThing"},"versions":[{"name":"v1","served":true,"storage":true,"schema":{"openAPIV3Schema":{"type":"object","properties":{"spec":{"type":"object","required":["replicas"],"properties":{"replicas":{"type":"integer","description":"Desired count"},"opaque":{"type":"object","x-kubernetes-preserve-unknown-fields":true}}}}}}}]}}`, group, group)
	crd := &unstructured.Unstructured{}
	if err := crd.UnmarshalJSON([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	created, err := f.b.CreateObject(f.ctx, gvr, "", crd, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		uid := created.GetUID()
		_ = f.b.DeleteObject(ctx, gvr, "", created.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
	})
	var d *schemaassist.Document
	err = wait.PollUntilContextTimeout(f.ctx, 150*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		var e error
		d, e = f.b.SchemaDocument(ctx, schema.GroupVersionKind{Group: group, Version: "v1", Kind: "SchemaThing"})
		if errors.Is(e, schemaassist.ErrNotFound) || errors.Is(e, schemaassist.ErrUnavailable) {
			return false, nil
		}
		return e == nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
	h, err := d.Help(f.ctx, "/spec/replicas")
	if err != nil || h.Type != "integer" || !strings.Contains(h.Description, "Desired count") {
		t.Fatalf("%+v %v", h, err)
	}
	draft := map[string]any{"apiVersion": group + "/v1", "kind": "SchemaThing", "metadata": map[string]any{"name": "not-created"}, "spec": map[string]any{"opaque": map[string]any{"arbitrary": "private"}}}
	r, err := d.Check(f.ctx, draft)
	if err != nil || !strings.Contains(r.Text(), "/spec/replicas · required") || strings.Contains(r.Text(), "/spec/opaque/arbitrary") {
		t.Fatalf("%+v %v", r, err)
	}
	// Inspection must not create the draft's resource.
	resources := schema.GroupVersionResource{Group: group, Version: "v1", Resource: "schemathings"}
	if _, err = f.b.GetObject(f.ctx, resources, f.target.Namespace, "not-created"); !apierrors.IsNotFound(err) {
		t.Fatalf("draft unexpectedly persisted: %v", err)
	}
}
