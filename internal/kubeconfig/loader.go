package kubeconfig

import (
	"fmt"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Connection struct {
	Config      *rest.Config
	ContextName string
	Namespace   string
}

func LoadDefault() (Connection, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	deferred := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
	cfg, err := deferred.ClientConfig()
	if err != nil {
		return Connection{}, fmt.Errorf("load kubeconfig: %w", err)
	}
	ns, _, err := deferred.Namespace()
	if err != nil {
		return Connection{}, fmt.Errorf("load namespace: %w", err)
	}
	raw, err := deferred.RawConfig()
	if err != nil {
		return Connection{}, fmt.Errorf("load raw kubeconfig: %w", err)
	}
	return Connection{Config: cfg, ContextName: raw.CurrentContext, Namespace: ns}, nil
}
