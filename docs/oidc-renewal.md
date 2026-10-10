# Explicit, memory-only OIDC rotation

Scope: Issue #27, `feat/native-oidc-renewal`. Reuses verified browser login,
maintained JOSE/go-oidc verification, immutable cluster transport and lease teardown.

## User flow

Before reviewing a credential-free HTTPS target, opt into **Allow memory-only
renewal (requests offline access and consent)**. Changing this choice discards the
old endpoint review. Default login does not request or retain refresh credentials.
The issuer must advertise refresh grant and offline scope support, then issue a
refresh token after code+S256 login and consent. No OS-vault access is performed.

After the existing two confirmations have connected, open **Browser sign-in**.
**Renew identity once** consumes the current refresh capability and sends one
POST to the originally reviewed endpoint. The active connection is unchanged.
A valid response becomes a pending verified identity: type the exact context in
**Confirm OIDC replacement context**, then **Replace verified connection**.
Replacement closes the original connection context and clears its resources,
prepared operations, watch, logs, command and terminal state before installing
an independent client/session. Failed replacement never restores an old credential.

**Forget renewal** clears the unused capability or cancels/discards a pending
refresh, but leaves an active connection alone. Leaving the panel discards pending
reviewed results. Disconnect, credential expiry and closing the window clear all
local renewal. These actions do not revoke credentials at the issuer or log out
the system browser. Network cancellation cannot roll back an issuer-side rotation.

## Protocol and limits

The rotating capability is opaque and pointer-owned. It is bound to the originating
connection lease, including background callers and an unprocessed UI expiry. It contains the frozen
issuer, client, HTTPS endpoints, CA trust and cluster target digest, original
subject/nonce/optional authentication time, and a hash history (never formatted
token data). Each instance is consumed before validation or network I/O, and one
family is limited to **one hour from login and 32 rotations**. Refreshes do not
extend that absolute deadline. Pending review and the resulting connection are
capped by this family limit, ID-token expiry and the existing local connection cap.

Each operation uses a 30-second total deadline, 10-second request limits, 64-KiB
JSON limits and 16-KiB credential limits. No environment proxy, redirects, network
key references, compression or automatic retry is enabled. A response must supply
a Bearer access token, a verifiable ID token and a fresh refresh token not equal
to any earlier credential in this bounded family. Missing/unchanged/reused tokens,
error envelopes and unexpected scope changes require a new browser login. Provider
rotation/reuse detection remains required on the server; the client cannot prove
that the issuer invalidated its old refresh token.

Refreshed IDs must preserve issuer, subject, single audience and authorized-party
presence; a nonce may be absent, but if present must match the original login.
An auth_time value must describe the original authentication, not the refresh.
If the original had no auth_time, a newly supplied value is conservatively refused.
Signatures, algorithms, key restrictions, access-token hash and fresh iat/exp/nbf
are checked with the existing bounded verifier. No claim skipping was introduced.
The target configuration/CA bytes are reread before renewal and again before
connection. Old HTTP configurations reject further use after replacement.

This is a deliberately constrained public-client rotation profile, not universal
IdP compatibility, transparent refresh, cross-launch storage or federated logout.
Same-user compromise, strong memory erasure and signed physical release access
remain outside this milestone. Updated groups/cluster policy remain authorized by
Kubernetes; verified identity is not itself an RBAC grant.

## Verification

Local signed fixtures cover opt-in, missing/error envelopes, subject/issuer/
audience/nonce/auth_time confusion, rotated-token reuse, fixed budgets, concurrent
attempts, cancellation and ambiguous network results. Native controls test the
second confirmation, minimum-size layout, unchanged old session until confirmation,
late-result isolation, target changes, disconnect/window closure and expiry.

The existing independent Dex plus OIDC-enabled Kubernetes job additionally performs
two actual rotations, connects the new ID, checks namespace-only authorization,
closes old log/HTTP sessions and leaves kubeconfig unchanged. Its bounded HTML
form driver is not a physical/system-browser qualification test. Existing three
Kubernetes versions, actual OS vaults, two clusters, MyGo codemod no-op and both
build-mode migration/desktop gates remain required. Exact CI receipts belong to
the PR and #13/#27, not a claim that the whole product is production-certified.

Primary references: OIDC Core 1.0 sections 11 and 12.2
(https://openid.net/specs/openid-connect-core-1_0.html#RefreshTokenResponse),
OAuth Security BCP RFC 9700 section 4.14
(https://www.rfc-editor.org/rfc/rfc9700.html#section-4.14).

### Independent provider fixture configuration

Pinned Dex v2.46.0 is run with server sessions enabled, rotation required and
zero reuse interval. The first CI run exposed a request timeout with its
session-disabled in-memory store: upstream Rotate invokes freshIdentity inside
UpdateRefreshToken, while that callback reads OfflineSessions through the same
non-reentrant storage mutex. Enabling issuer sessions uses the upstream cached
identity path before the transaction. No issuer source/library is patched and no
Aster signature, nonce, time, rotation, retry or authorization check is disabled.
The fixture metadata records this configuration; compatibility with the stalled
configuration is not claimed. Failed/ambiguous rotation always requires login.
