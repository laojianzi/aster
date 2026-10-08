//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/operation"
	"github.com/laojianzi/aster/internal/rollout"
	"github.com/laojianzi/aster/internal/testcluster"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestReviewedScaleThenRealControllerReadiness(t *testing.T) {
	f := testcluster.NewDeployment(t, 0)
	backend, err := kube.New(f.Config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	target := f.Target("rollout-e2e")
	observer := rollout.New(backend, rollout.Options{Interval: 100 * time.Millisecond})
	first, err := observer.Observe(ctx, target, f.Deployment.Generation, func(rollout.Observation) {})
	if err != nil || first.State != rollout.Ready {
		t.Fatalf("initial readiness: %+v %v", first, err)
	}
	service := operation.NewService(backend, target.SessionID)
	plan, err := service.PrepareScale(ctx, target, 1)
	if err != nil {
		t.Fatal(err)
	}
	live, err := f.Client.AppsV1().Deployments(target.Namespace).Get(ctx, target.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if *live.Spec.Replicas != 0 {
		t.Fatal("scale dry-run persisted")
	}
	accepted, err := service.Execute(ctx, plan)
	if err != nil || accepted.State != "Succeeded" {
		t.Fatalf("scale: %+v %v", accepted, err)
	}
	final, err := observer.Observe(ctx, target, accepted.Object.GetGeneration(), func(rollout.Observation) {})
	if err != nil || final.State != rollout.Ready || final.Health.ObservedGeneration < accepted.Object.GetGeneration() {
		t.Fatalf("readiness: %+v %v", final, err)
	}
	live, err = f.Client.AppsV1().Deployments(target.Namespace).Get(ctx, target.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if live.Status.AvailableReplicas != 1 {
		t.Fatalf("no available Pod: %+v", live.Status)
	}
}

func TestRealRolloutStopsForNewGenerationAndReplacement(t *testing.T) {
	f := testcluster.NewDeployment(t, 0)
	backend, err := kube.New(f.Config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	target := f.Target("rollout-replacement")
	observer := rollout.New(backend, rollout.Options{Interval: 20 * time.Millisecond})
	_, err = observer.Observe(ctx, target, f.Deployment.Generation, func(rollout.Observation) {})
	if err != nil {
		t.Fatal(err)
	}
	newer, err := f.Client.AppsV1().Deployments(target.Namespace).Patch(ctx, target.Name, types.MergePatchType, []byte(`{"spec":{"paused":true}}`), metav1.PatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := observer.Observe(ctx, target, f.Deployment.Generation, func(rollout.Observation) {})
	if err != nil || state.State != rollout.Superseded {
		t.Fatalf("new-generation: %+v %v", state, err)
	}
	uid := newer.UID
	if err = f.Client.AppsV1().Deployments(target.Namespace).Delete(ctx, target.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil {
		t.Fatal(err)
	}
	for {
		_, err = f.Client.AppsV1().Deployments(target.Namespace).Get(ctx, target.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	replacement := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: target.Name}, Spec: newer.Spec}
	fresh, err := f.Client.AppsV1().Deployments(target.Namespace).Create(ctx, replacement, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.UID == target.UID {
		t.Fatal("replacement reused UID")
	}
	state, err = observer.Observe(ctx, target, newer.Generation, func(rollout.Observation) {})
	if err != nil || state.State != rollout.Replaced {
		t.Fatalf("replacement: %+v %v", state, err)
	}
}
