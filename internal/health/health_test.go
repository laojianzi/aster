package health

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func object(t *testing.T, value any) *unstructured.Unstructured {
	t.Helper()
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(value)
	if err != nil {
		t.Fatal(err)
	}
	return &unstructured.Unstructured{Object: raw}
}
func deployment() appsv1.Deployment {
	replicas := int32(2)
	return appsv1.Deployment{TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}, ObjectMeta: metav1.ObjectMeta{Name: "web", Generation: 3}, Spec: appsv1.DeploymentSpec{Replicas: &replicas}, Status: appsv1.DeploymentStatus{ObservedGeneration: 3, Replicas: 2, UpdatedReplicas: 2, ReadyReplicas: 2, AvailableReplicas: 2}}
}
func TestDeploymentReadinessRequiresObservedGenerationAndNoOldReplicas(t *testing.T) {
	cases := []struct {
		name   string
		change func(*appsv1.Deployment)
		want   State
	}{
		{"ready", func(*appsv1.Deployment) {}, Ready},
		{"old-status", func(d *appsv1.Deployment) { d.Status.ObservedGeneration = 2 }, Progressing},
		{"old-pods", func(d *appsv1.Deployment) { d.Status.Replicas = 3 }, Progressing},
		{"not-updated", func(d *appsv1.Deployment) { d.Status.UpdatedReplicas = 1 }, Progressing},
		{"not-available", func(d *appsv1.Deployment) { d.Status.AvailableReplicas = 1 }, Progressing},
		{"paused", func(d *appsv1.Deployment) { d.Spec.Paused = true }, Paused},
		{"deadline", func(d *appsv1.Deployment) {
			d.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded"}}
		}, Failed},
		{"stale-deadline", func(d *appsv1.Deployment) {
			d.Status.ObservedGeneration = 2
			d.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded"}}
		}, Progressing},
		{"replica-failure", func(d *appsv1.Deployment) {
			d.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentReplicaFailure, Status: corev1.ConditionTrue, Reason: "FailedCreate"}}
		}, Degraded},
		{"scale-zero", func(d *appsv1.Deployment) {
			*d.Spec.Replicas = 0
			d.Status = appsv1.DeploymentStatus{ObservedGeneration: 3}
		}, Ready},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := deployment()
			tc.change(&d)
			got := Assess(object(t, &d))
			if got.State != tc.want {
				t.Fatalf("got %+v, want %s", got, tc.want)
			}
		})
	}
}
func TestStatefulSetPartitionAndNonzeroOrdinalSemantics(t *testing.T) {
	replicas, partition := int32(4), int32(2)
	s := appsv1.StatefulSet{TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSet"}, ObjectMeta: metav1.ObjectMeta{Name: "db", Generation: 2}, Spec: appsv1.StatefulSetSpec{Replicas: &replicas, UpdateStrategy: appsv1.StatefulSetUpdateStrategy{Type: appsv1.RollingUpdateStatefulSetStrategyType, RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{Partition: &partition}}}, Status: appsv1.StatefulSetStatus{ObservedGeneration: 2, Replicas: 4, ReadyReplicas: 4, UpdatedReplicas: 2, CurrentRevision: "old", UpdateRevision: "new"}}
	if r := Assess(object(t, &s)); r.State != Ready || !strings.Contains(r.Message, "Partitioned") {
		t.Fatal(r)
	}
	s.Spec.Ordinals = &appsv1.StatefulSetOrdinals{Start: 10}
	if r := Assess(object(t, &s)); r.State != Progressing {
		t.Fatalf("partition below start must update all replicas: %+v", r)
	}
	s.Status.UpdatedReplicas = 4
	s.Status.CurrentRevision = "new"
	if r := Assess(object(t, &s)); r.State != Ready {
		t.Fatal(r)
	}
	s.Spec.MinReadySeconds = 30
	if r := Assess(object(t, &s)); r.State != Progressing {
		t.Fatal("readiness interval not respected:", r)
	}
	s.Status.AvailableReplicas = 4
	if r := Assess(object(t, &s)); r.State != Ready {
		t.Fatal(r)
	}
	s.Spec.UpdateStrategy.Type = appsv1.OnDeleteStatefulSetStrategyType
	if r := Assess(object(t, &s)); r.State != Manual {
		t.Fatal(r)
	}
}
func TestDaemonSetAndJobStates(t *testing.T) {
	d := appsv1.DaemonSet{TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "DaemonSet"}, ObjectMeta: metav1.ObjectMeta{Name: "agent", Generation: 1}, Spec: appsv1.DaemonSetSpec{UpdateStrategy: appsv1.DaemonSetUpdateStrategy{Type: appsv1.RollingUpdateDaemonSetStrategyType}}, Status: appsv1.DaemonSetStatus{ObservedGeneration: 1, DesiredNumberScheduled: 2, CurrentNumberScheduled: 2, UpdatedNumberScheduled: 2, NumberReady: 2, NumberAvailable: 2}}
	if r := Assess(object(t, &d)); r.State != Ready {
		t.Fatal(r)
	}
	d.Status.NumberMisscheduled = 1
	if r := Assess(object(t, &d)); r.State != Progressing {
		t.Fatal(r)
	}
	d.Spec.UpdateStrategy.Type = appsv1.OnDeleteDaemonSetStrategyType
	if r := Assess(object(t, &d)); r.State != Manual {
		t.Fatal(r)
	}
	j := batchv1.Job{TypeMeta: metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"}, ObjectMeta: metav1.ObjectMeta{Name: "job"}}
	if r := Assess(object(t, &j)); r.State != Progressing {
		t.Fatal(r)
	}
	j.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
	if r := Assess(object(t, &j)); r.State != Completed {
		t.Fatal(r)
	}
	j.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"}}
	if r := Assess(object(t, &j)); r.State != Failed {
		t.Fatal(r)
	}
}
func TestPodPhaseIsNotReadiness(t *testing.T) {
	p := corev1.Pod{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	if r := Assess(object(t, &p)); r.State != Progressing {
		t.Fatal(r)
	}
	p.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	if r := Assess(object(t, &p)); r.State != Ready {
		t.Fatal(r)
	}
	p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "web", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}}}
	if r := Assess(object(t, &p)); r.State != Degraded {
		t.Fatal(r)
	}
	now := metav1.Now()
	p.DeletionTimestamp = &now
	if r := Assess(object(t, &p)); r.State != Terminating {
		t.Fatal(r)
	}
}
func TestUnknownCRDsAreNeverMarkedHealthyAndTextIsBounded(t *testing.T) {
	if r := Assess(nil); r.State != Unknown {
		t.Fatal(r)
	}
	d := deployment()
	o := object(t, &d)
	o.SetAPIVersion("custom.example/v1")
	if Supports(o) || Assess(o).State != Unsupported {
		t.Fatal("CRD borrowed built-in semantics")
	}
	o.SetAPIVersion("apps/v1")
	o.Object["status"] = "not-an-object"
	if r := Assess(o); r.State != Unknown {
		t.Fatal(r)
	}
	for _, value := range []string{"字", "😀", "\x1b", "a"} {
		text := bounded(strings.Repeat(value, 3000))
		if len(text) > 1024 || strings.ContainsRune(text, '\x1b') {
			t.Fatal("unbounded or unsafe diagnostic")
		}
	}
}
