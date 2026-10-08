package kube

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

type Backend struct {
	config *rest.Config
	typed kubernetes.Interface
	dynamic dynamic.Interface
	discovery discovery.DiscoveryInterface
}

func New(config *rest.Config) (*Backend, error) {
	if config == nil { return nil, fmt.Errorf("kube: nil config") }
	cfg := rest.CopyConfig(config)
	if cfg.UserAgent == "" { cfg.UserAgent = "aster" }
	// One request budget and connection pool for all clients in this identity.
	if cfg.RateLimiter == nil { qps := cfg.QPS; if qps <= 0 { qps = 20 }; burst := cfg.Burst; if burst <= 0 { burst = 40 }; cfg.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(qps,burst) }
	httpClient,err := rest.HTTPClientFor(cfg); if err != nil { return nil,fmt.Errorf("transport: %w",err) }
	typed,err := kubernetes.NewForConfigAndClient(cfg,httpClient); if err != nil { return nil,fmt.Errorf("typed client: %w",err) }
	dyn,err := dynamic.NewForConfigAndClient(cfg,httpClient); if err != nil { return nil,fmt.Errorf("dynamic client: %w",err) }
	disc,err := discovery.NewDiscoveryClientForConfigAndClient(cfg,httpClient); if err != nil { return nil,fmt.Errorf("discovery client: %w",err) }
	return &Backend{config:cfg,typed:typed,dynamic:dyn,discovery:disc},nil
}
func (b *Backend) ServerVersion() (string,error) { v,err := b.discovery.ServerVersion(); if err != nil { return "",err }; return v.GitVersion,nil }
func (b *Backend) List(ctx context.Context,gvr schema.GroupVersionResource,ns string,opts metav1.ListOptions) (int,string,error) {
	list,err := resourceInterface(b.dynamic,gvr,ns).List(ctx,opts)
	if err != nil { return 0,"",err }; return len(list.Items),list.GetResourceVersion(),nil
}
func (b *Backend) Config() *rest.Config { return rest.CopyConfig(b.config) }
