package kube

import (
	"context"
	"errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
)

var optionVersion = schema.GroupVersion{Version: "v1"}
var writeParameterCodec = func() runtime.ParameterCodec {
	s := runtime.NewScheme()
	metav1.AddToGroupVersion(s, optionVersion)
	return runtime.NewParameterCodec(s)
}()

// Every mutating request uses MaxRetries(0). client-go's normal dynamic client
// can replay even PATCH/POST after a 429 or 5xx with Retry-After. An operation
// service's no-retry promise must also hold at this lower request layer.
func (b *Backend) CreateObject(ctx context.Context, gvr schema.GroupVersionResource, ns string, obj *unstructured.Unstructured, opts metav1.CreateOptions) (*unstructured.Unstructured, error) {
	if obj == nil {
		return nil, errors.New("cannot create a nil object")
	}
	path, err := writePath(gvr, ns, "")
	if err != nil {
		return nil, err
	}
	if b.writes == nil {
		return nil, errors.New("write client is not initialized")
	}
	var out unstructured.Unstructured
	err = b.writes.Post().AbsPath(path...).MaxRetries(0).Body(obj).SpecificallyVersionedParams(&opts, writeParameterCodec, optionVersion).Do(ctx).Into(&out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}
func (b *Backend) PatchObject(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, pt types.PatchType, body []byte, opts metav1.PatchOptions) (*unstructured.Unstructured, error) {
	if name == "" {
		return nil, errors.New("patch requires a resource name")
	}
	path, err := writePath(gvr, ns, name)
	if err != nil {
		return nil, err
	}
	if b.writes == nil {
		return nil, errors.New("write client is not initialized")
	}
	var out unstructured.Unstructured
	err = b.writes.Patch(pt).AbsPath(path...).MaxRetries(0).Body(body).SpecificallyVersionedParams(&opts, writeParameterCodec, optionVersion).Do(ctx).Into(&out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}
func (b *Backend) DeleteObject(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, opts metav1.DeleteOptions) error {
	if name == "" {
		return errors.New("delete requires a resource name")
	}
	path, err := writePath(gvr, ns, name)
	if err != nil {
		return err
	}
	if b.writes == nil {
		return errors.New("write client is not initialized")
	}
	return b.writes.Delete().AbsPath(path...).MaxRetries(0).Body(&opts).Do(ctx).Error()
}
func writePath(gvr schema.GroupVersionResource, ns, name string) ([]string, error) {
	if gvr.Version == "" || gvr.Resource == "" || ns == "*" {
		return nil, errors.New("write requires an explicit resource API and scope")
	}
	for _, segment := range []string{gvr.Group, gvr.Version, gvr.Resource, ns, name} {
		if len(rest.IsValidPathSegmentName(segment)) != 0 {
			return nil, errors.New("invalid resource path segment")
		}
	}
	var parts []string
	if gvr.Group == "" {
		parts = []string{"api", gvr.Version}
	} else {
		parts = []string{"apis", gvr.Group, gvr.Version}
	}
	if ns != "" {
		parts = append(parts, "namespaces", ns)
	}
	parts = append(parts, gvr.Resource)
	if name != "" {
		parts = append(parts, name)
	}
	return parts, nil
}
