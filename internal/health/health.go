// Package health interprets resource status without performing network I/O.
// API acceptance, controller observation and workload readiness are distinct.
package health

import (
	"fmt"
	"strings"
	"unicode/utf8"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

type State string

const (
	Unknown     State = "Unknown"
	Progressing State = "Progressing"
	Ready       State = "Ready"
	Degraded    State = "Degraded"
	Failed      State = "Failed"
	Completed   State = "Completed"
	Paused      State = "Paused"
	Manual      State = "Manual intervention"
	Terminating State = "Terminating"
	Unsupported State = "Unsupported"
)

// Report contains bounded display text only, never a resource body or Secret.
// Ready and Completed describe this observation, not continued availability.
type Report struct {
	State              State
	Message            string
	Generation         int64
	ObservedGeneration int64
}

func (r Report) String() string {
	return fmt.Sprintf("%s\n%s\n\nGeneration: %d\nController observed generation: %d", r.State, r.Message, r.Generation, r.ObservedGeneration)
}

// Supports deliberately matches group and version as well as Kind. A CRD
// named Deployment must not inherit the semantics of apps/v1 Deployment.
func Supports(o *unstructured.Unstructured) bool {
	if o == nil {
		return false
	}
	switch o.GetAPIVersion() + "/" + o.GetKind() {
	case "v1/Pod", "v1/Node", "apps/v1/Deployment", "apps/v1/StatefulSet", "apps/v1/DaemonSet", "batch/v1/Job":
		return true
	}
	return false
}

func Assess(o *unstructured.Unstructured) Report {
	r := Report{State: Unknown, Message: "Status has not been established."}
	if o == nil {
		return r
	}
	r.Generation = o.GetGeneration()
	finish := func(state State, message string) Report {
		r.State, r.Message = state, bounded(message)
		return r
	}
	if !Supports(o) {
		return finish(Unsupported, "No readiness evaluator is defined for this resource type.")
	}
	if o.GetDeletionTimestamp() != nil {
		return finish(Terminating, "Deletion was requested; the object still exists.")
	}
	convert := func(to any) error { return runtime.DefaultUnstructuredConverter.FromUnstructured(o.Object, to) }
	waiting := func() bool { return r.ObservedGeneration == 0 || r.ObservedGeneration < r.Generation }

	switch o.GetAPIVersion() + "/" + o.GetKind() {
	case "apps/v1/Deployment":
		var d appsv1.Deployment
		if err := convert(&d); err != nil {
			return finish(Unknown, "Malformed Deployment status.")
		}
		r.ObservedGeneration = d.Status.ObservedGeneration
		if d.Spec.Paused {
			return finish(Paused, "Deployment is paused; no automatic rollout completion is claimed.")
		}
		if waiting() {
			return finish(Progressing, "Waiting for the controller to observe the requested generation.")
		}
		for _, c := range d.Status.Conditions {
			if c.Type == appsv1.DeploymentProgressing && c.Status == corev1.ConditionFalse && c.Reason == "ProgressDeadlineExceeded" {
				return finish(Failed, "Deployment exceeded its progress deadline. Inspect Pods and Events.")
			}
			if c.Type == appsv1.DeploymentReplicaFailure && c.Status == corev1.ConditionTrue {
				return finish(Degraded, "Replica creation failed: "+c.Reason+". Inspect Events.")
			}
		}
		desired := int32(1)
		if d.Spec.Replicas != nil {
			desired = *d.Spec.Replicas
		}
		if desired < 0 {
			return finish(Unknown, "Invalid replica count.")
		}
		message := fmt.Sprintf("%d/%d updated, %d ready, %d available, %d total replicas.", d.Status.UpdatedReplicas, desired, d.Status.ReadyReplicas, d.Status.AvailableReplicas, d.Status.Replicas)
		if d.Status.UpdatedReplicas == desired && d.Status.Replicas == desired && d.Status.ReadyReplicas >= desired && d.Status.AvailableReplicas >= desired {
			return finish(Ready, message)
		}
		return finish(Progressing, message)

	case "apps/v1/StatefulSet":
		var s appsv1.StatefulSet
		if err := convert(&s); err != nil {
			return finish(Unknown, "Malformed StatefulSet status.")
		}
		r.ObservedGeneration = s.Status.ObservedGeneration
		if waiting() {
			return finish(Progressing, "Waiting for the controller to observe the requested generation.")
		}
		if s.Spec.UpdateStrategy.Type == appsv1.OnDeleteStatefulSetStrategyType {
			return finish(Manual, "OnDelete strategy: Pods are not automatically replaced for a template change.")
		}
		desired := int32(1)
		if s.Spec.Replicas != nil {
			desired = *s.Spec.Replicas
		}
		if desired < 0 {
			return finish(Unknown, "Invalid replica count.")
		}
		partition := int32(0)
		if s.Spec.UpdateStrategy.RollingUpdate != nil && s.Spec.UpdateStrategy.RollingUpdate.Partition != nil {
			partition = *s.Spec.UpdateStrategy.RollingUpdate.Partition
		}
		if partition < 0 {
			return finish(Unknown, "Invalid rolling update partition.")
		}
		// A nonzero start ordinal shifts the range [start, start+replicas).
		start := int32(0)
		if s.Spec.Ordinals != nil {
			start = s.Spec.Ordinals.Start
		}
		if start < 0 {
			return finish(Unknown, "Invalid starting ordinal.")
		}
		expectedUpdated := desired - max(int32(0), min(desired, partition-start))
		message := fmt.Sprintf("%d/%d ready; %d/%d target replicas updated (partition %d).", s.Status.ReadyReplicas, desired, s.Status.UpdatedReplicas, expectedUpdated, partition)
		if s.Status.Replicas != desired || s.Status.ReadyReplicas < desired || s.Status.UpdatedReplicas < expectedUpdated {
			return finish(Progressing, message)
		}
		if s.Spec.MinReadySeconds > 0 && s.Status.AvailableReplicas < desired {
			return finish(Progressing, message+" Waiting for minReadySeconds availability.")
		}
		if expectedUpdated == desired && desired > 0 && (s.Status.UpdateRevision == "" || s.Status.CurrentRevision != s.Status.UpdateRevision) {
			return finish(Progressing, message+" Waiting for revisions to converge.")
		}
		if expectedUpdated < desired {
			message += " Partitioned rollout only; lower ordinals intentionally retain their revision."
		}
		return finish(Ready, message)

	case "apps/v1/DaemonSet":
		var d appsv1.DaemonSet
		if err := convert(&d); err != nil {
			return finish(Unknown, "Malformed DaemonSet status.")
		}
		r.ObservedGeneration = d.Status.ObservedGeneration
		if waiting() {
			return finish(Progressing, "Waiting for the controller to observe the requested generation.")
		}
		if d.Spec.UpdateStrategy.Type == appsv1.OnDeleteDaemonSetStrategyType {
			return finish(Manual, "OnDelete strategy requires deliberate Pod replacement; no automatic rollout is claimed.")
		}
		desired := d.Status.DesiredNumberScheduled
		message := fmt.Sprintf("%d/%d updated, %d ready, %d available, %d misscheduled Pods.", d.Status.UpdatedNumberScheduled, desired, d.Status.NumberReady, d.Status.NumberAvailable, d.Status.NumberMisscheduled)
		if desired >= 0 && d.Status.CurrentNumberScheduled == desired && d.Status.UpdatedNumberScheduled == desired && d.Status.NumberReady >= desired && d.Status.NumberAvailable >= desired && d.Status.NumberMisscheduled == 0 {
			return finish(Ready, message)
		}
		return finish(Progressing, message)

	case "batch/v1/Job":
		var j batchv1.Job
		if err := convert(&j); err != nil {
			return finish(Unknown, "Malformed Job status.")
		}
		// Job completion is condition-based; do not manufacture an observed generation.
		if j.Spec.Suspend != nil && *j.Spec.Suspend {
			return finish(Paused, "Job is suspended.")
		}
		for _, c := range j.Status.Conditions {
			if c.Status != corev1.ConditionTrue {
				continue
			}
			if c.Type == batchv1.JobFailed {
				return finish(Failed, "Job failed: "+c.Reason)
			}
			if c.Type == batchv1.JobComplete {
				return finish(Completed, "Job completed successfully.")
			}
		}
		return finish(Progressing, fmt.Sprintf("%d active, %d succeeded, %d failed Pods; waiting for a terminal Job condition.", j.Status.Active, j.Status.Succeeded, j.Status.Failed))

	case "v1/Pod":
		var p corev1.Pod
		if err := convert(&p); err != nil {
			return finish(Unknown, "Malformed Pod status.")
		}
		if p.Status.Phase == corev1.PodSucceeded {
			return finish(Completed, "All containers completed successfully.")
		}
		if p.Status.Phase == corev1.PodFailed {
			return finish(Failed, "Pod failed: "+p.Status.Reason)
		}
		for _, statuses := range [][]corev1.ContainerStatus{p.Status.InitContainerStatuses, p.Status.ContainerStatuses} {
			for _, status := range statuses {
				if status.State.Waiting != nil {
					reason := status.State.Waiting.Reason
					switch reason {
					case "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull", "CreateContainerConfigError", "CreateContainerError", "RunContainerError":
						return finish(Degraded, status.Name+": "+reason+". Inspect container logs and Events.")
					}
				}
			}
		}
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue && p.Status.Phase == corev1.PodRunning {
				return finish(Ready, "Pod Ready condition is true.")
			}
		}
		return finish(Progressing, "Pod phase: "+string(p.Status.Phase)+"; readiness has not been confirmed.")

	case "v1/Node":
		var n corev1.Node
		if err := convert(&n); err != nil {
			return finish(Unknown, "Malformed Node status.")
		}
		ready := false
		for _, c := range n.Status.Conditions {
			if c.Type == corev1.NodeReady {
				ready = c.Status == corev1.ConditionTrue
			}
			if c.Status == corev1.ConditionTrue && (c.Type == corev1.NodeDiskPressure || c.Type == corev1.NodeMemoryPressure || c.Type == corev1.NodePIDPressure) {
				return finish(Degraded, "Node reports "+string(c.Type)+".")
			}
		}
		if !ready {
			return finish(Degraded, "Node Ready condition is not true.")
		}
		if n.Spec.Unschedulable {
			return finish(Ready, "Node is ready but cordoned (unschedulable).")
		}
		return finish(Ready, "Node is ready with no reported memory, disk or PID pressure.")
	}
	return r
}

func bounded(s string) string {
	var out strings.Builder
	out.Grow(min(len(s), 1024))
	for _, r := range s {
		if r < 32 && r != '\n' && r != '\t' {
			r = ' '
		}
		if out.Len()+utf8.RuneLen(r) > 1021 {
			out.WriteString("…")
			break
		}
		out.WriteRune(r)
	}
	return out.String()
}
