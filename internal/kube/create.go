package kube

import (
 "context"
 metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
 "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
 "k8s.io/apimachinery/pkg/runtime/schema"
)

func(b *Backend)CreateObject(ctx context.Context,gvr schema.GroupVersionResource,ns string,obj *unstructured.Unstructured,opts metav1.CreateOptions)(*unstructured.Unstructured,error){return resourceInterface(b.dynamic,gvr,ns).Create(ctx,obj,opts)}
