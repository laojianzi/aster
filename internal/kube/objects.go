package kube

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func (b *Backend) ListObjects(ctx context.Context, gvr schema.GroupVersionResource, namespace string, opts metav1.ListOptions) ([]unstructured.Unstructured, string, error) {
	ri := resourceInterface(b.dynamic, gvr, namespace)
	list, err := ri.List(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	items := make([]unstructured.Unstructured, len(list.Items))
	for i := range list.Items {
		items[i] = *list.Items[i].DeepCopy()
	}
	return items, list.GetResourceVersion(), nil
}
