// Package credentialvault stores only explicit, expiring bearer-token records in
// OS credential storage. It never falls back to files or implicitly looks up a key.
package credentialvault

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/laojianzi/aster/internal/credentialexec"
	"k8s.io/client-go/rest"
	strictjson "sigs.k8s.io/json"
)

const ServiceName = "io.aster.kubernetes.tokens.v1"
const MaxTokenBytes = 1800
const MaxRecordBytes = 2560 // Windows Credential Manager's generic blob limit.
const MaxStoredLifetime = 24 * time.Hour

var (
	ErrInvalid     = errors.New("invalid credential record or target")
	ErrUnavailable = errors.New("OS credential store unavailable or locked; no plaintext fallback")
	ErrNotFound    = errors.New("no token stored for this exact target")
	ErrExpired     = errors.New("stored token local expiry reached; explicitly store a new token")
	ErrFailed      = errors.New("OS credential operation failed; credential contents are not displayed")
	ErrUncertain   = errors.New("credential operation interrupted; store state is uncertain; review or forget explicitly")
	ErrBusy        = errors.New("credential store is busy; try again after the current operation finishes")
)

// Store keys are 64 lowercase hexadecimal characters, never caller-supplied
// service names. Implementations must not log record contents.
type Store interface {
	Put(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
}
type Target struct {
	key                   string
	config                *rest.Config
	Context, User, Server string
}

func (t *Target) Key() string {
	if t == nil {
		return ""
	}
	return t.key
}
func validKey(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func safeLabel(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			return false
		}
	}
	return true
}

// Bind requires an authentication-free reviewed HTTPS configuration. CA files
// must already have been bounded and snapshotted by kubeconfig.LoadVault.
func Bind(cfg *rest.Config, contextName, user string) (*Target, error) {
	if cfg == nil || contextName == "" || user == "" || len(contextName) > 512 || len(user) > 512 || !safeLabel(contextName) || !safeLabel(user) || !safeLabel(cfg.ServerName) {
		return nil, ErrInvalid
	}
	u, err := url.Parse(cfg.Host)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || len(cfg.Host) > 2048 || strings.ContainsAny(cfg.Host, "\r\n\x00") {
		return nil, ErrInvalid
	}
	if cfg.Insecure || cfg.CAFile != "" || len(cfg.CAData) > 1<<20 || cfg.ExecProvider != nil || cfg.AuthProvider != nil || cfg.BearerToken != "" || cfg.BearerTokenFile != "" || cfg.Username != "" || cfg.Password != "" || cfg.CertFile != "" || cfg.KeyFile != "" || len(cfg.CertData) > 0 || len(cfg.KeyData) > 0 || cfg.Impersonate.UserName != "" || cfg.Impersonate.UID != "" || len(cfg.Impersonate.Groups) > 0 || len(cfg.Impersonate.Extra) > 0 || cfg.Proxy != nil || cfg.Dial != nil || cfg.Transport != nil || cfg.WrapTransport != nil {
		return nil, ErrInvalid
	}
	if len(cfg.CAData) > 0 && !x509.NewCertPool().AppendCertsFromPEM(cfg.CAData) {
		return nil, ErrInvalid
	}
	ca := sha256.Sum256(cfg.CAData)
	data, _ := json.Marshal(struct {
		Version                         int
		Server, Name, User, TLSName, CA string
	}{1, cfg.Host, contextName, user, cfg.ServerName, hex.EncodeToString(ca[:])})
	sum := sha256.Sum256(data)
	copy := rest.CopyConfig(cfg)
	copy.CAData = append([]byte(nil), cfg.CAData...)
	copy.NextProtos = append([]string(nil), cfg.NextProtos...)
	return &Target{key: hex.EncodeToString(sum[:]), config: copy, Context: contextName, User: user, Server: cfg.Host}, nil
}

type record struct {
	Version   int       `json:"version"`
	Binding   string    `json:"binding"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func validToken(s string) bool {
	if len(s) == 0 || len(s) > MaxTokenBytes {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~+/=", rune(c))) {
			return false
		}
	}
	return true
}
func decode(data []byte, key string, now time.Time) (record, error) {
	var r record
	if len(data) > MaxRecordBytes {
		return r, ErrInvalid
	}
	warnings, err := strictjson.UnmarshalStrict(data, &r)
	if err != nil || len(warnings) > 0 || r.Version != 1 || r.Binding != key || !validKey(key) || !validToken(r.Token) || r.ExpiresAt.After(now.Add(MaxStoredLifetime)) {
		return record{}, ErrInvalid
	}
	if !now.Before(r.ExpiresAt) {
		return record{}, ErrExpired
	}
	return r, nil
}
func (t *Target) Put(ctx context.Context, s Store, token string, expires time.Time) error {
	if t == nil || s == nil || !validToken(token) || !expires.After(time.Now()) || expires.After(time.Now().Add(MaxStoredLifetime)) {
		return ErrInvalid
	}
	data, err := json.Marshal(record{1, t.key, token, expires.UTC()})
	if err != nil || len(data) > MaxRecordBytes {
		return ErrInvalid
	}
	defer clear(data)
	return s.Put(ctx, t.key, data)
}
func (t *Target) Forget(ctx context.Context, s Store) error {
	if t == nil || s == nil {
		return ErrInvalid
	}
	return s.Delete(ctx, t.key)
}
func (t *Target) Resolve(ctx context.Context, s Store) (credentialexec.Resolved, error) {
	if t == nil || s == nil {
		return credentialexec.Resolved{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return credentialexec.Resolved{}, err
	}
	data, err := s.Get(ctx, t.key)
	if err != nil {
		return credentialexec.Resolved{}, err
	}
	defer clear(data)
	r, err := decode(data, t.key, time.Now())
	if err != nil {
		return credentialexec.Resolved{}, err
	}
	expires := r.ExpiresAt
	if max := time.Now().Add(credentialexec.MaxConnectionLifetime); expires.After(max) {
		expires = max
	}
	cfg := rest.CopyConfig(t.config)
	cfg.CAData = append([]byte(nil), t.config.CAData...)
	cfg.BearerToken = r.Token
	cfg.Wrap(func(base http.RoundTripper) http.RoundTripper { return &guard{base, ctx, expires} })
	return credentialexec.Resolved{Config: cfg, ExpiresAt: expires}, nil
}

type guard struct {
	base    http.RoundTripper
	ctx     context.Context
	expires time.Time
}

func (g *guard) RoundTrip(r *http.Request) (*http.Response, error) {
	if g.ctx.Err() != nil {
		return nil, credentialexec.ErrClosed
	}
	if !time.Now().Before(g.expires) {
		return nil, credentialexec.ErrExpired
	}
	return g.base.RoundTrip(r)
}
func (g *guard) WrappedRoundTripper() http.RoundTripper { return g.base }
