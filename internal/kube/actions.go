package kube

import (
	"bufio"
	"context"
	"fmt"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func (b *Backend) GetObject(ctx context.Context, gvr schema.GroupVersionResource, ns, name string) (*unstructured.Unstructured, error) {
	return resourceInterface(b.dynamic, gvr, ns).Get(ctx, name, metav1.GetOptions{})
}
func (b *Backend) CanI(ctx context.Context, gvr schema.GroupVersionResource, ns, name, verb, subresource string) (bool, string, error) {
	r, err := b.typed.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authorizationv1.ResourceAttributes{Namespace: ns, Verb: verb, Group: gvr.Group, Version: gvr.Version, Resource: gvr.Resource, Subresource: subresource, Name: name}}}, metav1.CreateOptions{})
	if err != nil {
		return false, "", err
	}
	return r.Status.Allowed, r.Status.Reason, nil
}
func (b *Backend) ReadLogs(ctx context.Context, ns, pod, container string, previous, follow bool, tail int64, onLine func(string) error) error {
	if ns == "" || pod == "" || onLine == nil {
		return fmt.Errorf("logs require namespace, pod and handler")
	}
	if tail < 1 || tail > 10000 {
		return fmt.Errorf("tail must be between 1 and 10000")
	}
	stream, err := b.typed.CoreV1().Pods(ns).GetLogs(pod, &corev1.PodLogOptions{Container: container, Previous: previous, Follow: follow, TailLines: &tail, Timestamps: true}).Stream(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 4096), 256<<10)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := onLine(scanner.Text()); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("log stream ended (maximum line 256 KiB): %w", err)
	}
	return ctx.Err()
}
