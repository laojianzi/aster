package relationship

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/laojianzi/aster/internal/resource"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

type fakeClient struct {
	objects map[string]*unstructured.Unstructured
	gets    []string
	pages   []metav1.ListOptions
	list    func(schema.GroupVersionResource, string, metav1.ListOptions) (*unstructured.UnstructuredList, error)
}

func key(gvr schema.GroupVersionResource, ns, name string) string {
	return gvr.String() + "/" + ns + "/" + name
}
func (f *fakeClient) GetObject(ctx context.Context, gvr schema.GroupVersionResource, ns, name string) (*unstructured.Unstructured, error) {
	f.gets = append(f.gets, key(gvr, ns, name))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if o := f.objects[key(gvr, ns, name)]; o != nil {
		return o.DeepCopy(), nil
	}
	return nil, apierrors.NewNotFound(gvr.GroupResource(), name)
}
func (f *fakeClient) ListPage(ctx context.Context, gvr schema.GroupVersionResource, ns string, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.pages = append(f.pages, opts)
	if f.list == nil {
		return page(), nil
	}
	return f.list(gvr, ns, opts)
}
func object(api, kind, ns, name, uid string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": api, "kind": kind, "metadata": map[string]interface{}{"namespace": ns, "name": name, "uid": uid, "resourceVersion": "1"}}}
}
func page(objects ...*unstructured.Unstructured) *unstructured.UnstructuredList {
	p := &unstructured.UnstructuredList{}
	p.SetResourceVersion("collection-1")
	for _, o := range objects {
		p.Items = append(p.Items, *o.DeepCopy())
	}
	return p
}
func fixture(root *unstructured.Unstructured, gvr schema.GroupVersionResource) (*fakeClient, resource.Identity) {
	return &fakeClient{objects: map[string]*unstructured.Unstructured{key(gvr, root.GetNamespace(), root.GetName()): root}}, resource.Identity{SessionID: "test-session", GVR: gvr, Namespace: root.GetNamespace(), Name: root.GetName(), UID: root.GetUID()}
}
func own(child, root *unstructured.Unstructured) {
	yes := true
	child.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: root.GetAPIVersion(), Kind: root.GetKind(), Name: root.GetName(), UID: root.GetUID(), Controller: &yes}})
}
func TestRelationshipsVerifyOwnerUIDAndScope(t *testing.T) {
	pod := object("v1", "Pod", "team", "pod", "pod-uid")
	rs := object("apps/v1", "ReplicaSet", "team", "rs", "rs-old")
	own(pod, rs)
	f, target := fixture(pod, core("pods"))
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}
	f.objects[key(gvr, "team", "rs")] = rs
	snap, err := New(f, nil, Limits{}).Read(context.Background(), target)
	if err != nil || len(snap.Links) != 1 || !snap.Links[0].Navigable() || snap.Links[0].Target.Namespace != "team" {
		t.Fatalf("%+v %v", snap, err)
	}
	rs.SetUID("rs-new")
	snap, err = New(f, nil, Limits{}).Read(context.Background(), target)
	if err != nil || snap.Links[0].State != Replaced || snap.Links[0].Navigable() || !snap.Incomplete {
		t.Fatalf("stale owner accepted: %+v %v", snap, err)
	}
}
func TestRelationshipsDoNotGuessCustomResourcePlurals(t *testing.T) {
	root := object("v1", "Pod", "team", "pod", "pod-uid")
	root.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "example.test/v1", Kind: "Mouse", Name: "parent", UID: "parent-uid"}})
	f, target := fixture(root, core("pods"))
	snap, err := New(f, nil, Limits{}).Read(context.Background(), target)
	if err != nil || len(f.gets) != 1 || len(snap.Links) != 1 || snap.Links[0].State != Unsupported {
		t.Fatalf("%+v %v", snap, err)
	}
	typ := Type{GVR: schema.GroupVersionResource{Group: "example.test", Version: "v1", Resource: "mice"}, Kind: "Mouse", Namespaced: true}
	f.objects[key(typ.GVR, "team", "parent")] = object("example.test/v1", "Mouse", "team", "parent", "parent-uid")
	snap, err = New(f, []Type{typ}, Limits{}).Read(context.Background(), target)
	if err != nil || !snap.Links[0].Navigable() || snap.Links[0].Target.GVR.Resource != "mice" {
		t.Fatalf("%+v %v", snap, err)
	}
}
func TestRelationshipsRejectClusterToNamespacedOwner(t *testing.T) {
	root := object("v1", "Node", "", "node", "node-uid")
	owner := object("apps/v1", "Deployment", "team", "deployment", "deployment-uid")
	own(root, owner)
	f, target := fixture(root, core("nodes"))
	snap, err := New(f, nil, Limits{}).Read(context.Background(), target)
	if err != nil || !snap.Incomplete || len(f.gets) != 1 || snap.Links[0].Navigable() {
		t.Fatalf("%+v %v", snap, err)
	}
}
func TestRelationshipsDependentsUseUIDNotLabels(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	root := object("apps/v1", "Deployment", "team", "app", "deployment-uid")
	child := object("apps/v1", "ReplicaSet", "team", "owned", "rs-one")
	own(child, root)
	stray := object("apps/v1", "ReplicaSet", "team", "stray", "rs-two")
	stray.SetLabels(map[string]string{"app": "same-label"})
	outside := child.DeepCopy()
	outside.SetNamespace("outside")
	outside.SetName("cross-namespace")
	f, target := fixture(root, gvr)
	f.list = func(gvr schema.GroupVersionResource, ns string, o metav1.ListOptions) (*unstructured.UnstructuredList, error) {
		if ns != "team" || gvr.Resource != "replicasets" || o.Limit <= 0 {
			t.Fatal("unbounded or wrong query")
		}
		return page(child, stray, outside), nil
	}
	snap, err := New(f, nil, Limits{}).Read(context.Background(), target)
	if err != nil || len(snap.Links) != 1 || snap.Links[0].Target.Name != "owned" || !snap.Incomplete {
		t.Fatalf("%+v %v", snap, err)
	}
}
func TestRelationshipsRespectBoundedPagination(t *testing.T) {
	root := object("apps/v1", "Deployment", "team", "app", "root")
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	f, target := fixture(root, gvr)
	child := object("apps/v1", "ReplicaSet", "team", "owned", "child")
	own(child, root)
	f.list = func(_ schema.GroupVersionResource, _ string, o metav1.ListOptions) (*unstructured.UnstructuredList, error) {
		p := page(child)
		p.SetContinue("next")
		return p, nil
	}
	snap, err := New(f, nil, Limits{Objects: 2, PageSize: 1}).Read(context.Background(), target)
	if err != nil || !snap.Incomplete || len(f.pages) != 2 || snap.Scanned != 2 || len(snap.Links) != 1 {
		t.Fatalf("%+v %v pages=%v", snap, err, f.pages)
	}
	if f.pages[1].Continue != "next" {
		t.Fatal("continuation not propagated")
	}
}
func TestRelationshipsPermissionDenialIsNotEmptySuccess(t *testing.T) {
	root := object("apps/v1", "Deployment", "team", "app", "root")
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	f, target := fixture(root, gvr)
	f.list = func(gvr schema.GroupVersionResource, _ string, _ metav1.ListOptions) (*unstructured.UnstructuredList, error) {
		return nil, apierrors.NewForbidden(gvr.GroupResource(), "", errors.New("synthetic-private-reason"))
	}
	snap, err := New(f, nil, Limits{}).Read(context.Background(), target)
	if err != nil || !snap.Incomplete || !strings.Contains(strings.Join(snap.Warnings, " "), "Forbidden") || strings.Contains(strings.Join(snap.Warnings, " "), "synthetic-private-reason") {
		t.Fatalf("%+v %v", snap, err)
	}
}
func TestRelationshipsSelectorlessServiceNeverQueriesAllPods(t *testing.T) {
	for _, selector := range []map[string]string{nil, {}, {"app": "api"}} {
		root := object("v1", "Service", "team", "service", "service-uid")
		if selector != nil {
			_ = unstructured.SetNestedStringMap(root.Object, selector, "spec", "selector")
		}
		f, target := fixture(root, core("services"))
		podQueries := 0
		f.list = func(gvr schema.GroupVersionResource, ns string, o metav1.ListOptions) (*unstructured.UnstructuredList, error) {
			if ns != "team" {
				t.Fatal("scope widened")
			}
			if gvr.Resource == "pods" {
				podQueries++
				if o.LabelSelector != "app=api" {
					t.Fatal(o)
				}
				pod := object("v1", "Pod", "team", "selected", "p")
				pod.SetLabels(map[string]string{"app": "api"})
				return page(pod), nil
			}
			return page(), nil
		}
		snap, err := New(f, nil, Limits{}).Read(context.Background(), target)
		if err != nil {
			t.Fatal(err)
		}
		if len(selector) == 0 && podQueries != 0 {
			t.Fatal("selectorless service listed all Pods")
		}
		if len(selector) > 0 && (podQueries != 1 || len(snap.Links) != 1 || snap.Links[0].Relation != "Selector match") {
			t.Fatal(snap)
		}
	}
}
func TestRelationshipsNeverFetchSecretContents(t *testing.T) {
	root := object("v1", "Pod", "team", "pod", "pod-uid")
	root.Object["spec"] = map[string]interface{}{"containers": []interface{}{map[string]interface{}{"name": "app", "env": []interface{}{map[string]interface{}{"name": "PASSWORD", "valueFrom": map[string]interface{}{"secretKeyRef": map[string]interface{}{"name": "credentials", "key": "password"}}}}}}}
	f, target := fixture(root, core("pods"))
	snap, err := New(f, nil, Limits{}).Read(context.Background(), target)
	if err != nil || len(f.gets) != 1 || len(snap.Links) != 1 || snap.Links[0].State != Referenced || snap.Links[0].Navigable() || snap.Links[0].Target.Name != "credentials" {
		t.Fatalf("%+v %v gets=%v", snap, err, f.gets)
	}
}
func TestRelationshipsRejectReplacedRootAndCancellation(t *testing.T) {
	f, target := fixture(object("v1", "Pod", "team", "pod", "new"), core("pods"))
	target.UID = types.UID("old")
	if _, err := New(f, nil, Limits{}).Read(context.Background(), target); !errors.Is(err, ErrReplaced) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(f, nil, Limits{}).Read(ctx, target); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRelationshipsDoNotFetchSecretOrConfigMapOwners(t *testing.T) {
	for _, kind := range []string{"Secret", "ConfigMap"} {
		root := object("v1", "Pod", "team", "pod", "pod-uid")
		owner := object("v1", kind, "team", "sensitive-owner", "sensitive-uid")
		own(root, owner)
		f, target := fixture(root, core("pods"))
		snapshot, err := New(f, nil, Limits{}).Read(context.Background(), target)
		if err != nil || len(f.gets) != 1 || len(snapshot.Links) != 1 || snapshot.Links[0].Navigable() || snapshot.Links[0].State != Referenced {
			t.Fatalf("sensitive owner read: %+v %v", snapshot, err)
		}
	}
}
func TestRelationshipsWrongOwnerKindIsNotVerified(t *testing.T) {
	root := object("v1", "Pod", "team", "pod", "pod-uid")
	owner := object("apps/v1", "ReplicaSet", "team", "parent", "parent-uid")
	own(root, owner)
	f, target := fixture(root, core("pods"))
	f.objects[key(schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, "team", "parent")] = object("apps/v1", "Deployment", "team", "parent", "parent-uid")
	snapshot, err := New(f, nil, Limits{}).Read(context.Background(), target)
	if err != nil || snapshot.Links[0].Navigable() {
		t.Fatal("mismatched owner kind was navigable")
	}
}

func TestRelationshipsRejectWrongRootKind(t *testing.T) {
	root := object("v1", "NotPod", "team", "pod", "uid")
	f, target := fixture(root, core("pods"))
	if _, err := New(f, nil, Limits{}).Read(context.Background(), target); err == nil {
		t.Fatal("wrong root kind was accepted")
	}
}
