# Security boundaries and release blockers

Aster currently implements a **direct-access development desktop**, not a managed enterprise
authorization gateway. Kubernetes authorization is authoritative. A desktop confirmation cannot
prevent a user with direct cluster credentials from using another client.

## Implemented safeguards

- UI state is owned by the UI thread. Workers capture immutable session/scope/resource identity.
- Kubeconfig risky features require a fingerprint-bound trust decision before constructing the
  authenticated connection. Referenced paths, proxy/TLS settings, executable arguments and identity
  are included. Only a digest, not credential contents, is shown for confirmation.
- Writes require a successful server dry-run and a service-issued, immutable, short-lived,
  single-use plan. Editing/deletion check the reviewed UID/resourceVersion. Create uses POST,
  not apply/upsert, so a concurrent creator cannot be overwritten.
- The native editor hides Secret values and refuses Secret editing; limits apply to manifest,
  log, resource cache and queued state. Redaction is not a universal DLP guarantee for arbitrary
  sensitive fields in custom resources or application log output.
- Port forwarding binds an ephemeral IPv4 loopback listener and is canceled on resource/session
  lifecycle changes. It does not expose a wildcard listener or silently reconnect/replay actions.
- Non-interactive commands require explicit container/argv and Pod-name confirmation, have shared
  stdout/stderr bounds and deadlines, and use no automatic transport fallback or replay. Cancel
  never implies confirmed remote termination. The UI does not interpret terminal control output.
  See [the command contract](commands.md) for name-addressed UID limits and sensitive data handling.
- Health tracking is read-only, pins UID/generation, and has cancellation and deadlines. Ready
  is a point-in-time observation, not an availability guarantee.
- E2E fixtures require explicit disposable-cluster configuration and never default to the user's
  active kubeconfig. CI has read-only repository permissions and pinned action revisions.

## Credential and terminal limits

External kubeconfig exec authentication has a trusted, non-interactive, connection-time runner
with deadlines, environment/output limits, process-group/Job Object lifecycle and expiring snapshots.
It is **not sandboxed**, does not silently renew, and does not qualify arbitrary cloud login tools.
See [authentication](authentication.md). Legacy auth-provider is rejected. Never approve an
untrusted kubeconfig or assume process isolation prevents access to the user's files.

Explicit OS token storage, public-client browser OIDC/PKCE and opt-in memory-only
rotation are implemented as described below. No automatic credential renewal,
short-lived managed credentials, tenant-isolated
Gateway/Connector, server-enforced approvals, append-only remote audit, revocation protocol,
terminal recording, or compliant retention is implemented. In-memory operation history is not
an audit log. Persistent customer resource/log storage is not a supported feature.

The interactive native terminal is separate from Command and uses the hardened, pinned MyGo
adaptation described in [terminal](terminal.md). Its transport/clipboard/input/library policies are
implemented; it is not terminal recording, an OS sandbox, or signed-release qualification.

There is no automatic rollback on an unknown mutation result. Re-read the resource and reconcile
the outcome explicitly. Existing-resource SSA now has a separate reviewed workflow with force disabled and pinned
UID/resourceVersion. This is not a GitOps policy: ordinary explicit Edit remains an update,
not SSA, and direct users/controllers can bypass ownership through other update paths.

## Production release gate

Do not publish a production release until the approved scope, threat model and evidence cover:
identity and credential handling; managed-access isolation where required; all write/stream
paths; adversarial authorization and failure injection; resource/queue/stream soak tests;
real OS IME/accessibility/DPI scenarios; signed installers and update metadata; upgrade and
rollback/recovery drills; dependency inventory/SBOM and vulnerability review; support matrix
and documented operational recovery. Missing items remain blockers, even when CI is green.


## Transport boundary (post-PR #3 hardening)

All authenticated Kubernetes transports reject HTTP 3xx responses before a
second endpoint can receive a request. The redirect Location/body is not exposed
in the error. Configure and explicitly trust the final API endpoint instead;
transparent redirect-based login/API routing is not supported. This does not
prevent a trusted API endpoint or network proxy from forwarding a request itself.

Create, patch and delete now use explicit REST requests with `MaxRetries(0)`.
The default dynamic client's Retry-After handling can replay mutating requests;
a single call to Service.Execute alone did not enforce its no-replay contract.
Read-only LIST/WATCH recovery keeps its existing retry and resynchronization path.
A network/proxy failure after submission remains Unknown and a Prepared plan is
consumed even when its result is uncertain. Exactly-once execution across an
arbitrary intermediary is not claimed.

Regression tests cover authenticated GET/log/discovery redirects and POST/PATCH/
DELETE after 429/500/503 Retry-After responses. Real-cluster fault injection
replaces a successfully committed response with a 503 and independently verifies
the actual persisted effect, the Unknown outcome, and exactly one client write.


### Schema assistance boundary

Schema reads share the current connection's guarded transport and request budget.
Only locally constructed OpenAPI paths and a validated hexadecimal hash are used;
remote hosts/paths/queries, redirects and external refs never become destinations.
Decoded byte/depth/work/output budgets cover untrusted OpenAPI and diagnostic text.
No draft values, schema defaults or examples appear in diagnostic output. Plain-text
descriptions are not executable markup. Editing, hiding or closing the panel and
connection expiry invalidate pending results. Structural hints do not replace
server admission/dry-run or authorize mutations. See `schema-assistance.md`.

## Explicit OS token storage

See [credential-vault](credential-vault.md). The vault never overrides another
credential source, rewrites kubeconfig, silently loads a token or falls back to
plaintext. Its exact-target slot, bounded same-binary helper and transport lease
are separate from issuer-side validity/revocation. Forget is not global logout.
System-user compromise, native provider memory and signed Keychain upgrade
qualification are not solved by this scoped milestone.

## Explicit OIDC rotation (#27)

See `oidc-renewal.md`: opt-in only, memory-only, consume before a single POST,
original identity/target/trust binding, fixed family budget, second confirmation
before replacing a connection. No refresh persistence, automatic retry or issuer
revocation is claimed. Existing expiry/stream teardown remains authoritative.
