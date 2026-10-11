# Reviewed issuer sign-out

Scope #37: explicit RP-Initiated Logout handoff, not RFC7009 revocation or automatic logout.

## User workflow

After connecting with browser OIDC, reopen **Browser sign-in**. An optional sign-out
section shows the original issuer endpoint and Aster workspace context/subject.
The browser's currently signed-in account may differ and its session may serve
other applications. Review that warning, type the exact active context, then choose
**Disconnect and open issuer sign-out**. Aster synchronously closes this workspace,
cancels requests/watches/logs/terminal sessions, forgets memory-only renewal and
clears reviewed plans before requesting browser navigation. The issuer's page asks
the user to confirm its current browser account. Finish or cancel there.

Missing, malformed, query-bearing, non-HTTPS, or foreign-origin `end_session_endpoint`
metadata disables this feature without breaking normal login. No `/logout` path is
invented. The destination is copied from the original reviewed discovery; later
edits to issuer/client input fields cannot retarget an existing connection.

## Security and ownership

A successfully resolved, target-bound identity can transfer a private single-use
capability to its originating connection lease. The capability holds public OIDC
metadata and display identity only, never ID/access/refresh tokens. It expires with
the connection. Opening requires current connection and panel epochs, exact context,
no pending conflicting operation, and no composing confirmation input. Taking a
URL consumes the capability even if validation fails. It cannot cancel the parent
lease on its own; the UI owns local teardown. New/closed connections invalidate it.

The browser request is a top-level HTTPS GET with `client_id` only. No `id_token_hint`,
access/refresh token, login code, state, callback, post-logout redirect or local file
path is appended. Avoiding the hint prevents sending credential material through
OS launch arguments, browser history or intermediary URL logs. Without a hint the
provider must ask the end user to confirm. This restricted public-client profile
does not attempt arbitrary end-session origins or provider-specific query parameters.

## Outcome and limits

Aster displays **requested**, never **issuer logout completed**. The stable services
API requests system-browser navigation; it does not acknowledge successful logout.
A launch failure is not automatically retried and local disconnection is not rolled
back. The UI does not infer that previously issued Kubernetes JWTs are revoked.
Other workspaces stay locally connected, and stored OS-vault entries are untouched.
The issuer may affect other relying applications sharing its browser session.

No token revocation endpoint, refresh persistence, universal IdP support,
back-channel logout listener or federated-logout guarantee is implemented here.
A signed/physical desktop qualification and independent security audit remain open.

## Verification

Protocol fixtures check unsupported metadata, immutable ownership, target and lease
failure, single-use concurrency, formatting and credential-free destinations.
Native tests exercise review/confirmation, minimum-window layout with a pending
renewal, delayed panel actions, context/expiry/close guards, independent workspaces,
Undo reset and persistent-services dispatch. They run in checked and production modes.

The dedicated existing Dex/Kubernetes workflow adds a third provider flow. A bounded
CI HTML driver saves **disposable fixture cookies only** in a private temporary file
outside artifacts. It requires the pinned Dex confirmation form, submits that explicit
fixture consent, verifies the logged-out page, then replays the old cookie to verify
the upstream session was removed. The same flow validates restricted Kubernetes RBAC,
live-log teardown and the old local transport's rejection. No IdP implementation is
patched and no production browser data or developer credentials are used. This
proves the tested provider path, not physical system-browser rendering or broad IdP support.

References: OIDC RP-Initiated Logout 1.0, section 2;
https://openid.net/specs/openid-connect-rpinitiated-1_0.html
Pinned Dex v2.46.0 `server/logout/logout.go` and `web/templates/logout.html`.
