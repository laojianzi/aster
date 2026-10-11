package oidclogin

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"
)

// SignOut is a public-data-only, one-use browser handoff. It deliberately never
// contains an ID/access/refresh token. Without id_token_hint, RP-Initiated
// Logout requires the provider to ask the browser user for confirmation. This
// also allows a top-level GET to carry SameSite=Lax session cookies; cross-site
// POSTs of an ID token cannot reliably identify the browser's current session.
// It is NOT a revocation grant or evidence that a provider actually logged out.
// Only a successfully resolved Identity can transfer it to its connection owner.
type SignOut struct {
	mu       sync.Mutex
	used     bool
	info     SignOutInfo
	clientID string
	lease    context.Context
	cancel   context.CancelFunc
	stop     func() bool
}

type SignOutInfo struct {
	Issuer, Subject, Endpoint, Context string
	ExpiresAt                          time.Time
}

func (*SignOut) String() string   { return "OIDC sign-out handoff (private context redacted)" }
func (*SignOut) GoString() string { return "OIDC sign-out handoff (private context redacted)" }
func (s *SignOut) Info() SignOutInfo {
	if s == nil {
		return SignOutInfo{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info
}

// Optional metadata is conservative: missing, malformed, foreign-origin or
// query-bearing endpoints disable sign-out, but do not break ordinary login.
// No endpoint is synthesized from an issuer URL or from an editable UI field.
func supportedSignOutEndpoint(issuer, value string) string {
	u, err := endpoint(issuer)
	v, other := endpoint(value)
	if err != nil || other != nil || u.Host != v.Host ||
		(v.Path != "" && !safeText(v.Path, 2048)) || strings.ContainsAny(v.Path, "\\?#") {
		return ""
	}
	return value
}

func newSignOut(opts Options, ep Endpoints, info IdentityInfo) *SignOut {
	path := supportedSignOutEndpoint(opts.Issuer, ep.EndSession)
	if path == "" {
		return nil
	}
	return &SignOut{info: SignOutInfo{Issuer: info.Issuer, Subject: info.Subject, Endpoint: path}, clientID: opts.ClientID}
}

func (s *SignOut) bindLease(parent context.Context, contextName string, expires time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.info.Context, s.info.ExpiresAt = contextName, expires
	s.lease, s.cancel = context.WithDeadline(parent, expires)
	s.stop = context.AfterFunc(s.lease, s.Close)
}

func (s *SignOut) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.used = true
	if s.stop != nil {
		s.stop()
		s.stop = nil
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

func (i *Identity) TakeSignOut() *SignOut {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.resolved {
		return nil
	}
	s := i.signOut
	i.signOut = nil
	return s
}

// TakeURL consumes the handoff before checking its lease. The UI must close its
// local connection before opening the returned, public-only URL, and must not
// retry automatically if browser launching fails. No post-logout redirect is
// requested: a browser launch cannot certify remote completion or JWT revocation.
func (s *SignOut) TakeURL(ctx context.Context, contextName string) (string, error) {
	if s == nil {
		return "", ErrConfiguration
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used {
		return "", ErrUsed
	}
	s.used = true
	defer func() {
		if s.stop != nil {
			s.stop()
			s.stop = nil
		}
		if s.cancel != nil {
			s.cancel()
			s.cancel = nil
		}
	}()
	if ctx == nil || ctx.Err() != nil || s.lease == nil || s.lease.Err() != nil {
		return "", ErrInterrupted
	}
	if !time.Now().Before(s.info.ExpiresAt) {
		return "", ErrExpired
	}
	if contextName != s.info.Context || !safeText(s.clientID, 256) || supportedSignOutEndpoint(s.info.Issuer, s.info.Endpoint) == "" {
		return "", ErrConfiguration
	}
	link, _ := url.Parse(s.info.Endpoint)
	link.RawQuery = url.Values{"client_id": {s.clientID}}.Encode()
	return link.String(), nil
}
