package kube

import (
	"context"
	"errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ListPage preserves the continuation token. Relationship queries must have an
// explicit namespace and page budget; they never scan a whole cluster implicitly.
func (b *Backend) ListPage(ctx context.Context, gvr schema.GroupVersionResource, ns string, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	if ns == "" || ns == "*" || opts.Limit < 1 || opts.Limit > 200 || opts.Watch {
		return nil, errors.New("relationship pages require an explicit namespace and limit in [1,200]")
	}
	page, err := resourceInterface(b.dynamic, gvr, ns).List(ctx, opts)
	if err != nil {
		return nil, err
	}
	return page.DeepCopy(), nil
}
