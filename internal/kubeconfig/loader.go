package kubeconfig

import (
	"fmt"
	"os"
	"sort"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type Connection struct {
	Config *rest.Config
	ContextName string
	Namespace string
}

type Options struct {
	Path string
	Context string
	Namespace string
	// Trusted permits exec plugins, proxy URLs, local file references and
	// insecure TLS declared by this context. It is never persisted by Aster.
	Trusted bool
}

type ContextInfo struct { Name, Namespace string; RequiresTrust bool }

type TrustRequiredError struct { Context string; Reasons []string }
func (e *TrustRequiredError) Error() string { return fmt.Sprintf("context %q requires explicit trust: %v", e.Context, e.Reasons) }

func loadRaw(path string) (*clientcmdapi.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if path != "" { rules.ExplicitPath = path }
	paths := rules.GetLoadingPrecedence()
	if len(paths) > 32 { return nil, fmt.Errorf("too many kubeconfig files") }
	for _, p := range paths {
		st, err := os.Stat(p)
		if err == nil && (!st.Mode().IsRegular() || st.Size() > 8<<20) { return nil, fmt.Errorf("kubeconfig must be a regular file under 8 MiB") }
	}
	return rules.Load()
}

// Contexts only parses configuration. It never constructs an authenticated
// transport, invokes executables, or reads referenced certificate/token files.
func Contexts(path string) ([]ContextInfo, string, error) {
	raw, err := loadRaw(path); if err != nil { return nil, "", err }
	out := make([]ContextInfo, 0, len(raw.Contexts))
	for name, c := range raw.Contexts {
		if c == nil { continue }
		ns := c.Namespace; if ns == "" { ns = "default" }
		out = append(out, ContextInfo{name, ns, len(trustReasons(raw, name)) > 0})
	}
	sort.Slice(out, func(i,j int) bool { return out[i].Name < out[j].Name })
	return out, raw.CurrentContext, nil
}

func trustReasons(raw *clientcmdapi.Config, name string) []string {
	c := raw.Contexts[name]; if c == nil { return nil }
	var reasons []string
	if a := raw.AuthInfos[c.AuthInfo]; a != nil {
		if a.Exec != nil { reasons = append(reasons, "external authentication command: "+a.Exec.Command) }
		if a.AuthProvider != nil { reasons = append(reasons, "legacy authentication provider") }
		if a.ClientCertificate != "" || a.ClientKey != "" || a.TokenFile != "" { reasons = append(reasons, "credential file references") }
		if a.Impersonate != "" { reasons = append(reasons, "user impersonation") }
	}
	if c := raw.Clusters[c.Cluster]; c != nil {
		if c.CertificateAuthority != "" { reasons = append(reasons, "certificate authority file reference") }
		if c.InsecureSkipTLSVerify { reasons = append(reasons, "TLS verification disabled") }
		if c.ProxyURL != "" { reasons = append(reasons, "proxy URL") }
	}
	return reasons
}

func Load(opts Options) (Connection, error) {
	raw, err := loadRaw(opts.Path)
	if err != nil { return Connection{}, fmt.Errorf("load kubeconfig: %w", err) }
	name := opts.Context; if name == "" { name = raw.CurrentContext }
	if name == "" || raw.Contexts[name] == nil { return Connection{}, fmt.Errorf("kubeconfig context %q not found", name) }
	if reasons := trustReasons(raw, name); len(reasons) > 0 && !opts.Trusted { return Connection{}, &TrustRequiredError{Context: name, Reasons: reasons} }
	overrides := &clientcmd.ConfigOverrides{CurrentContext: name}
	if opts.Namespace != "" { overrides.Context.Namespace = opts.Namespace }
	cc := clientcmd.NewNonInteractiveClientConfig(*raw, name, overrides, nil)
	cfg, err := cc.ClientConfig(); if err != nil { return Connection{}, fmt.Errorf("load connection: %w", err) }
	ns, _, err := cc.Namespace(); if err != nil { return Connection{}, err }
	return Connection{Config: cfg, ContextName: name, Namespace: ns}, nil
}
func LoadDefault() (Connection, error) { return Load(Options{}) }
