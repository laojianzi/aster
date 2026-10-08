package health

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestNodeReadinessPressureAndCordon(t *testing.T) {
	cases := []struct {
		name       string
		conditions []corev1.NodeCondition
		cordoned   bool
		want       State
	}{
		{"no-conditions", nil, false, Degraded},
		{"ready-unknown", []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionUnknown}}, false, Degraded},
		{"ready", []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}, false, Ready},
		{"cordoned", []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}, true, Ready},
		{"disk", []corev1.NodeCondition{{Type: corev1.NodeDiskPressure, Status: corev1.ConditionTrue}}, false, Degraded},
		{"memory", []corev1.NodeCondition{{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionTrue}}, false, Degraded},
		{"pid", []corev1.NodeCondition{{Type: corev1.NodePIDPressure, Status: corev1.ConditionTrue}}, false, Degraded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := corev1.Node{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Node"}, Spec: corev1.NodeSpec{Unschedulable: tc.cordoned}, Status: corev1.NodeStatus{Conditions: tc.conditions}}
			report := Assess(object(t, &n))
			if report.State != tc.want {
				t.Fatalf("%+v", report)
			}
			if tc.cordoned && !strings.Contains(report.Message, "cordoned") {
				t.Fatal(report)
			}
		})
	}
}

func TestMalformedBuiltinStatusIsNeverHealthy(t *testing.T) {
	for _, kind := range []string{"Deployment", "StatefulSet", "DaemonSet", "Pod", "Node", "Job"} {
		t.Run(kind, func(t *testing.T) {
			version := "apps/v1"
			if kind == "Pod" || kind == "Node" {
				version = "v1"
			}
			if kind == "Job" {
				version = "batch/v1"
			}
			o := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": version, "kind": kind, "status": true}}
			if report := Assess(o); report.State != Unknown {
				t.Fatalf("%+v", report)
			}
		})
	}
	if Supports(nil) {
		t.Fatal("nil object is supported")
	}
	report := Report{State: Ready, Message: "fixture", Generation: 7, ObservedGeneration: 7}
	if !strings.Contains(report.String(), "Ready\nfixture") || !strings.Contains(report.String(), "Controller observed generation: 7") {
		t.Fatal(report.String())
	}
}

func TestStatefulSetAndDeploymentRejectInvalidReplicaMetadata(t *testing.T) {
	one, minus := int32(1), int32(-1)
	for _, field := range []string{"replicas", "partition", "ordinal", "revision", "observation"} {
		t.Run(field, func(t *testing.T) {
			s := appsv1.StatefulSet{TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSet"}, ObjectMeta: metav1.ObjectMeta{Generation: 2}, Spec: appsv1.StatefulSetSpec{Replicas: &one}, Status: appsv1.StatefulSetStatus{ObservedGeneration: 2, Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1}}
			want := Unknown
			switch field {
			case "replicas":
				s.Spec.Replicas = &minus
			case "partition":
				s.Spec.UpdateStrategy.RollingUpdate = &appsv1.RollingUpdateStatefulSetStrategy{Partition: &minus}
			case "ordinal":
				s.Spec.Ordinals = &appsv1.StatefulSetOrdinals{Start: -1}
			case "revision":
				want = Progressing
			case "observation":
				s.Status.ObservedGeneration = 1
				want = Progressing
			}
			if report := Assess(object(t, &s)); report.State != want {
				t.Fatalf("%+v", report)
			}
		})
	}
	d := deployment()
	d.Spec.Replicas = &minus
	if report := Assess(object(t, &d)); report.State != Unknown {
		t.Fatal(report)
	}
	d.Spec.Replicas = nil
	d.Status.Replicas = 1
	d.Status.UpdatedReplicas = 1
	if report := Assess(object(t, &d)); report.State != Ready {
		t.Fatal("default replica count:", report)
	}
}

func TestPodCompletionAndJobSuspensionAreDistinct(t *testing.T) {
	p := corev1.Pod{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"}, Status: corev1.PodStatus{Phase: corev1.PodSucceeded}}
	if report := Assess(object(t, &p)); report.State != Completed {
		t.Fatal(report)
	}
	p.Status.Phase = corev1.PodFailed
	if report := Assess(object(t, &p)); report.State != Failed {
		t.Fatal(report)
	}
	yes := true
	j := batchv1.Job{TypeMeta: metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"}, Spec: batchv1.JobSpec{Suspend: &yes}}
	if report := Assess(object(t, &j)); report.State != Paused {
		t.Fatal(report)
	}
	j.Spec.Suspend = nil
	j.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionFalse}}
	if report := Assess(object(t, &j)); report.State != Progressing {
		t.Fatal(report)
	}
	d := appsv1.DaemonSet{TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "DaemonSet"}, ObjectMeta: metav1.ObjectMeta{Generation: 3}, Status: appsv1.DaemonSetStatus{ObservedGeneration: 2}}
	if report := Assess(object(t, &d)); report.State != Progressing {
		t.Fatal(report)
	}
}
