package kube

import (
	"context"
	"fmt"
	"net/http"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

// backendIdentity binds inert preparations without retaining transport credentials.
type backendIdentity struct{ marker byte }

// Capacity metadata is known only for the token bucket constructed by New.
// Caller-supplied RateLimiters expose no reliable burst/capacity contract.
type backendRateLimit struct {
	qps   float32
	burst int
}

type Backend struct {
	identity           *backendIdentity
	helmJournalBinding string
	metricsHTTP        *http.Client
	config             *rest.Config
	typed              kubernetes.Interface
	dynamic            dynamic.Interface
	discovery          discovery.DiscoveryInterface
	writes             rest.Interface
	ownedRateLimit     *backendRateLimit
}

func New(config *rest.Config) (*Backend, error) {
	if config == nil {
		return nil, fmt.Errorf("kube: nil config")
	}
	if config.AuthProvider != nil {
		return nil, fmt.Errorf("kube: legacy authentication providers are unsupported; use a reviewed exec credential plugin")
	}
	if config.ExecProvider != nil {
		return nil, fmt.Errorf("kube: exec credentials must be resolved through the bounded connection authenticator")
	}
	cfg := copyConnectionConfig(config)
	journalBinding := helmUninstallConnectionBinding(cfg)
	cfg.Wrap(func(base http.RoundTripper) http.RoundTripper { return rejectAPIRedirects{base: base} })
	if cfg.UserAgent == "" {
		cfg.UserAgent = "aster"
	}
	// One request budget and connection pool for all clients in this identity.
	var ownedRateLimit *backendRateLimit
	if cfg.RateLimiter == nil {
		qps := cfg.QPS
		if qps <= 0 {
			qps = 20
		}
		burst := cfg.Burst
		if burst <= 0 {
			burst = 40
		}
		cfg.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(qps, burst)
		ownedRateLimit = &backendRateLimit{qps: qps, burst: burst}
	}
	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("transport: %w", err)
	}
	typed, err := kubernetes.NewForConfigAndClient(cfg, httpClient)
	if err != nil {
		return nil, fmt.Errorf("typed client: %w", err)
	}
	dyn, err := dynamic.NewForConfigAndClient(cfg, httpClient)
	if err != nil {
		return nil, fmt.Errorf("dynamic client: %w", err)
	}
	disc, err := discovery.NewDiscoveryClientForConfigAndClient(cfg, httpClient)
	if err != nil {
		return nil, fmt.Errorf("discovery client: %w", err)
	}
	writeConfig := dynamic.ConfigFor(cfg)
	writeConfig.GroupVersion = nil
	writeConfig.APIPath = "/api"
	writes, err := rest.UnversionedRESTClientForConfigAndClient(writeConfig, httpClient)
	if err != nil {
		return nil, fmt.Errorf("write client: %w", err)
	}
	return &Backend{identity: &backendIdentity{marker: 1}, helmJournalBinding: journalBinding, metricsHTTP: httpClient, config: cfg, typed: typed, dynamic: dyn, discovery: disc, writes: writes, ownedRateLimit: ownedRateLimit}, nil
}
func (b *Backend) ServerVersion() (string, error) {
	v, err := b.discovery.ServerVersion()
	if err != nil {
		return "", err
	}
	return v.GitVersion, nil
}
func (b *Backend) List(ctx context.Context, gvr schema.GroupVersionResource, ns string, opts metav1.ListOptions) (int, string, error) {
	list, err := resourceInterface(b.dynamic, gvr, ns).List(ctx, opts)
	if err != nil {
		return 0, "", err
	}
	return len(list.Items), list.GetResourceVersion(), nil
}
func (b *Backend) Config() *rest.Config { return copyConnectionConfig(b.config) }
