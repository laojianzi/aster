// Package testoidc is a local signed protocol fixture, not an independent IdP.
// It is imported by tests only and never authenticates production users.
package testoidc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

type grant struct{ nonce, challenge, client, redirect string }
type Provider struct {
	Server                                        *httptest.Server
	CA                                            []byte
	mu                                            sync.Mutex
	key                                           *ecdsa.PrivateKey
	grants                                        map[string]grant
	EditClaims                                    func(map[string]any)
	EditDiscovery                                 func(map[string]any)
	TokenStatus                                   int
	BrokenExchange                                bool
	TokenRequests, KeyRequests, DiscoveryRequests atomic.Int32
}

func New(t testing.TB) *Provider {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	p := &Provider{key: key, grants: map[string]grant{}}
	p.Server = httptest.NewTLSServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.Server.Close)
	p.CA = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: p.Server.Certificate().Raw})
	return p
}
func (p *Provider) Sign(claims map[string]any, extra map[jose.HeaderKey]any) string {
	opts := (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "fixture-key")
	for k, v := range extra {
		opts.WithHeader(k, v)
	}
	signer, e := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: p.key}, opts)
	if e != nil {
		panic(e)
	}
	b, _ := json.Marshal(claims)
	signed, e := signer.Sign(b)
	if e != nil {
		panic(e)
	}
	raw, e := signed.CompactSerialize()
	if e != nil {
		panic(e)
	}
	return raw
}
func (p *Provider) Keys() []byte {
	b, _ := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: "fixture-key", Algorithm: "ES256", Use: "sig"}}})
	return b
}
func (p *Provider) Claims(nonce string) map[string]any {
	return map[string]any{"iss": p.Server.URL, "sub": "fixture-subject", "aud": "aster-test", "nonce": nonce, "iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix()}
}
func (p *Provider) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		p.DiscoveryRequests.Add(1)
		d := map[string]any{"issuer": p.Server.URL, "authorization_endpoint": p.Server.URL + "/auth", "token_endpoint": p.Server.URL + "/token", "jwks_uri": p.Server.URL + "/keys", "response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"}, "id_token_signing_alg_values_supported": []string{"ES256"}, "token_endpoint_auth_methods_supported": []string{"none"}}
		if p.EditDiscovery != nil {
			p.EditDiscovery(d)
		}
		_ = json.NewEncoder(w).Encode(d)
	case "/keys":
		p.KeyRequests.Add(1)
		_, _ = w.Write(p.Keys())
	case "/token":
		p.TokenRequests.Add(1)
		if p.BrokenExchange {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		if p.TokenStatus != 0 {
			w.WriteHeader(p.TokenStatus)
			_, _ = io.WriteString(w, `{"error":"do-not-leak-token-content"}`)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if r.ParseForm() != nil || r.Method != "POST" || r.Header.Get("Authorization") != "" {
			w.WriteHeader(400)
			return
		}
		p.mu.Lock()
		g, ok := p.grants[r.Form.Get("code")]
		delete(p.grants, r.Form.Get("code"))
		p.mu.Unlock()
		hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || r.Form.Get("grant_type") != "authorization_code" || g.client != r.Form.Get("client_id") || g.redirect != r.Form.Get("redirect_uri") || base64.RawURLEncoding.EncodeToString(hash[:]) != g.challenge {
			w.WriteHeader(400)
			return
		}
		claims := p.Claims(g.nonce)
		if p.EditClaims != nil {
			p.EditClaims(claims)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"token_type": "Bearer", "access_token": "unused-access-token", "id_token": p.Sign(claims, nil)})
	default:
		w.WriteHeader(404)
	}
}

// Open emulates only the redirect roundtrip after a successful fixture login;
// it is intentionally not described as a real user-agent/IdP acceptance test.
func (p *Provider) Open(link string) error {
	u, e := url.Parse(link)
	if e != nil {
		return e
	}
	v := u.Query()
	if u.Scheme+"://"+u.Host != p.Server.URL || u.Path != "/auth" || v.Get("code_challenge_method") != "S256" || len(v.Get("code_challenge")) != 43 || len(v.Get("state")) != 43 || len(v.Get("nonce")) != 43 || v.Get("response_type") != "code" || strings.Contains(v.Get("scope"), "offline_access") {
		return io.ErrUnexpectedEOF
	}
	var random [24]byte
	_, _ = rand.Read(random[:])
	code := base64.RawURLEncoding.EncodeToString(random[:])
	p.mu.Lock()
	p.grants[code] = grant{v.Get("nonce"), v.Get("code_challenge"), v.Get("client_id"), v.Get("redirect_uri")}
	p.mu.Unlock()
	cb, e := url.Parse(v.Get("redirect_uri"))
	if e != nil {
		return e
	}
	cb.RawQuery = url.Values{"state": {v.Get("state")}, "code": {code}, "iss": {p.Server.URL}}.Encode()
	resp, e := (&http.Client{Timeout: 3 * time.Second}).Get(cb.String())
	if e != nil {
		return io.ErrUnexpectedEOF
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 {
		return io.ErrUnexpectedEOF
	}
	return nil
}
