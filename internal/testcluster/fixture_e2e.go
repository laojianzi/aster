//go:build e2e

// Package testcluster provisions disposable fixtures in the explicit E2E cluster.
// It is excluded from ordinary application builds and unit tests.
package testcluster

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/resource"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Version from Kubernetes v1.37.0's official E2E image manifest.
const Image = "registry.k8s.io/e2e-test-images/busybox:1.37.0-2"
const Message = "aster-stream-ok"
type Fixture struct { Config *rest.Config; Client kubernetes.Interface; Pod *corev1.Pod }
func (f Fixture) Target(session string) resource.Identity {
	return resource.Identity{SessionID:session,GVR:schema.GroupVersionResource{Version:"v1",Resource:"pods"},Namespace:f.Pod.Namespace,Name:f.Pod.Name,UID:f.Pod.UID}
}
func NewHTTPPod(t *testing.T) Fixture {
	t.Helper()
	cfg, err := clientcmd.BuildConfigFromFlags("",clientcmd.RecommendedHomeFile)
	if err != nil { t.Fatal(err) }
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil { t.Fatal(err) }
	ctx,cancel := context.WithTimeout(context.Background(),3*time.Minute); defer cancel()
	ns, err := client.CoreV1().Namespaces().Create(ctx,&corev1.Namespace{ObjectMeta:metav1.ObjectMeta{GenerateName:"aster-stream-"}},metav1.CreateOptions{})
	if err != nil { t.Fatal(err) }
	t.Cleanup(func(){ctx,cancel:=context.WithTimeout(context.Background(),20*time.Second);defer cancel();_ = client.CoreV1().Namespaces().Delete(ctx,ns.Name,metav1.DeleteOptions{})})
	no,yes,uid := false,true,int64(1000)
	pod,err := client.CoreV1().Pods(ns.Name).Create(ctx,&corev1.Pod{
		ObjectMeta:metav1.ObjectMeta{Name:"aster-stream-fixture"},
		Spec:corev1.PodSpec{
			AutomountServiceAccountToken:&no,
			SecurityContext:&corev1.PodSecurityContext{RunAsNonRoot:&yes,RunAsUser:&uid,RunAsGroup:&uid,FSGroup:&uid,SeccompProfile:&corev1.SeccompProfile{Type:corev1.SeccompProfileTypeRuntimeDefault}},
			Containers:[]corev1.Container{{
				Name:"http",Image:Image,WorkingDir:"/work",
				Command:[]string{"/bin/sh","-c","printf '"+Message+"' > /work/index.html; echo aster-log-ready; exec httpd -f -p 8080 -h /work"},
				SecurityContext:&corev1.SecurityContext{AllowPrivilegeEscalation:&no,ReadOnlyRootFilesystem:&yes,Capabilities:&corev1.Capabilities{Drop:[]corev1.Capability{"ALL"}}},
				Ports:[]corev1.ContainerPort{{Name:"http",ContainerPort:8080}},
				VolumeMounts:[]corev1.VolumeMount{{Name:"work",MountPath:"/work"}},
				ReadinessProbe:&corev1.Probe{ProbeHandler:corev1.ProbeHandler{HTTPGet:&corev1.HTTPGetAction{Path:"/",Port:intstr.FromInt32(8080)}},PeriodSeconds:1},
			}},
			Volumes:[]corev1.Volume{{Name:"work",VolumeSource:corev1.VolumeSource{EmptyDir:&corev1.EmptyDirVolumeSource{}}}},
		},
	},metav1.CreateOptions{})
	if err != nil { t.Fatal(err) }
	for {
		pod,err = client.CoreV1().Pods(ns.Name).Get(ctx,pod.Name,metav1.GetOptions{})
		if err != nil { t.Fatal(err) }
		for _,condition := range pod.Status.Conditions { if condition.Type==corev1.PodReady && condition.Status==corev1.ConditionTrue { return Fixture{cfg,client,pod} } }
		select{case <-ctx.Done():t.Fatal(fmt.Sprintf("fixture did not become ready: %+v",pod.Status));case <-time.After(250*time.Millisecond):}
	}
}
