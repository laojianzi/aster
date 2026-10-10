package oidclogin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/laojianzi/aster/internal/credentialvault"
	"k8s.io/client-go/rest"
)

const (
	MaxRenewalLifetime = time.Hour
	MaxRenewals        = 32
	RenewalTimeout     = 30 * time.Second
)

var ErrRotation = errors.New("OIDC rotation unavailable or invalid; sign in again. The previous refresh credential will not be retried")

type claimBinding struct {
	subject, nonce                  string
	authTime                        int64
	hasAuthTime, hasAuthorizedParty bool
}

// Renewal is an opaque, memory-only, one-attempt capability. Never serialize it.
// It is issued only by verified, opted-in login or a successful token rotation.
// Its absolute family deadline and bounded hash history do not slide on renewal.
// Local deletion is not issuer revocation; the issuer must enforce rotation too.
type Renewal struct {
	mu          sync.Mutex
	used        bool
	cancel      context.CancelFunc
	leaseCtx    context.Context
	leaseCancel context.CancelFunc
	leaseStop   func() bool
	opts        Options
	endpoints   Endpoints
	binding     claimBinding
	token       string
	until       time.Time
	remaining   int
	history     map[[32]byte]struct{}
}
type RenewalInfo struct {
	Issuer, Subject string
	FamilyExpiresAt time.Time
	Remaining       int
}

func (*Renewal) String() string   { return "OIDC renewal (credentials redacted)" }
func (*Renewal) GoString() string { return "OIDC renewal (credentials redacted)" }
func (r *Renewal) Info() RenewalInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RenewalInfo{r.opts.Issuer, r.binding.subject, r.until, r.remaining}
}
func (r *Renewal) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.used = true
	r.token = ""
	clear(r.history)
	if r.leaseStop != nil {
		r.leaseStop()
		r.leaseStop = nil
	}
	if r.leaseCancel != nil {
		r.leaseCancel()
		r.leaseCancel = nil
	}
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
}

// bindLease is called only by the successful, single-use Identity.Resolve.
// A capability cannot outlive its originating connection even if a caller later
// supplies context.Background, or the UI expiry callback has not been pumped.
func (r *Renewal) bindLease(parent context.Context, expires time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.leaseCtx, r.leaseCancel = context.WithDeadline(parent, expires)
	r.leaseStop = context.AfterFunc(r.leaseCtx, r.Close)
}

// TakeRenewal transfers ownership only after Resolve successfully bound the ID
// token to its target. The caller must close it on failed/canceled connection.
func (i *Identity) TakeRenewal() *Renewal {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.resolved {
		return nil
	}
	r := i.renewal
	i.renewal = nil
	return r
}
func validRefresh(s string) bool {
	if len(s) == 0 || len(s) > MaxTokenBytes {
		return false
	}
	for _, c := range []byte(s) {
		if c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}

// Renew never changes an existing client or contacts Kubernetes. It consumes
// this capability before validation or network I/O, posts once to the original
// token endpoint, then verifies a new ID token and requires refresh rotation.
// Lost, invalid, canceled or ambiguous responses require a fresh browser login.
func (r *Renewal) Renew(ctx context.Context, cfg *rest.Config, contextName, user string) (*Identity, error) {
	r.mu.Lock()
	if r.used {
		r.mu.Unlock()
		return nil, ErrUsed
	}
	r.used = true
	defer r.Close() // only the admitted attempt owns cleanup; duplicates must not cancel it
	token := r.token
	r.token = ""
	if ctx == nil || ctx.Err() != nil {
		clear(r.history)
		r.mu.Unlock()
		return nil, ErrInterrupted
	}
	if !time.Now().Before(r.until) || r.remaining <= 0 || r.leaseCtx == nil || r.leaseCtx.Err() != nil {
		clear(r.history)
		r.mu.Unlock()
		return nil, ErrExpired
	}
	ctx, cancel := context.WithDeadline(ctx, r.until)
	r.cancel = cancel
	stopLease := context.AfterFunc(r.leaseCtx, cancel)
	defer stopLease()
	history := r.history
	r.history = nil
	r.mu.Unlock()
	defer cancel()
	defer clear(history)
	ctx, boundedCancel := context.WithTimeout(ctx, RenewalTimeout)
	defer boundedCancel()
	target, err := credentialvault.Bind(cfg, contextName, user)
	if err != nil || target.Key() != r.opts.TargetKey {
		return nil, ErrConfiguration
	}
	client, err := newClient(r.opts.CAPEM)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	start := time.Now()
	body := url.Values{"grant_type": {"refresh_token"}, "client_id": {r.opts.ClientID}, "refresh_token": {token}}
	raw, err := request(ctx, client, "POST", r.endpoints.Token, strings.NewReader(body.Encode()))
	token = ""
	body = nil
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	var response map[string]json.RawMessage
	if decodeJSON(ctx, raw, &response) != nil {
		return nil, ErrResponse
	}
	text := func(k string) string { var s string; _ = json.Unmarshal(response[k], &s); return s }
	_, hasError := response["error"]
	idToken, access, refresh := text("id_token"), text("access_token"), text("refresh_token")
	if hasError || !strings.EqualFold(text("token_type"), "Bearer") || !safeText(idToken, MaxTokenBytes) || !safeText(access, MaxTokenBytes) {
		return nil, ErrResponse
	}
	if !validRefresh(refresh) {
		return nil, ErrRotation
	}
	hash := sha256.Sum256([]byte(refresh))
	if _, repeated := history[hash]; repeated {
		return nil, ErrRotation
	}
	if scope, present := response["scope"]; present {
		// Reduced or extended scope changes need a new explicit login, not a silent fallback.
		var value string
		if json.Unmarshal(scope, &value) != nil || !sameScopes(value) {
			return nil, ErrRotation
		}
	}
	keys, err := request(ctx, client, "GET", r.endpoints.Keys, nil)
	if err != nil {
		return nil, err
	}
	info, binding, err := validateBoundIdentity(ctx, r.opts.Issuer, r.opts.ClientID, r.binding.nonce, idToken, access, keys, start, &r.binding)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ErrInterrupted
	}
	deadline := time.Now().Add(ReviewLifetime)
	if deadline.After(r.until) {
		deadline = r.until
	}
	if leaseDeadline, ok := r.leaseCtx.Deadline(); ok && deadline.After(leaseDeadline) {
		deadline = leaseDeadline
	}
	if !time.Now().Before(deadline) {
		return nil, ErrExpired
	}
	id := &Identity{info: info, key: r.opts.TargetKey, token: idToken, deadline: deadline}
	// The final rotation still yields a usable reviewed identity, but no further
	// capability. The absolute family lifetime limits its connection as well.
	nextHistory := make(map[[32]byte]struct{}, len(history)+1)
	for h := range history {
		nextHistory[h] = struct{}{}
	}
	nextHistory[hash] = struct{}{}
	id.renewal = &Renewal{opts: r.opts, endpoints: r.endpoints, binding: binding, token: refresh, until: r.until, remaining: r.remaining - 1, history: nextHistory}
	return id, nil
}
func sameScopes(s string) bool {
	want := map[string]bool{"openid": true, "email": true, "profile": true, "groups": true, "offline_access": true}
	fields := strings.Fields(s)
	if len(fields) != len(want) {
		return false
	}
	for _, field := range fields {
		if !want[field] {
			return false
		}
		delete(want, field)
	}
	return len(want) == 0
}
