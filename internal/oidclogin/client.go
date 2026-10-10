// Package oidclogin implements explicit, ephemeral native OIDC code login.
// Discovery is a separate review step; renewal is explicit and memory-only.
// No credential persistence is performed. All network endpoints are HTTPS on the reviewed issuer's origin.
package oidclogin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/laojianzi/aster/internal/credentialexec"
	"github.com/laojianzi/aster/internal/credentialvault"
	"k8s.io/client-go/rest"
)

// Options contains public configuration, not credentials. TargetKey must come
// from a reviewed credential-free kubeconfig target. A fixed callback port is
// optional for providers which require pre-registration; zero chooses a port.
type Options struct {
	Issuer, ClientID, TargetKey string
	CAPEM                       []byte
	Port                        uint16
	// AllowRenewal requests offline_access and consent. False discards refresh tokens.
	AllowRenewal bool
}
type Endpoints struct{ Issuer, ClientID, Authorization, Token, Keys, TrustSHA256 string }
type Review struct {
	mu        sync.Mutex
	used      bool
	opts      Options
	endpoints Endpoints
	client    *http.Client
	expires   time.Time
}

func (r *Review) Endpoints() Endpoints { return r.endpoints }
func (r *Review) Close()               { r.mu.Lock(); r.used = true; r.mu.Unlock(); r.client.CloseIdleConnections() }
func safeText(s string, max int) bool {
	if s == "" || len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return false
		}
	}
	return true
}
func endpoint(s string) (*url.URL, error) {
	if !safeText(s, 2048) || strings.ContainsAny(s, "\\ ") {
		return nil, ErrConfiguration
	}
	u, e := url.Parse(s)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" {
		return nil, ErrConfiguration
	}
	return u, nil
}
func newClient(ca []byte) (*http.Client, error) {
	if len(ca) > MaxCABytes {
		return nil, ErrConfiguration
	}
	var roots *x509.CertPool
	if len(ca) > 0 {
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(ca) {
			return nil, ErrConfiguration
		}
	}
	tr := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: RequestTimeout}).DialContext,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, TLSHandshakeTimeout: RequestTimeout,
		ResponseHeaderTimeout: RequestTimeout, MaxResponseHeaderBytes: 16 << 10, DisableKeepAlives: true, DisableCompression: true}
	return &http.Client{Transport: tr, Timeout: RequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

// request never retries, follows redirects, reports raw errors, or accepts a
// compressed/unbounded response. The POST has no GetBody or idempotency header.
func request(ctx context.Context, c *http.Client, method, path string, body io.Reader) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, method, path, body)
	if e != nil {
		return nil, ErrConfiguration
	}
	req.GetBody = nil
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Aster-OIDC")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, e := c.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ErrInterrupted
		}
		return nil, ErrTransport
	}
	defer resp.Body.Close()
	mt, _, e := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode != http.StatusOK || e != nil || mt != "application/json" || resp.Header.Get("Content-Encoding") != "" || resp.ContentLength > MaxJSONBytes {
		return nil, ErrResponse
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, MaxJSONBytes+1))
	if e != nil || len(data) > MaxJSONBytes {
		return nil, ErrResponse
	}
	if ctx.Err() != nil {
		clear(data)
		return nil, ErrInterrupted
	}
	return data, nil
}
func Discover(ctx context.Context, opts Options) (*Review, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, ErrInterrupted
	}
	u, e := endpoint(opts.Issuer)
	key, e2 := hex.DecodeString(opts.TargetKey)
	if e != nil || (opts.Port != 0 && opts.Port < 1024) || !safeText(opts.ClientID, 256) || e2 != nil || len(key) != 32 || hex.EncodeToString(key) != opts.TargetKey {
		return nil, ErrConfiguration
	}
	opts.CAPEM = append([]byte(nil), opts.CAPEM...)
	c, e := newClient(opts.CAPEM)
	if e != nil {
		return nil, e
	}
	defer c.CloseIdleConnections()
	data, e := request(ctx, c, "GET", strings.TrimSuffix(opts.Issuer, "/")+"/.well-known/openid-configuration", nil)
	if e != nil {
		return nil, e
	}
	var doc map[string]json.RawMessage
	if decodeJSON(ctx, data, &doc) != nil {
		return nil, ErrDiscovery
	}
	text := func(k string) string { var s string; _ = json.Unmarshal(doc[k], &s); return s }
	array := func(k string) []string { var s []string; _ = json.Unmarshal(doc[k], &s); return s }
	if text("issuer") != opts.Issuer || !slices.Contains(array("response_types_supported"), "code") || !slices.Contains(array("code_challenge_methods_supported"), "S256") || !slices.Contains(array("id_token_signing_alg_values_supported"), "RS256") && !slices.Contains(array("id_token_signing_alg_values_supported"), "ES256") {
		return nil, ErrDiscovery
	}
	if opts.AllowRenewal && (!slices.Contains(array("grant_types_supported"), "refresh_token") || !slices.Contains(array("scopes_supported"), "offline_access")) {
		return nil, ErrDiscovery
	}
	for _, field := range []string{"authorization_endpoint", "token_endpoint", "jwks_uri"} {
		x, e := endpoint(text(field))
		if e != nil || x.Host != u.Host {
			return nil, ErrDiscovery
		}
	}
	trust := sha256.Sum256(opts.CAPEM)
	return &Review{opts: opts, client: c, expires: time.Now().Add(ReviewLifetime), endpoints: Endpoints{opts.Issuer, opts.ClientID, text("authorization_endpoint"), text("token_endpoint"), text("jwks_uri"), hex.EncodeToString(trust[:])}}, nil
}
func randomValue() (string, error) {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", ErrInterrupted
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

type IdentityInfo struct {
	Issuer, Subject string
	ExpiresAt       time.Time
}

// Identity is opaque and single-use. String/GoString never format token data.
type Identity struct {
	mu         sync.Mutex
	info       IdentityInfo
	key, token string
	deadline   time.Time
	used       bool
	resolved   bool
	renewal    *Renewal
}

func (*Identity) String() string       { return "OIDC identity (credentials redacted)" }
func (*Identity) GoString() string     { return "OIDC identity (credentials redacted)" }
func (i *Identity) Info() IdentityInfo { i.mu.Lock(); defer i.mu.Unlock(); return i.info }
func (i *Identity) Close() {
	if i == nil {
		return
	}
	i.mu.Lock()
	i.used = true
	i.token = ""
	if i.renewal != nil {
		i.renewal.Close()
		i.renewal = nil
	}
	i.mu.Unlock()
}

// Resolve checks freshly loaded, credential-free configuration before exposing
// the ID token to client-go. A successful identity is not a permission grant.
func (i *Identity) Resolve(ctx context.Context, cfg *rest.Config, contextName, user string) (credentialexec.Resolved, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.used {
		return credentialexec.Resolved{}, ErrUsed
	}
	i.used = true
	token := i.token
	i.token = ""
	if ctx == nil || ctx.Err() != nil {
		return credentialexec.Resolved{}, ErrInterrupted
	}
	if !time.Now().Before(i.deadline) || !time.Now().Before(i.info.ExpiresAt) {
		return credentialexec.Resolved{}, ErrExpired
	}
	t, e := credentialvault.Bind(cfg, contextName, user)
	if e != nil || t.Key() != i.key {
		return credentialexec.Resolved{}, ErrConfiguration
	}
	copy := rest.CopyConfig(cfg)
	copy.CAData = append([]byte(nil), cfg.CAData...)
	copy.NextProtos = append([]string(nil), cfg.NextProtos...)
	copy.BearerToken = token
	copy.Proxy = func(*http.Request) (*url.URL, error) { return nil, nil }
	expires := i.info.ExpiresAt
	if limit := time.Now().Add(credentialexec.MaxConnectionLifetime); expires.After(limit) {
		expires = limit
	}
	if i.renewal != nil && expires.After(i.renewal.until) {
		expires = i.renewal.until
	}
	i.resolved = true
	copy.Wrap(func(base http.RoundTripper) http.RoundTripper { return &leaseGuard{base, ctx, expires} })
	return credentialexec.Resolved{Config: copy, ExpiresAt: expires}, nil
}

// The UI cancels active streams; copied Config values also reject new requests
// after disconnection/expiry rather than silently retaining a bearer identity.
type leaseGuard struct {
	base    http.RoundTripper
	ctx     context.Context
	expires time.Time
}

func (g *leaseGuard) RoundTrip(r *http.Request) (*http.Response, error) {
	if g.ctx.Err() != nil {
		return nil, credentialexec.ErrClosed
	}
	if !time.Now().Before(g.expires) {
		return nil, credentialexec.ErrExpired
	}
	return g.base.RoundTrip(r)
}
func (g *leaseGuard) WrappedRoundTripper() http.RoundTripper { return g.base }
