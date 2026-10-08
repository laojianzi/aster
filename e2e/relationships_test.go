//go:build e2e

package e2e

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/relationship"
	"github.com/laojianzi/aster/internal/resource"
	"github.com/laojianzi/aster/internal/rollout"
	"github.com/laojianzi/aster/internal/testcluster"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/rest"
)

func TestRealWorkloadRelationshipsRespectUIDAndSelectors(t *testing.T) {
	f := testcluster.NewDeployment(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	backend, err := kube.New(f.Config)
	if err != nil {
		t.Fatal(err)
	}
	state, err := rollout.New(backend, rollout.Options{Interval: 100 * time.Millisecond}).Observe(ctx, f.Target("ready"), f.Deployment.Generation, func(rollout.Observation) {})
	if err != nil || state.State != rollout.Ready {
		t.Fatalf("fixture not ready: %+v %v", state, err)
	}
	zero, yes := int32(0), true
	// A different real owner prevents the Deployment controller from adopting the
	// label-matching ReplicaSet. Label equality must not imply ownership.
	other := f.Deployment.DeepCopy()
	other.ObjectMeta = metav1.ObjectMeta{Name: "other-owner"}
	other.Spec.Replicas = &zero
	other.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"other": "true"}}
	other.Spec.Template.Labels = map[string]string{"other": "true"}
	other, err = f.Client.AppsV1().Deployments(f.Deployment.Namespace).Create(ctx, other, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	template := f.Deployment.Spec.Template.DeepCopy()
	template.Labels = map[string]string{"unrelated": "true"}
	stray, err := f.Client.AppsV1().ReplicaSets(f.Deployment.Namespace).Create(ctx, &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: "label-match-not-owned", Labels: f.Deployment.Spec.Selector.MatchLabels, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: other.Name, UID: other.UID, Controller: &yes}}},
		Spec:       appsv1.ReplicaSetSpec{Replicas: &zero, Selector: &metav1.LabelSelector{MatchLabels: template.Labels}, Template: *template},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reader := relationship.New(backend, nil, relationship.Limits{})
	snapshot, err := reader.Read(ctx, f.Target("relationships"))
	if err != nil || snapshot.Incomplete {
		t.Fatalf("%+v %v", snapshot, err)
	}
	var rs relationship.Link
	for _, link := range snapshot.Links {
		if link.Target.UID == stray.UID {
			t.Fatal("label-matching unrelated ReplicaSet was included")
		}
		if link.Relation == "Dependent" && link.Type.Kind == "ReplicaSet" {
			rs = link
		}
	}
	if !rs.Navigable() {
		t.Fatal("owned ReplicaSet missing")
	}
	if _, err = f.Client.AppsV1().ReplicaSets(f.Deployment.Namespace).Get(ctx, stray.Name, metav1.GetOptions{}); err != nil {
		t.Fatal("negative fixture disappeared", err)
	}
	children, err := reader.Read(ctx, rs.Target)
	if err != nil {
		t.Fatal(err)
	}
	var pod relationship.Link
	for _, link := range children.Links {
		if link.Relation == "Dependent" && link.Type.Kind == "Pod" {
			pod = link
		}
	}
	if !pod.Navigable() {
		t.Fatal("owned Pod missing")
	}
	svc, err := f.Client.CoreV1().Services(f.Deployment.Namespace).Create(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "related-service"}, Spec: corev1.ServiceSpec{Selector: f.Deployment.Spec.Selector.MatchLabels, Ports: []corev1.ServicePort{{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(8080)}}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for {
		slices, e := f.Client.DiscoveryV1().EndpointSlices(svc.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "kubernetes.io/service-name=" + svc.Name})
		if e == nil && len(slices.Items) > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("endpoint slice did not appear: %v", e)
		case <-time.After(100 * time.Millisecond):
		}
	}
	target := resource.Identity{SessionID: "relationships", GVR: schema.GroupVersionResource{Version: "v1", Resource: "services"}, Namespace: svc.Namespace, Name: svc.Name, UID: svc.UID}
	snapshot, err = reader.Read(ctx, target)
	if err != nil || snapshot.Incomplete {
		t.Fatalf("%+v %v", snapshot, err)
	}
	selectorMatch, sliceMatch := false, false
	for _, link := range snapshot.Links {
		if link.Relation == "Selector match" && link.Target.UID == pod.Target.UID {
			selectorMatch = true
		}
		if link.Relation == "EndpointSlice" && link.Navigable() {
			sliceMatch = true
		}
	}
	if !selectorMatch || !sliceMatch {
		t.Fatalf("Service links missing: %+v", snapshot)
	}
	svc.Spec.Selector = nil
	if _, err = f.Client.CoreV1().Services(svc.Namespace).Update(ctx, svc, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = reader.Read(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range snapshot.Links {
		if link.Relation == "Selector match" {
			t.Fatal("selectorless Service selected Pods")
		}
	}
	wrong := f.Target("relationships")
	wrong.UID = "not-the-recorded-uid"
	if _, err = reader.Read(ctx, wrong); !errors.Is(err, relationship.ErrReplaced) {
		t.Fatal("replaced root accepted", err)
	}
	limited, err := relationship.New(backend, nil, relationship.Limits{Objects: 1, PageSize: 1}).Read(ctx, f.Target("relationships"))
	if err != nil || !limited.Incomplete || limited.Scanned > 1 {
		t.Fatalf("unbounded relationship scan: %+v %v", limited, err)
	}
}
func TestRealRelationshipsReportRBACPartialResults(t *testing.T) {
	f := testcluster.NewDeployment(t, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	const user = "aster-relationship-reader"
	_, err := f.Client.RbacV1().Roles(f.Deployment.Namespace).Create(ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: user}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"get"}}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Client.RbacV1().RoleBindings(f.Deployment.Namespace).Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: user}, Subjects: []rbacv1.Subject{{Kind: "User", Name: user}}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: user}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := rest.CopyConfig(f.Config)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: user}
	backend, err := kube.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	target := f.Target("restricted-relationships")
	for {
		_, err = backend.GetObject(ctx, target.GVR, target.Namespace, target.Name)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("read authorization did not propagate: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	snapshot, err := relationship.New(backend, nil, relationship.Limits{}).Read(ctx, target)
	if err != nil || !snapshot.Incomplete || len(snapshot.Links) != 0 || !strings.Contains(strings.Join(snapshot.Warnings, " "), "Forbidden") {
		t.Fatalf("RBAC denial masqueraded as a complete empty result: %+v %v", snapshot, err)
	}
}
