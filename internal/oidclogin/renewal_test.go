package oidclogin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/credentialvault"
	"github.com/laojianzi/aster/internal/testoidc"
	"k8s.io/client-go/rest"
)

func renewable(t *testing.T, p *testoidc.Provider) (*Identity, *Renewal) {
	return renewableBound(t, p, context.Background())
}

func renewableBound(t *testing.T, p *testoidc.Provider, lease context.Context) (*Identity, *Renewal) {
	t.Helper()
	p.AllowRenewal = true
	cfg := &rest.Config{Host: "https://cluster.invalid"}
	target, e := credentialvault.Bind(cfg, "context", "user")
	if e != nil {
		t.Fatal(e)
	}
	review, e := Discover(context.Background(), Options{Issuer: p.Server.URL, ClientID: "aster-test", TargetKey: target.Key(), CAPEM: p.CA, AllowRenewal: true})
	if e != nil {
		t.Fatal(e)
	}
	defer review.Close()
	id, e := review.Login(context.Background(), p.Open)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(id.Close)
	if id.TakeRenewal() != nil {
		t.Fatal("renewal transferred before connection target validated")
	}
	if _, e = id.Resolve(lease, cfg, "context", "user"); e != nil {
		t.Fatal(e)
	}
	cap := id.TakeRenewal()
	if cap == nil {
		t.Fatal("no opted-in renewal")
	}
	t.Cleanup(cap.Close)
	return id, cap
}
func renew(r *Renewal) (*Identity, error) {
	return r.Renew(context.Background(), &rest.Config{Host: "https://cluster.invalid"}, "context", "user")
}
func TestOIDCRenewalOptInRotationAndSingleUse(t *testing.T) {
	p := testoidc.New(t)
	p.EditTokenResponse = func(m map[string]any) {
		if _, ok := m["refresh_token"]; !ok {
			m["refresh_token"] = "unsolicited-refresh"
		}
	}
	r := reviewed(t, p)
	plain, e := r.Login(context.Background(), p.Open)
	if e != nil {
		t.Fatal(e)
	}
	defer plain.Close()
	if plain.renewal != nil {
		t.Fatal("default login retained refresh token")
	}
	original, cap := renewable(t, p)
	if strings.Contains(fmt.Sprintf("%+v %#v", cap, cap), cap.token) {
		t.Fatal("renewal formatted secret")
	}
	id, e := renew(cap)
	if e != nil {
		t.Fatal(e)
	}
	defer id.Close()
	if id.Info().Subject != original.Info().Subject || p.RefreshRequests.Load() != 1 {
		t.Fatal("wrong identity or request count")
	}
	if cap.token != "" || len(cap.history) != 0 {
		t.Fatal("consumed capability retained secret/history")
	}
	if _, e = renew(cap); !errors.Is(e, ErrUsed) || p.RefreshRequests.Load() != 1 {
		t.Fatal("replayed capability", e)
	}
	cfg := &rest.Config{Host: "https://cluster.invalid"}
	if _, e = id.Resolve(context.Background(), cfg, "context", "user"); e != nil {
		t.Fatal(e)
	}
	next := id.TakeRenewal()
	if next == nil {
		t.Fatal("no rotated capability")
	}
	defer next.Close()
	if next.Info().Remaining != MaxRenewals-1 || !next.Info().FamilyExpiresAt.Equal(cap.Info().FamilyExpiresAt) {
		t.Fatal("family budget slides")
	}
	last, e := renew(next)
	if e != nil {
		t.Fatal(e)
	}
	last.Close()
}
func TestOIDCRenewalRejectsChangedIdentityAndAuthentication(t *testing.T) {
	cases := map[string]func(map[string]any){
		"subject":      func(c map[string]any) { c["sub"] = "other" },
		"issuer":       func(c map[string]any) { c["iss"] = "https://other.invalid" },
		"audience":     func(c map[string]any) { c["aud"] = "other" },
		"azp-presence": func(c map[string]any) { c["azp"] = "aster-test" },
		"nonce":        func(c map[string]any) { c["nonce"] = "not-original" },
		"nonce-null":   func(c map[string]any) { c["nonce"] = nil },
		"auth-time":    func(c map[string]any) { c["auth_time"] = int64(1) },
		"auth-string":  func(c map[string]any) { c["auth_time"] = "1" },
		"iat":          func(c map[string]any) { c["iat"] = time.Now().Add(-time.Hour).Unix() },
		"expires":      func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() },
		"access-hash":  func(c map[string]any) { c["at_hash"] = "invalid" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			p := testoidc.New(t)
			p.EditRefreshClaims = edit
			_, cap := renewable(t, p)
			if id, e := renew(cap); !errors.Is(e, ErrVerification) || id != nil {
				t.Fatal("changed grant accepted", e)
			}
			if _, e := renew(cap); !errors.Is(e, ErrUsed) || p.RefreshRequests.Load() != 1 {
				t.Fatal("invalid grant replayed")
			}
		})
	}
	for _, present := range []bool{true, false} {
		t.Run(fmt.Sprint("original-nonce-and-optional-auth-", present), func(t *testing.T) {
			p := testoidc.New(t)
			var nonce atomic.Value
			p.EditClaims = func(c map[string]any) { nonce.Store(c["nonce"]) }
			p.EditRefreshClaims = func(c map[string]any) {
				c["nonce"] = nonce.Load()
				if !present {
					delete(c, "auth_time")
				}
			}
			_, cap := renewable(t, p)
			id, e := renew(cap)
			if e != nil {
				t.Fatal(e)
			}
			id.Close()
		})
	}
}
func TestOIDCRenewalRejectsEnvelopeAndRepeatedRotation(t *testing.T) {
	cases := map[string]func(map[string]any){
		"error":           func(m map[string]any) { m["error"] = nil },
		"missing-id":      func(m map[string]any) { delete(m, "id_token") },
		"missing-access":  func(m map[string]any) { delete(m, "access_token") },
		"missing-refresh": func(m map[string]any) { delete(m, "refresh_token") },
		"wrong-type":      func(m map[string]any) { m["refresh_token"] = 42 },
		"oversize":        func(m map[string]any) { m["refresh_token"] = strings.Repeat("s", MaxTokenBytes+1) },
		"invalid-refresh": func(m map[string]any) { m["refresh_token"] = "token with spaces" },
		"scopes":          func(m map[string]any) { m["scope"] = "openid admin offline_access" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			p := testoidc.New(t)
			p.EditRefreshResponse = edit
			_, cap := renewable(t, p)
			if id, e := renew(cap); e == nil || id != nil {
				t.Fatal("bad envelope accepted")
			}
			if _, e := renew(cap); !errors.Is(e, ErrUsed) || p.RefreshRequests.Load() != 1 {
				t.Fatal("bad envelope replayed")
			}
		})
	}
	t.Run("older-family-token", func(t *testing.T) {
		p := testoidc.New(t)
		var first atomic.Value
		p.EditTokenResponse = func(m map[string]any) { first.Store(m["refresh_token"]) }
		p.EditRefreshResponse = func(m map[string]any) {
			if p.RefreshRequests.Load() == 2 {
				m["refresh_token"] = first.Load()
			}
		}
		_, cap := renewable(t, p)
		id, e := renew(cap)
		if e != nil {
			t.Fatal(e)
		}
		defer id.Close()
		if _, e = id.Resolve(context.Background(), &rest.Config{Host: "https://cluster.invalid"}, "context", "user"); e != nil {
			t.Fatal(e)
		}
		next := id.TakeRenewal()
		defer next.Close()
		if id, e = renew(next); !errors.Is(e, ErrRotation) || id != nil {
			t.Fatal("old token rotated into family again", e)
		}
	})
}
func TestOIDCRenewalSingleFlightCancellationAndNoReplay(t *testing.T) {
	p := testoidc.New(t)
	p.RefreshStarted = make(chan struct{}, 1)
	release := make(chan struct{})
	p.RefreshRelease = release
	_, cap := renewable(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		id, e := cap.Renew(ctx, &rest.Config{Host: "https://cluster.invalid"}, "context", "user")
		if id != nil {
			id.Close()
		}
		done <- e
	}()
	select {
	case <-p.RefreshStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh did not begin")
	}
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Go(func() {
			if _, e := renew(cap); !errors.Is(e, ErrUsed) {
				t.Error("duplicate accepted", e)
			}
		})
	}
	wg.Wait()
	cap.Close()
	cancel()
	close(release)
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("canceled refresh succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not join")
	}
	if p.RefreshRequests.Load() != 1 {
		t.Fatal("more than one POST")
	}
	for _, code := range []int{0, 302, 429, 503} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			p := testoidc.New(t)
			p.RefreshStatus = code
			p.BrokenRefresh = code == 0
			_, cap := renewable(t, p)
			if id, e := renew(cap); e == nil || id != nil {
				t.Fatal("ambiguous success")
			}
			if _, e := renew(cap); !errors.Is(e, ErrUsed) || p.RefreshRequests.Load() != 1 {
				t.Fatal("automatic refresh retry")
			}
		})
	}
}
func TestOIDCRenewalTargetDeadlineAndBoundedFamily(t *testing.T) {
	for _, kind := range []string{"target", "expired", "exhausted", "canceled", "closed"} {
		t.Run(kind, func(t *testing.T) {
			p := testoidc.New(t)
			_, cap := renewable(t, p)
			ctx := context.Background()
			cfg := &rest.Config{Host: "https://cluster.invalid"}
			switch kind {
			case "target":
				cfg.Host = "https://other.invalid"
			case "expired":
				cap.until = time.Now().Add(-time.Second)
			case "exhausted":
				cap.remaining = 0
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "closed":
				cap.Close()
			}
			if id, e := cap.Renew(ctx, cfg, "context", "user"); e == nil || id != nil {
				t.Fatal("invalid capability accepted")
			}
			if p.RefreshRequests.Load() != 0 {
				t.Fatal("invalid capability made request")
			}
		})
	}
	p := testoidc.New(t)
	_, cap := renewable(t, p)
	cap.until = time.Now().Add(20 * time.Second)
	id, e := renew(cap)
	if e != nil {
		t.Fatal(e)
	}
	defer id.Close()
	result, e := id.Resolve(context.Background(), &rest.Config{Host: "https://cluster.invalid"}, "context", "user")
	if e != nil || !result.ExpiresAt.Equal(cap.until) {
		t.Fatal("family deadline not applied", e)
	}
}

func TestOIDCRenewalCannotOutliveOriginalConnection(t *testing.T) {
	t.Run("before-post", func(t *testing.T) {
		p := testoidc.New(t)
		lease, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, cap := renewableBound(t, p, lease)
		cancel()
		id, err := renew(cap) // background caller cannot resurrect a closed lease
		if id != nil || err == nil || p.RefreshRequests.Load() != 0 {
			t.Fatal("renewal escaped originating connection", err)
		}
	})
	t.Run("during-post", func(t *testing.T) {
		p := testoidc.New(t)
		p.RefreshStarted = make(chan struct{}, 1)
		release := make(chan struct{})
		p.RefreshRelease = release
		lease, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, cap := renewableBound(t, p, lease)
		done := make(chan error, 1)
		go func() {
			id, err := renew(cap)
			if id != nil {
				id.Close()
			}
			done <- err
		}()
		select {
		case <-p.RefreshStarted:
		case <-time.After(3 * time.Second):
			t.Fatal("refresh not started")
		}
		cancel()
		close(release)
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("canceled lease yielded identity")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("lease cancellation did not bound I/O")
		}
		if p.RefreshRequests.Load() != 1 {
			t.Fatal("refresh replayed")
		}
	})
}
