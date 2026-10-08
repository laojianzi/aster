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

## Not yet provided

External kubeconfig exec authentication is trusted local code. It is **not sandboxed**, and its
process-group lifecycle/output budget is not yet hardened for enterprise use. Do not import
untrusted kubeconfigs or approve an authentication command without reviewing it.

No enterprise SSO/PKCE, system-keyring lifecycle, short-lived managed credentials, tenant-isolated
Gateway/Connector, server-enforced approvals, append-only remote audit, revocation protocol,
terminal recording, or compliant retention is implemented. In-memory operation history is not
an audit log. Persistent customer resource/log storage is not a supported feature.

Interactive native terminal/TTY is not shipped; the non-interactive Command tab is separate. It requires bounded bidirectional transport,
TTY resize/input lifecycle, terminal escape/clipboard policy and native-library
packaging before exposure. Do not equate log viewing with a terminal.

There is no automatic rollback on an unknown mutation result. Re-read the resource and reconcile
the outcome explicitly. There is no field-ownership-aware SSA workflow or GitOps policy yet;
manual edits may conflict with an external reconciler.

## Production release gate

Do not publish a production release until the approved scope, threat model and evidence cover:
identity and credential handling; managed-access isolation where required; all write/stream
paths; adversarial authorization and failure injection; resource/queue/stream soak tests;
real OS IME/accessibility/DPI scenarios; signed installers and update metadata; upgrade and
rollback/recovery drills; dependency inventory/SBOM and vulnerability review; support matrix
and documented operational recovery. Missing items remain blockers, even when CI is green.
