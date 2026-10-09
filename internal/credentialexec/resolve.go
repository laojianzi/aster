// Package credentialexec resolves trusted, non-interactive exec credentials once
// for an explicitly bounded connection. It intentionally does not silently renew
// credentials or use client-go's process-global exec authenticator cache.
package credentialexec

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	strictjson "sigs.k8s.io/json"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	authapi "k8s.io/client-go/pkg/apis/clientauthentication"
	"k8s.io/client-go/pkg/apis/clientauthentication/install"
	authv1 "k8s.io/client-go/pkg/apis/clientauthentication/v1"
	authplugin "k8s.io/client-go/plugin/pkg/client/auth/exec"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const InvocationTimeout = 30 * time.Second
const MaxConnectionLifetime = 15 * time.Minute
const MaxStdoutBytes = 256 << 10
const MaxStderrBytes = 16 << 10

var ErrExpired = errors.New("exec credential session expired; reconnect to authenticate again")
var ErrClosed = errors.New("exec credential connection closed")
var ErrOutputLimit = errors.New("authentication command exceeded its output budget")
var ErrCommandFailed = errors.New("authentication command failed; its output is not displayed")
var ErrInvalidCredential = errors.New("authentication command returned invalid or expired credentials")
var ErrTimeout = errors.New("authentication command timed out")

// Resolved contains a credential snapshot. ExpiresAt is zero for non-exec input.
// The desktop must bind all watches and streams to the earlier of ExpiresAt and
// connection cancellation. The transport guard also rejects new calls through
// old Config copies after expiry/closure. No credential is written to disk.
type Resolved struct {
	Config    *rest.Config
	ExpiresAt time.Time
}

// Resolve must only receive configuration after its trust decision. A config
// with an exec plugin produces static credentials valid for this connection;
// it never retains ExecProvider or delegates process creation to client-go.
func Resolve(ctx context.Context, source *rest.Config) (Resolved, error) {
	return resolve(ctx, source, InvocationTimeout, MaxConnectionLifetime)
}
func resolve(ctx context.Context, source *rest.Config, timeout, lifetime time.Duration) (Resolved, error) {
	if ctx == nil || source == nil {
		return Resolved{}, errors.New("authentication requires a context and configuration")
	}
	if err := ctx.Err(); err != nil {
		return Resolved{}, err
	}
	if source.AuthProvider != nil {
		return Resolved{}, errors.New("legacy authentication providers are unsupported; migrate to an exec credential plugin")
	}
	if source.ExecProvider == nil {
		return Resolved{Config: source}, nil
	}
	spec := source.ExecProvider.DeepCopy()
	if spec.APIVersion != "client.authentication.k8s.io/v1" && spec.APIVersion != "client.authentication.k8s.io/v1beta1" {
		return Resolved{}, errors.New("unsupported exec credential API version")
	}
	if spec.InteractiveMode == clientcmdapi.AlwaysExecInteractiveMode || (spec.InteractiveMode != "" && spec.InteractiveMode != clientcmdapi.NeverExecInteractiveMode && spec.InteractiveMode != clientcmdapi.IfAvailableExecInteractiveMode) || (spec.APIVersion == "client.authentication.k8s.io/v1" && spec.InteractiveMode == "") {
		return Resolved{}, errors.New("desktop authentication requires a non-interactive exec credential plugin")
	}
	if source.AuthProvider != nil || source.BearerToken != "" || source.BearerTokenFile != "" || source.Username != "" || source.Password != "" || source.CertFile != "" || source.KeyFile != "" || len(source.CertData) > 0 || len(source.KeyData) > 0 {
		return Resolved{}, errors.New("exec credentials cannot be combined with another credential source")
	}
	if err := authplugin.ValidatePluginPolicy(spec.PluginPolicy); err != nil {
		return Resolved{}, errors.New("invalid exec plugin policy")
	}
	if spec.PluginPolicy.PolicyType == clientcmdapi.PluginPolicyDenyAll {
		return Resolved{}, errors.New("exec credential plugins are disabled by policy")
	}
	if spec.PluginPolicy.PolicyType == clientcmdapi.PluginPolicyAllowlist {
		found := false
		for _, entry := range spec.PluginPolicy.Allowlist {
			if entry.Command == spec.Command {
				found = true
			}
		}
		if !found {
			return Resolved{}, errors.New("exec command is not allowed by policy")
		}
	}
	var cluster *authapi.Cluster
	if spec.ProvideClusterInfo {
		var err error
		cluster, err = rest.ConfigToExecCluster(source)
		if err != nil {
			return Resolved{}, errors.New("cannot prepare exec cluster information")
		}
		cluster.Config = spec.Config
	}
	s := runtime.NewScheme()
	install.Install(s)
	data, err := runtime.Encode(serializer.NewCodecFactory(s).LegacyCodec(schema.FromAPIVersionAndKind(spec.APIVersion, "ExecCredential").GroupVersion()), &authapi.ExecCredential{Spec: authapi.ExecCredentialSpec{Interactive: false, Cluster: cluster}})
	if err != nil || len(data) > 24<<10 {
		return Resolved{}, errors.New("exec cluster information exceeds its budget")
	}
	env, err := environment(spec.Env, string(data))
	if err != nil {
		return Resolved{}, err
	}
	output, err := run(ctx, spec.Command, spec.Args, env, timeout)
	if err != nil {
		return Resolved{}, err
	}
	credential, expires, err := decode(output, spec.APIVersion, time.Now(), lifetime)
	if err != nil {
		return Resolved{}, err
	}
	seed := *source
	seed.ExecProvider = nil
	cfg := rest.CopyConfig(&seed)
	cfg.CAData = append([]byte(nil), source.CAData...)
	cfg.NextProtos = append([]string(nil), source.NextProtos...)
	cfg.Impersonate.Groups = append([]string(nil), source.Impersonate.Groups...)
	if source.Impersonate.Extra != nil {
		cfg.Impersonate.Extra = map[string][]string{}
		for k, v := range source.Impersonate.Extra {
			cfg.Impersonate.Extra[k] = append([]string(nil), v...)
		}
	}
	cfg.BearerToken = credential.Token
	cfg.CertData = []byte(credential.ClientCertificateData)
	cfg.KeyData = []byte(credential.ClientKeyData)
	cfg.Wrap(func(base http.RoundTripper) http.RoundTripper {
		return &leaseTransport{base: base, ctx: ctx, expires: expires}
	})
	return Resolved{cfg, expires}, nil
}

func decode(data []byte, version string, now time.Time, lifetime time.Duration) (*authv1.ExecCredentialStatus, time.Time, error) {
	// v1 and v1beta1 share these JSON fields. Strict decoding rejects unknown
	// fields and trailing documents rather than trusting an ambiguous payload.
	var result authv1.ExecCredential
	if warnings, err := strictjson.UnmarshalStrict(data, &result); err != nil || len(warnings) != 0 {
		return nil, time.Time{}, ErrInvalidCredential
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil {
		return nil, time.Time{}, ErrInvalidCredential
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, time.Time{}, ErrInvalidCredential
	}
	if result.APIVersion != version || result.Kind != "ExecCredential" || result.Status == nil {
		return nil, time.Time{}, ErrInvalidCredential
	}
	st := result.Status
	token := st.Token != ""
	cert := st.ClientCertificateData != ""
	key := st.ClientKeyData != ""
	if cert != key || token == cert {
		return nil, time.Time{}, ErrInvalidCredential
	}
	if token {
		if len(st.Token) > 16<<10 {
			return nil, time.Time{}, ErrInvalidCredential
		}
		for _, b := range []byte(st.Token) {
			if b < 33 || b > 126 {
				return nil, time.Time{}, ErrInvalidCredential
			}
		}
	}
	expires := now.Add(lifetime)
	if st.ExpirationTimestamp != nil {
		if !st.ExpirationTimestamp.Time.After(now) {
			return nil, time.Time{}, ErrInvalidCredential
		}
		if st.ExpirationTimestamp.Time.Before(expires) {
			expires = st.ExpirationTimestamp.Time
		}
	}
	if cert {
		pair, err := tls.X509KeyPair([]byte(st.ClientCertificateData), []byte(st.ClientKeyData))
		if err != nil || len(pair.Certificate) == 0 {
			return nil, time.Time{}, ErrInvalidCredential
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil || leaf.NotBefore.After(now) || !leaf.NotAfter.After(now) {
			return nil, time.Time{}, ErrInvalidCredential
		}
		if leaf.NotAfter.Before(expires) {
			expires = leaf.NotAfter
		}
	}
	return st, expires, nil
}

type leaseTransport struct {
	base    http.RoundTripper
	ctx     context.Context
	expires time.Time
}

func (t *leaseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.ctx.Err() != nil {
		return nil, ErrClosed
	}
	if !time.Now().Before(t.expires) {
		return nil, ErrExpired
	}
	return t.base.RoundTrip(req)
}
func (t *leaseTransport) WrappedRoundTripper() http.RoundTripper { return t.base }
