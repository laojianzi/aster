package oidclogin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/laojianzi/aster/internal/credentialvault"
	"github.com/laojianzi/aster/internal/testoidc"
	"k8s.io/client-go/rest"
)

func reviewed(t *testing.T, p *testoidc.Provider) *Review {
	t.Helper()
	target, e := credentialvault.Bind(&rest.Config{Host: "https://cluster.invalid"}, "context", "user")
	if e != nil {
		t.Fatal(e)
	}
	r, e := Discover(context.Background(), Options{Issuer: p.Server.URL, ClientID: "aster-test", TargetKey: target.Key(), CAPEM: p.CA})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(r.Close)
	return r
}
func TestOIDCCodePKCEAndSingleUseIdentity(t *testing.T) {
	p := testoidc.New(t)
	r := reviewed(t, p)
	id, e := r.Login(context.Background(), p.Open)
	if e != nil {
		t.Fatal(e)
	}
	defer id.Close()
	if id.Info().Subject != "fixture-subject" || p.TokenRequests.Load() != 1 || p.KeyRequests.Load() != 1 {
		t.Fatal("wrong identity or exchange count")
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", id, id), "eyJ") {
		t.Fatal("token formatting")
	}
	result, e := id.Resolve(context.Background(), &rest.Config{Host: "https://cluster.invalid"}, "context", "user")
	if e != nil || !strings.HasPrefix(result.Config.BearerToken, "ey") || result.ExpiresAt.IsZero() {
		t.Fatal("resolve failed", e)
	}
	if _, e = id.Resolve(context.Background(), &rest.Config{Host: "https://cluster.invalid"}, "context", "user"); !errors.Is(e, ErrUsed) {
		t.Fatal("identity reused")
	}
	if _, e = r.Login(context.Background(), p.Open); !errors.Is(e, ErrUsed) {
		t.Fatal("review reused")
	}
}
func TestOIDCRejectsClaimAndSignatureConfusion(t *testing.T) {
	p := testoidc.New(t)
	cases := map[string]func(map[string]any){
		"issuer": func(c map[string]any) { c["iss"] = "https://other.invalid" }, "audience": func(c map[string]any) { c["aud"] = "other" }, "extra-audience": func(c map[string]any) { c["aud"] = []string{"aster-test", "other"} },
		"azp": func(c map[string]any) { c["azp"] = "other" }, "nonce": func(c map[string]any) { c["nonce"] = "wrong" }, "expired": func(c map[string]any) { c["exp"] = time.Now().Add(-time.Second).Unix() }, "future-iat": func(c map[string]any) { c["iat"] = time.Now().Add(time.Hour).Unix() },
		"missing-iat": func(c map[string]any) { delete(c, "iat") }, "string-exp": func(c map[string]any) { c["exp"] = fmt.Sprint(time.Now().Add(time.Hour).Unix()) }, "future-nbf": func(c map[string]any) { c["nbf"] = time.Now().Add(time.Hour).Unix() }, "subject-control": func(c map[string]any) { c["sub"] = "user\nadmin" }, "access-hash": func(c map[string]any) { c["at_hash"] = "wrong" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			c := p.Claims("nonce")
			edit(c)
			if _, e := validateIdentity(context.Background(), p.Server.URL, "aster-test", "nonce", p.Sign(c, nil), "access", p.Keys(), time.Now()); e == nil {
				t.Fatal("accepted invalid identity")
			}
		})
	}
	for _, header := range []map[jose.HeaderKey]any{{"jku": "https://other.invalid/keys"}, {"jwk": "injected"}, {"crit": []string{"unknown"}}} {
		if _, e := validateIdentity(context.Background(), p.Server.URL, "aster-test", "nonce", p.Sign(p.Claims("nonce"), header), "access", p.Keys(), time.Now()); e == nil {
			t.Fatal("accepted unsafe header")
		}
	}
	other := testoidc.New(t)
	if _, e := validateIdentity(context.Background(), p.Server.URL, "aster-test", "nonce", p.Sign(p.Claims("nonce"), nil), "access", other.Keys(), time.Now()); e == nil {
		t.Fatal("wrong signing key accepted")
	}
}
func TestOIDCDiscoveryRejectsUnreviewedEndpoints(t *testing.T) {
	for _, kind := range []string{"foreign-origin", "http", "query", "issuer", "pkce"} {
		t.Run(kind, func(t *testing.T) {
			p := testoidc.New(t)
			p.EditDiscovery = func(d map[string]any) {
				switch kind {
				case "foreign-origin":
					d["token_endpoint"] = "https://other.invalid/token"
				case "http":
					d["jwks_uri"] = "http://127.0.0.1/keys"
				case "query":
					d["authorization_endpoint"] = p.Server.URL + "/auth?client_id=other"
				case "issuer":
					d["issuer"] = "https://other.invalid"
				case "pkce":
					delete(d, "code_challenge_methods_supported")
				}
			}
			_, e := Discover(context.Background(), Options{Issuer: p.Server.URL, ClientID: "aster-test", TargetKey: strings.Repeat("a", 64), CAPEM: p.CA})
			if e == nil || p.TokenRequests.Load() != 0 || p.KeyRequests.Load() != 0 {
				t.Fatal("unreviewed access")
			}
		})
	}
	p := testoidc.New(t)
	if _, e := Discover(context.Background(), Options{Issuer: p.Server.URL, ClientID: "aster-test", TargetKey: strings.Repeat("a", 64)}); e == nil {
		t.Fatal("untrusted TLS accepted")
	}
}
func TestOIDCInterruptedExchangeNeverReplays(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(fmt.Sprint(broken), func(t *testing.T) {
			p := testoidc.New(t)
			p.TokenStatus = 429
			p.BrokenExchange = broken
			r := reviewed(t, p)
			id, e := r.Login(context.Background(), p.Open)
			if e == nil || id != nil || p.TokenRequests.Load() != 1 || strings.Contains(e.Error(), "do-not-leak") {
				t.Fatal("replayed or leaked exchange")
			}
		})
	}
}
func TestOIDCCallbackRejectsForgeryAndClosesOnCancel(t *testing.T) {
	p := testoidc.New(t)
	r := reviewed(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var callback string
	_, e := r.Login(ctx, func(link string) error {
		u, _ := url.Parse(link)
		callback = u.Query().Get("redirect_uri")
		cb, _ := url.Parse(callback)
		for _, query := range []string{"state=forged&code=injected", "state=x&state=y&code=z"} {
			cb.RawQuery = query
			res, e := http.Get(cb.String())
			if e != nil {
				return e
			}
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			if res.StatusCode != 400 {
				t.Fatal("forged callback accepted")
			}
		}
		cancel()
		return nil
	})
	if !errors.Is(e, ErrInterrupted) || p.TokenRequests.Load() != 0 {
		t.Fatal("cancellation failed", e)
	}
	u, _ := url.Parse(callback)
	c, e := net.DialTimeout("tcp", u.Host, time.Second)
	if e == nil {
		c.Close()
		t.Fatal("listener retained")
	}
}
func TestOIDCJSONBudgetsAndDuplicateSecurityClaims(t *testing.T) {
	for _, s := range []string{`{"iss":"a","iss":"b"}`, `{"iss":"a","ISS":"b"}`, `{} {}`, strings.Repeat("[", 20) + "0" + strings.Repeat("]", 20), `{"x":"` + strings.Repeat("x", MaxJSONBytes) + `"}`} {
		var d any
		if decodeJSON(context.Background(), []byte(s), &d) == nil {
			t.Fatal("accepted ambiguous/unbounded JSON")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var d any
	if decodeJSON(ctx, []byte(`{}`), &d) == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestOIDCIdentityCannotCrossTargetOrExpiry(t *testing.T) {
	for _, mode := range []string{"target", "expired", "closed"} {
		t.Run(mode, func(t *testing.T) {
			p := testoidc.New(t)
			r := reviewed(t, p)
			id, e := r.Login(context.Background(), p.Open)
			if e != nil {
				t.Fatal(e)
			}
			cfg := &rest.Config{Host: "https://cluster.invalid"}
			switch mode {
			case "target":
				cfg.Host = "https://other.invalid"
			case "expired":
				id.deadline = time.Now().Add(-time.Second)
			case "closed":
				id.Close()
			}
			if _, e = id.Resolve(context.Background(), cfg, "context", "user"); e == nil {
				t.Fatal("stale identity accepted")
			}
		})
	}
}
func TestOIDCTransportRejectsRedirectCompressionAndOversize(t *testing.T) {
	var p *httptest.Server
	p = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			w.Header().Set("Location", p.URL+"/ok")
			w.WriteHeader(307)
		case "/zip":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "gzip")
			io.WriteString(w, "bad")
		case "/big":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, strings.Repeat("x", MaxJSONBytes+1))
		case "/ok":
			t.Error("redirect followed")
			json.NewEncoder(w).Encode(map[string]string{})
		}
	}))
	defer p.Close()
	c := p.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, path := range []string{"/redirect", "/zip", "/big"} {
		if _, e := request(context.Background(), c, "GET", p.URL+path, nil); e == nil {
			t.Fatal("unsafe response accepted")
		}
	}
}
