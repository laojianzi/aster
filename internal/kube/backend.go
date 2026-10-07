package kube

import (
	"context"
	"fmt"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Backend struct {
	config    *rest.Config
	typed     kubernetes.Interface
	dynamic   dynamic.Interface
	discovery discovery.DiscoveryInterface
	mu        sync.RWMutex
}

func New(config *rest.Config) (*Backend, error) {
	if config == nil {
		return nil, fmt.Errorf("kube: nil config")
	}
	cfg := rest.CopyConfig(config)
	if cfg.UserAgent == "" {
		cfg.UserAgent = "aster"
	}
	typed, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("typed client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("dynamic client: %w", err)
	}
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("discovery client: %w", err)
	}
	return &Backend{config: cfg, typed: typed, dynamic: dyn, discovery: disc}, nil
}

func (b *Backend) ServerVersion() (string, error) {
	v, err := b.discovery.ServerVersion()
	if err != nil {
		return "", err
	}
	return v.GitVersion, nil
}

func (b *Backend) List(ctx context.Context, gvr schema.GroupVersionResource, namespace string, opts metav1.ListOptions) (int, string, error) {
	resource := b.dynamic.Resource(gvr)
	if namespace != "" {
		list, err := resource.Namespace(namespace).List(ctx, opts)
		if err != nil {
			return 0, "", err
		}
		return len(list.Items), list.GetResourceVersion(), nil
	}
	list, err := resource.List(ctx, opts)
	if err != nil {
		return 0, "", err
	}
	return len(list.Items), list.GetResourceVersion(), nil
}

func (b *Backend) Config() *rest.Config {
	return rest.CopyConfig(b.config)
}
