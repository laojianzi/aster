# Explicit native OpenID Connect login

Scope: #24, original branch `feat/native-oidc-login`. Builds on the completed
MyGo 0.3 migration, credential-free target binding and connection expiry guards.
This is login plus reviewed connection, not refresh persistence or enterprise
access governance.

## Configure and use

Register a **public native client** with the issuer. Authorize the exact callback
`http://127.0.0.1:<port>/oidc/callback` and authorization code + S256 PKCE. Where
the provider permits arbitrary loopback ports use 0 in Aster; otherwise use the
registered port (1024–65535). The listener exists only during login and binds IPv4
loopback, never a wildcard address. IPv6-only callbacks are not yet supported.

Use an HTTPS kubeconfig context whose user entry contains no credentials. Mixed
exec, existing token, certificate, basic authentication, impersonation, custom
proxy and unsafe TLS configurations are refused, not overwritten. Existing
kubeconfigs continue working through their existing connection workflows.

Open **Browser sign-in**, fill in HTTPS issuer and public client ID. Optional
issuer CA PEM is independent of the cluster CA; empty uses OS system roots.
Review target/endpoints, type the exact context name, and open the system browser.
Default scopes are `openid email profile groups` (no offline access). Explicit memory-only renewal opt-in additionally requests `offline_access` and `prompt=consent`; see [renewal](oidc-renewal.md). The
issuer must advertise code, S256, and RS256 or ES256. This milestone accepts
same-origin HTTPS authorization/token/JWKS endpoints only; providers with split
origins need a future explicit endpoint allowlist, not weakened URL validation.

After browser login Aster shows the verified issuer, subject and expiry. Confirm
the context a second time to connect. The API server must be independently
configured to trust this issuer/client and map its identity. Verification of an
ID token is **not** proof of Kubernetes authorization; RBAC errors remain errors.
Kubernetes receives the ID token, not the OAuth access token. No OS-vault lookup,
write, kubeconfig rewrite or automatic reconnection occurs.

## Security and lifecycle

State, nonce and verifier use independent 256-bit random values. The verifier
never enters a URL; only its S256 challenge enters the authorization request.
The loopback handler verifies method, host, path, state, optional issuer,
duplicate parameters and size; caps accepted live connections at eight; sends
no-store/no-referrer/CSP headers; and never echoes codes or server descriptions.
The callback page does not imply successful identity verification.

Discovery, token exchange and JWKS reads have verified TLS, no environment
proxy, redirects, compression, cookies or automatic retry. Each response is
capped at 64 KiB and 10 seconds. JSON depth/work/key ambiguity is checked before
interpretation; JWTs are capped at 16 KiB; only bounded issuer public keys with
RS256 (2048–8192-bit RSA) or ES256 (P-256) can verify tokens. Maintained go-oidc and
go-jose implement cryptographic validation. We additionally require exact
issuer/nonce/subject, one reviewed audience, matching azp when present, numeric
iat/exp/nbf and fresh bounded times. Unreviewed extra audiences, JOSE network
headers, critical extensions and MAC/unsigned tokens are refused. Access-token
hashes are checked when included. Access tokens are not retained. Refresh tokens are discarded by default; only explicit pre-login opt-in may retain a bounded, window-local rotating capability. A successful code response must
include nonempty string ID/access tokens and Bearer type; any `error` member
is refused before key retrieval. If a JWK supplies `key_ops`, it must be a
bounded unique list authorizing `verify`; encryption-only keys are not used.

Review and login each expire after five minutes. Verified pending identities
expire after at most five minutes; connected identities after the earlier of
ID-token expiry and 15 minutes. Each review and identity is single-use. Profile
and CA bytes are reread before connecting, and copied REST configs reject new
requests after cancellation/expiry. Existing connection contexts stop streams.
Changing inputs/context/path, canceling, leaving the panel or closing a window
invalidates pending callbacks. Authentication does not silently change another
workspace. Cancellation does not revoke issuer tokens or log out the browser.

The persistent MyGo Services handle dispatches the URL on the UI thread; frame
Contexts are not stored in workers. Dispatch itself does not prove the OS browser
opened successfully. A user may cancel or wait for the bounded timeout if OS
browser handoff fails. Strong in-memory secret erasure is not claimed.

## Verification boundaries

Signed local protocol fixtures exercise adverse claims/keys, redirects, token
exchange failure, callback forgery, cancellation and budgets. Native Tester
exercises the actual MyGo controls, two separate confirmations, stale targets,
expiry, persistent-service URL handoff and minimum-window layout. Fixtures are
not real providers or desktop-system browser qualification.

The separate `OIDC qualification` workflow provisions **upstream Dex v2.46.0**
pinned by digest, with verified TLS, disposable static users, S256 enforcement and a dedicated
OIDC-enabled Kubernetes 1.37 control plane. A bounded HTML-form user-agent drives
real Dex authorization/login/consent; Native controls then connect, prove
namespace-only RBAC, follow live logs and clean up on token expiry. The HTML
form driver is test-only and is not a WebView or password-grant product feature.
It is not a rendered Chromium or physical browser test. Provider image identity,
exact checkout and mandatory test records are recorded separately. The existing
three Kubernetes versions, actual OS vaults, two-cluster isolation and both MyGo
build-mode regression gates remain unchanged.

Cross-launch refresh persistence, automatic renewal, issuer logout/revocation, complete enterprise governance and
physical browser/IME/accessibility/release qualification remain in #13.

Primary references:
- https://www.rfc-editor.org/rfc/rfc8252
- https://www.rfc-editor.org/rfc/rfc7636
- https://openid.net/specs/openid-connect-core-1_0.html#IDTokenValidation
- https://github.com/coreos/go-oidc
- https://github.com/dexidp/dex/tree/v2.46.0
