package oidclogin

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Login uses an external browser supplied by the native UI through a persistent
// service handle (never a retained frame Context). open must return promptly.
// The only browser URL values are public client parameters, random state/nonce,
// the S256 challenge and the loopback redirect; verifier and tokens stay local.
func (r *Review) Login(ctx context.Context, open func(string) error) (*Identity, error) {
	r.mu.Lock()
	if r.used {
		r.mu.Unlock()
		return nil, ErrUsed
	}
	r.used = true
	r.mu.Unlock()
	if !time.Now().Before(r.expires) {
		return nil, ErrExpired
	}
	if ctx == nil || ctx.Err() != nil {
		return nil, ErrInterrupted
	}
	if open == nil {
		return nil, ErrBrowser
	}
	ctx, cancel := context.WithTimeout(ctx, LoginTimeout)
	defer cancel()
	defer r.client.CloseIdleConnections()
	start := time.Now()
	state, e := randomValue()
	if e != nil {
		return nil, e
	}
	nonce, e := randomValue()
	if e != nil {
		return nil, e
	}
	verifier, e := randomValue()
	if e != nil {
		return nil, e
	}
	ln, e := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(r.opts.Port))))
	if e != nil {
		return nil, ErrListener
	}
	listener := &callbackListener{Listener: ln}
	callback := "http://" + ln.Addr().String() + "/oidc/callback"
	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	var consumed atomic.Bool
	server := &http.Server{ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, IdleTimeout: 2 * time.Second, MaxHeaderBytes: 4096, ErrorLog: log.New(io.Discard, "", 0)}
	server.SetKeepAlivesEnabled(false)
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		host, _, _ := net.SplitHostPort(q.RemoteAddr)
		if q.Method != "GET" || q.Host != ln.Addr().String() || q.URL.EscapedPath() != "/oidc/callback" || len(q.RequestURI) > 8192 || host != "127.0.0.1" {
			w.WriteHeader(400)
			return
		}
		values, e := url.ParseQuery(q.URL.RawQuery)
		if e != nil || len(values) > 8 {
			w.WriteHeader(400)
			return
		}
		for k, v := range values {
			if len(v) != 1 || len(v[0]) > 4096 {
				w.WriteHeader(400)
				return
			}
			switch k {
			case "code", "state", "iss", "error", "error_description", "error_uri", "session_state":
			default:
				w.WriteHeader(400)
				return
			}
		}
		if subtle.ConstantTimeCompare([]byte(values.Get("state")), []byte(state)) != 1 {
			w.WriteHeader(400)
			return
		}
		if !consumed.CompareAndSwap(false, true) {
			w.WriteHeader(409)
			return
		}
		out := result{code: values.Get("code")}
		if iss, ok := values["iss"]; ok && iss[0] != r.opts.Issuer {
			out.err = ErrVerification
		} else if values.Get("error") != "" {
			out.err = ErrDenied
		} else if !safeText(out.code, 2048) {
			out.err = ErrResponse
		}
		if out.err != nil {
			out.code = ""
			w.WriteHeader(400)
		}
		_, _ = io.WriteString(w, "Return to Aster to review the login result. No cluster access is granted by this page.")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		done <- out
	})
	served := make(chan struct{})
	go func() { defer close(served); _ = server.Serve(listener) }()
	stopped := make(chan struct{})
	go func() { defer close(stopped); <-ctx.Done(); _ = server.Close(); _ = listener.Close() }()
	defer func() { cancel(); _ = server.Close(); _ = listener.Close(); <-served; <-stopped }()
	digest := sha256.Sum256([]byte(verifier))
	auth, _ := url.Parse(r.endpoints.Authorization)
	params := url.Values{"client_id": {r.opts.ClientID}, "response_type": {"code"}, "redirect_uri": {callback}, "scope": {"openid email profile groups"}, "state": {state}, "nonce": {nonce}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}}
	if r.opts.AllowRenewal {
		params.Set("scope", params.Get("scope")+" offline_access")
		params.Set("prompt", "consent")
	}
	auth.RawQuery = params.Encode()
	if e = open(auth.String()); e != nil {
		return nil, ErrBrowser
	}
	var got result
	select {
	case <-ctx.Done():
		return nil, ErrInterrupted
	case got = <-done:
	}
	_ = server.Close()
	_ = listener.Close()
	if got.err != nil {
		return nil, got.err
	}
	if ctx.Err() != nil {
		return nil, ErrInterrupted
	}
	body := url.Values{"grant_type": {"authorization_code"}, "client_id": {r.opts.ClientID}, "code": {got.code}, "redirect_uri": {callback}, "code_verifier": {verifier}}
	raw, e := request(ctx, r.client, "POST", r.endpoints.Token, strings.NewReader(body.Encode()))
	if e != nil {
		return nil, e
	}
	defer clear(raw)
	var response map[string]json.RawMessage
	if decodeJSON(ctx, raw, &response) != nil {
		return nil, ErrResponse
	}
	text := func(k string) string { var s string; _ = json.Unmarshal(response[k], &s); return s }
	token, access := text("id_token"), text("access_token")
	// A successful code response requires both tokens. Treat any error
	// member as an error envelope, even if its value has the wrong type.
	// Reject it before fetching keys; an invalid response never grants identity.
	_, hasError := response["error"]
	if hasError || !strings.EqualFold(text("token_type"), "Bearer") || !safeText(token, MaxTokenBytes) || !safeText(access, MaxTokenBytes) {
		return nil, ErrResponse
	}
	keys, e := request(ctx, r.client, "GET", r.endpoints.Keys, nil)
	if e != nil {
		return nil, e
	}
	info, binding, e := validateBoundIdentity(ctx, r.opts.Issuer, r.opts.ClientID, nonce, token, access, keys, start, nil)
	if e != nil {
		return nil, e
	}
	if ctx.Err() != nil {
		return nil, ErrInterrupted
	}
	id := &Identity{info: info, key: r.opts.TargetKey, token: token, deadline: time.Now().Add(ReviewLifetime)}
	if r.opts.AllowRenewal {
		refresh := text("refresh_token")
		if !validRefresh(refresh) {
			id.Close()
			return nil, ErrRotation
		}
		id.renewal = &Renewal{opts: r.opts, endpoints: r.endpoints, binding: binding,
			token: refresh, until: start.Add(MaxRenewalLifetime), remaining: MaxRenewals,
			history: map[[32]byte]struct{}{sha256.Sum256([]byte(refresh)): {}}}
	}
	return id, nil
}

func validateIdentity(ctx context.Context, issuer, client, nonce, raw, access string, keys []byte, start time.Time) (IdentityInfo, error) {
	info, _, err := validateBoundIdentity(ctx, issuer, client, nonce, raw, access, keys, start, nil)
	return info, err
}

// A refresh may omit nonce, but cannot change the original authentication or identity.
func validateBoundIdentity(ctx context.Context, issuer, client, nonce, raw, access string, keys []byte, start time.Time, prior *claimBinding) (IdentityInfo, claimBinding, error) {
	fail := func() (IdentityInfo, claimBinding, error) { return IdentityInfo{}, claimBinding{}, ErrVerification }
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || len(raw) > MaxTokenBytes {
		return fail()
	}
	var header map[string]json.RawMessage
	h, e := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if e != nil || decodeJSON(ctx, h, &header) != nil {
		return fail()
	}
	for k := range header {
		if k != "alg" && k != "kid" && k != "typ" {
			return fail()
		}
	}
	str := func(m map[string]json.RawMessage, k string) string {
		var s string
		_ = json.Unmarshal(m[k], &s)
		return s
	}
	alg, kid := str(header, "alg"), str(header, "kid")
	if typ := str(header, "typ"); typ != "" && typ != "JWT" {
		return fail()
	}
	if alg != "RS256" && alg != "ES256" {
		return fail()
	}
	var claims map[string]json.RawMessage
	data, e := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if e != nil || decodeJSON(ctx, data, &claims) != nil {
		return fail()
	}
	if str(claims, "iss") != issuer || !safeText(str(claims, "sub"), 255) {
		return fail()
	}
	_, hasNonce := claims["nonce"]
	if prior != nil {
		nonce = prior.nonce
	}
	if (prior == nil || hasNonce) && subtle.ConstantTimeCompare([]byte(str(claims, "nonce")), []byte(nonce)) != 1 {
		return fail()
	}
	var audiences []string
	if json.Unmarshal(claims["aud"], &audiences) != nil {
		audiences = []string{str(claims, "aud")}
	}
	// This milestone trusts exactly the reviewed client, not additional audiences.
	if len(audiences) != 1 || audiences[0] != client {
		return fail()
	}
	if azp, ok := claims["azp"]; ok {
		var s string
		if json.Unmarshal(azp, &s) != nil || s != client {
			return fail()
		}
	}
	number := func(k string) (int64, bool) {
		var n json.Number
		if len(claims[k]) == 0 || claims[k][0] < '0' || claims[k][0] > '9' {
			return 0, false
		}
		e := json.Unmarshal(claims[k], &n)
		if e != nil {
			return 0, false
		}
		i, e := n.Int64()
		return i, e == nil
	}
	exp, ok := number("exp")
	if !ok {
		return fail()
	}
	iat, ok := number("iat")
	if !ok {
		return fail()
	}
	now := time.Now()
	expires := time.Unix(exp, 0)
	if !expires.After(now) || expires.After(now.Add(24*time.Hour)) || iat > now.Add(time.Minute).Unix() || iat < start.Add(-time.Minute).Unix() || exp <= iat {
		return fail()
	}
	if _, ok := claims["nbf"]; ok {
		n, valid := number("nbf")
		if !valid || n > now.Unix() {
			return fail()
		}
	}
	binding := claimBinding{subject: str(claims, "sub"), nonce: nonce}
	_, binding.hasAuthorizedParty = claims["azp"]
	if _, present := claims["auth_time"]; present {
		value, valid := number("auth_time")
		if !valid || value > iat || value > now.Add(time.Minute).Unix() {
			return fail()
		}
		binding.hasAuthTime, binding.authTime = true, value
	}
	if prior != nil {
		if binding.subject != prior.subject || binding.hasAuthorizedParty != prior.hasAuthorizedParty {
			return fail()
		}
		if binding.hasAuthTime && (!prior.hasAuthTime || binding.authTime != prior.authTime) {
			return fail()
		}
		// Keep the original authentication even when a refresh omits auth_time.
		binding.hasAuthTime, binding.authTime = prior.hasAuthTime, prior.authTime
	}
	verified, e := verifySignedToken(ctx, issuer, client, alg, kid, raw, keys)
	if e != nil {
		return fail()
	}
	if verified.AccessTokenHash != "" && verified.VerifyAccessToken(access) != nil {
		return fail()
	}
	return IdentityInfo{Issuer: issuer, Subject: str(claims, "sub"), ExpiresAt: expires}, binding, nil
}
