package oidclogin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/testoidc"
	"k8s.io/client-go/rest"
)

func signOutIdentity(t *testing.T) (*Identity, *testoidc.Provider) {
	t.Helper()
	p := testoidc.New(t)
	p.EditDiscovery = func(d map[string]any) { d["end_session_endpoint"] = p.Server.URL + "/logout" }
	id, e := reviewed(t, p).Login(context.Background(), p.Open)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(id.Close)
	return id, p
}
func resolveSignOut(t *testing.T, id *Identity, ctx context.Context) *SignOut {
	t.Helper()
	if _, e := id.Resolve(ctx, &rest.Config{Host: "https://cluster.invalid"}, "context", "user"); e != nil {
		t.Fatal(e)
	}
	s := id.TakeSignOut()
	if s == nil {
		t.Fatal("missing reviewed handoff")
	}
	t.Cleanup(s.Close)
	return s
}
func TestOIDCSignOutOptionalDiscoveryRejectsUnsafeDestinations(t *testing.T) {
	for _, kind := range []string{"missing", "type", "http", "foreign", "port", "query", "force-query", "fragment", "user", "escaped", "control", "slash"} {
		t.Run(kind, func(t *testing.T) {
			p := testoidc.New(t)
			p.EditDiscovery = func(d map[string]any) {
				v := p.Server.URL + "/logout"
				switch kind {
				case "missing":
					return
				case "type":
					d["end_session_endpoint"] = []string{v}
					return
				case "http":
					v = strings.Replace(v, "https:", "http:", 1)
				case "foreign":
					v = "https://other.invalid/logout"
				case "port":
					v = "https://127.0.0.1/logout"
				case "query":
					v += "?id_token_hint=secret"
				case "force-query":
					v += "?"
				case "fragment":
					v += "#secret"
				case "user":
					v = strings.Replace(v, "https://", "https://secret@", 1)
				case "escaped":
					v += "/%2Fsecret"
				case "control":
					v += "/%0a"
				case "slash":
					v += "\\secret"
				}
				d["end_session_endpoint"] = v
			}
			r := reviewed(t, p)
			if r.Endpoints().EndSession != "" {
				t.Fatal("unsafe optional endpoint exposed")
			}
			id, e := r.Login(context.Background(), p.Open)
			if e != nil {
				t.Fatal("optional metadata blocked login", e)
			}
			defer id.Close()
			if _, e = id.Resolve(context.Background(), &rest.Config{Host: "https://cluster.invalid"}, "context", "user"); e != nil {
				t.Fatal(e)
			}
			if id.TakeSignOut() != nil {
				t.Fatal("invented sign-out destination")
			}
		})
	}
}
func TestOIDCSignOutTransferOnlyAfterResolveAndPublicSingleUseURL(t *testing.T) {
	id, p := signOutIdentity(t)
	if id.TakeSignOut() != nil {
		t.Fatal("unresolved identity transferred capability")
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := resolveSignOut(t, id, parent)
	if id.TakeSignOut() != nil {
		t.Fatal("double transfer")
	}
	id.Close() // transferred ownership must survive identity destruction
	info := s.Info()
	if info.Endpoint != p.Server.URL+"/logout" || info.Subject != "fixture-subject" || info.Context != "context" || !time.Now().Before(info.ExpiresAt) {
		t.Fatal("wrong reviewed binding")
	}
	info.Endpoint = "https://evil.invalid" // copied review cannot change destination
	raw, e := s.TakeURL(parent, "context")
	if e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(raw)
	if e != nil || u.Scheme+"://"+u.Host != p.Server.URL || u.Path != "/logout" || u.User != nil || u.Fragment != "" || len(u.Query()) != 1 || u.Query().Get("client_id") != "aster-test" {
		t.Fatal("unexpected public handoff")
	}
	for _, secret := range []string{"id_token", "access_token", "refresh_token", "state=", "redirect_uri", "eyJ"} {
		if strings.Contains(raw, secret) {
			t.Fatal("credential/callback in URL")
		}
	}
	if _, e = s.TakeURL(parent, "context"); e == nil {
		t.Fatal("replay")
	}
	if parent.Err() != nil {
		t.Fatal("capability canceled originating connection")
	}
	if p.TokenRequests.Load() != 1 || p.DiscoveryRequests.Load() != 1 {
		t.Fatal("handoff performed network request")
	}
	b, _ := json.Marshal(s)
	if string(b) != "{}" || strings.Contains(fmt.Sprintf("%+v %#v", s, s), "fixture-subject") {
		t.Fatal("capability formatting leaked private context")
	}
}
func TestOIDCSignOutCancelledExpiredWrongAndUnboundLeasesConsume(t *testing.T) {
	for _, which := range []string{"unbound", "caller-canceled", "parent-canceled", "expired", "wrong-context", "closed-capability", "nil-context"} {
		t.Run(which, func(t *testing.T) {
			id, _ := signOutIdentity(t)
			parent, stop := context.WithCancel(context.Background())
			defer stop()
			var s *SignOut
			if which == "unbound" {
				s = id.signOut
			} else {
				s = resolveSignOut(t, id, parent)
			}
			ctx, name := context.Background(), "context"
			switch which {
			case "caller-canceled":
				c, done := context.WithCancel(ctx)
				done()
				ctx = c
			case "parent-canceled":
				stop()
			case "expired":
				s.mu.Lock()
				s.info.ExpiresAt = time.Now().Add(-time.Second)
				s.mu.Unlock()
			case "wrong-context":
				name = "other"
			case "closed-capability":
				s.Close()
			case "nil-context":
				ctx = nil
			}
			if link, e := s.TakeURL(ctx, name); e == nil || link != "" {
				t.Fatal("invalid handoff")
			}
			if _, e := s.TakeURL(context.Background(), "context"); e == nil {
				t.Fatal("invalid attempt did not consume")
			}
		})
	}
	// A failed identity resolution must never yield a browser capability.
	id, _ := signOutIdentity(t)
	if _, e := id.Resolve(context.Background(), &rest.Config{Host: "https://other.invalid"}, "context", "user"); e == nil {
		t.Fatal("wrong target")
	}
	if id.TakeSignOut() != nil {
		t.Fatal("failed identity resolution transferred")
	}
}
func TestOIDCSignOutConcurrentConsumptionAndClose(t *testing.T) {
	id, _ := signOutIdentity(t)
	s := resolveSignOut(t, id, context.Background())
	var wg sync.WaitGroup
	var passed atomic.Int32
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.TakeURL(context.Background(), "context"); e == nil {
				passed.Add(1)
			}
		}()
	}
	wg.Wait()
	if passed.Load() != 1 {
		t.Fatal("not exactly once")
	}
	for range 16 {
		wg.Add(1)
		go func() { defer wg.Done(); s.Close(); _ = s.Info() }()
	}
	wg.Wait()
}

func TestOIDCSignOutUntransferredIdentityIsClosed(t *testing.T) {
	id, _ := signOutIdentity(t)
	if _, e := id.Resolve(context.Background(), &rest.Config{Host: "https://cluster.invalid"}, "context", "user"); e != nil {
		t.Fatal(e)
	}
	cap := id.signOut
	id.Close()
	if id.TakeSignOut() != nil {
		t.Fatal("closed identity transferred capability")
	}
	if _, e := cap.TakeURL(context.Background(), "context"); e == nil {
		t.Fatal("undelivered capability outlived identity")
	}
}
