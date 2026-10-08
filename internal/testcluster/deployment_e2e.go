//go:build e2e

package testcluster

import (
	"context"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/resource"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type DeploymentFixture struct {
	Config     *rest.Config
	Client     kubernetes.Interface
	Deployment *appsv1.Deployment
}

func (f DeploymentFixture) Target(session string) resource.Identity {
	return resource.Identity{SessionID: session, GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, Namespace: f.Deployment.Namespace, Name: f.Deployment.Name, UID: f.Deployment.UID}
}
func NewDeployment(t *testing.T, replicas int32) DeploymentFixture {
	t.Helper()
	cfg := Config(t)
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ns, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "aster-rollout-"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = client.CoreV1().Namespaces().Delete(ctx, ns.Name, metav1.DeleteOptions{})
	})
	no, yes, uid := false, true, int64(1000)
	pod := corev1.PodSpec{
		AutomountServiceAccountToken: &no,
		SecurityContext:              &corev1.PodSecurityContext{RunAsNonRoot: &yes, RunAsUser: &uid, RunAsGroup: &uid, FSGroup: &uid, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
		Containers: []corev1.Container{{Name: "web", Image: Image, WorkingDir: "/work", Command: []string{"/bin/sh", "-c", "printf 'aster-ready' > /work/index.html; exec httpd -f -p 8080 -h /work"},
			SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: &no, ReadOnlyRootFilesystem: &yes, Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}},
			VolumeMounts:    []corev1.VolumeMount{{Name: "work", MountPath: "/work"}},
			ReadinessProbe:  &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/", Port: intstr.FromInt32(8080)}}, PeriodSeconds: 1},
		}},
		Volumes: []corev1.Volume{{Name: "work", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}},
	}
	labels := map[string]string{"app": "aster-rollout-fixture"}
	d, err := client.AppsV1().Deployments(ns.Name).Create(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "aster-rollout-fixture"}, Spec: appsv1.DeploymentSpec{Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: labels}, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: pod}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return DeploymentFixture{cfg, client, d}
}
