package kubeconfig

import (
	"errors"
	"github.com/laojianzi/aster/internal/credentialvault"
	"io"
	"k8s.io/client-go/tools/clientcmd"
	"os"
)

// LoadVault reads a credential-free profile, never an exec/token/certificate
// source. Even approved mixed identities are refused, rather than overridden.
// The selected raw configuration is loaded once; CA file bytes are snapshotted.
func LoadVault(opts Options) (Connection, *credentialvault.Target, error) {
	raw, err := loadRaw(opts.Path)
	if err != nil {
		return Connection{}, nil, errors.New("cannot read credential profile")
	}
	name := opts.Context
	if name == "" {
		name = raw.CurrentContext
	}
	c := raw.Contexts[name]
	if c == nil || c.AuthInfo == "" {
		return Connection{}, nil, credentialvault.ErrInvalid
	}
	a, s := raw.AuthInfos[c.AuthInfo], raw.Clusters[c.Cluster]
	if a == nil || s == nil || a.Token != "" || a.TokenFile != "" || a.Exec != nil || a.AuthProvider != nil || a.ClientCertificate != "" || a.ClientKey != "" || len(a.ClientCertificateData) > 0 || len(a.ClientKeyData) > 0 || a.Username != "" || a.Password != "" || a.Impersonate != "" || a.ImpersonateUID != "" || len(a.ImpersonateGroups) > 0 || len(a.ImpersonateUserExtra) > 0 || s.InsecureSkipTLSVerify || s.ProxyURL != "" {
		return Connection{}, nil, credentialvault.ErrInvalid
	}
	if len(trustReasons(raw, name)) > 0 {
		f, e := fingerprint(raw, name)
		if e != nil {
			return Connection{}, nil, credentialvault.ErrInvalid
		}
		if opts.TrustToken != f {
			return Connection{}, nil, &TrustRequiredError{Context: name, Reasons: []string{"CA file reference for OS credential target"}, Fingerprint: f}
		}
	}
	if s.CertificateAuthority != "" {
		if len(s.CertificateAuthorityData) > 0 {
			return Connection{}, nil, credentialvault.ErrInvalid
		}
		st, e := os.Stat(s.CertificateAuthority)
		if e != nil || !st.Mode().IsRegular() || st.Size() > 1<<20 {
			return Connection{}, nil, credentialvault.ErrInvalid
		}
		file, e := os.Open(s.CertificateAuthority)
		if e != nil {
			return Connection{}, nil, credentialvault.ErrInvalid
		}
		opened, e := file.Stat()
		if e != nil || !opened.Mode().IsRegular() {
			file.Close()
			return Connection{}, nil, credentialvault.ErrInvalid
		}
		data, e := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		file.Close()
		if e != nil || len(data) > 1<<20 {
			return Connection{}, nil, credentialvault.ErrInvalid
		}
		s.CertificateAuthorityData = data
		s.CertificateAuthority = ""
	}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: name}
	if opts.Namespace != "" {
		overrides.Context.Namespace = opts.Namespace
	}
	cc := clientcmd.NewNonInteractiveClientConfig(*raw, name, overrides, nil)
	cfg, err := cc.ClientConfig()
	if err != nil {
		return Connection{}, nil, credentialvault.ErrInvalid
	}
	ns, _, err := cc.Namespace()
	if err != nil {
		return Connection{}, nil, credentialvault.ErrInvalid
	}
	target, err := credentialvault.Bind(cfg, name, c.AuthInfo)
	if err != nil {
		return Connection{}, nil, err
	}
	return Connection{Config: cfg, ContextName: name, Namespace: ns}, target, nil
}
