# Restricted Helm install server preflight

Implementation candidate for #105; reuse completed #103/#104 at `610fc9da`.
This is not installation, a persistent transaction, a deployed-transition
qualification, a journal extension or a desktop install control.

## Consent and immutable content

`BuildInstallCreatePlan` freezes one pending-install Secret followed by the
captured ConfigMaps in original manifest order. It reuses the bounded release
profile and pure Secret codec. It sets the reviewed namespace and deterministic
Helm ownership labels/annotations, retaining supplied data/binaryData and other
permitted metadata. It never rereads source files or rerenders a chart.
Bodies are bounded by 2 MiB each and 6 MiB in aggregate. Public formatting and
JSON suppress content; `SensitiveBody` returns an owned copy for the transport
and must never be placed in a report, UI, diagnostic or exported evidence.

The acknowledged grant snapshots the original preparation/candidate wrappers,
binds their IDs and original Backend/session, and preserves the preparation's
original deadline. Preparation itself also binds the immutable candidate ID so
replacing its original public wrapper before acknowledgement cannot retarget it.
Copies of a grant share one mutex-protected consumed/revoked state. Consumption
occurs before network work, even for a cancelled or mismatched call. Discard
cancels in-flight work; a later acknowledgement cannot extend an old preparation.
Concurrency means concurrent use of immutable handles and Discard, not concurrent
caller writes into a Go struct. Sensitive immutable memory may remain until all
handles are unreachable; this is not guaranteed secure memory erasure.

## Transport and verification

Reuse full current-identity preparation before and after the POST sequence;
namespace UID **and** resourceVersion must match the originally reviewed sample.
Additional exact discovery requires create support for the two collection paths.
Discovery is API capability information, not RBAC authorization.

The sole request constructor emits POST to the fixed core/v1 namespace collection,
with `dryRun=All`, `fieldValidation=Strict`, and `fieldManager=aster-helm-install`.
It reuses the current authenticated transport, TLS, redirect rejection and rate
limiter. Bodies have no `GetBody` replay callback or idempotency headers. No
application retry, fallback, adoption, PATCH, PUT or DELETE exists on this path.
The total preflight retains the existing ten-second read budget and original
preparation expiry; no test, workflow or product deadline is raised.

Only HTTP 201 with a single JSON content type, no warnings and no compression
qualifies for validation. Error bodies are not read or echoed. Response bodies
are bounded individually and in aggregate; incomplete reads, warning trailers,
duplicate fields, unknown fields, invalid UTF-8, excess JSON depth/tokens or
altered identity/content fail closed. Exact typed Secret/ConfigMap comparison
permits only these generated metadata differences: UID, resourceVersion,
creationTimestamp and bounded managedFields for the fixed field manager. UID
and creation time must be well formed; a dry-run create RV can be empty.
All other fields, including data, binaryData, labels, annotations, immutable,
ownerReferences and finalizers, must match the frozen request.

The immutable result records each attempted POST and whether its complete
response was verified. A later failure preserves earlier verified steps, but
`Complete` is false until the postflight passes. Returned generated identities
are deliberately not exposed as future write preconditions. Discard invalidates
`Matches` without erasing historical facts. Reports grant no execution authority.

## Limits and mandatory next work

The server dry-run contract passes admission without persistence; it does not
reserve names or freeze the namespace. New competing actors and future admission
changes remain possible after the last read. A dishonest server or external
webhook violating its dry-run declaration cannot be ruled out by a desktop
client. Postflight samples require exact target absence and unchanged namespace
identity; they are not proof of all possible external side effects.

A dry-run pending Secret is not persisted, so there is no real pending UID/RV for
a following pending-install -> deployed PATCH. No such identity is fabricated.
Persistent install requires its own durable pre-effect journal, independent
execution confirmation, create-only conflict handling, verified outcomes and
partial/Unknown recovery, without automatic replay or rollback. The existing
uninstall journal and all native install controls remain unchanged.

## Evidence and primary semantics

Unit tests cover frozen bodies, content/metadata rejection, shared consent
copies, cancellation, wrapper replacement, partial reports, body replay surfaces,
error-body suppression and transport budgets. Disposable cluster tests compare
full canaries and exact target absence, real RBAC rejection, concurrent creates,
competing history and namespace changes. Fixture writes are independently
credentialed; every product mutation is checked by a transport guard.

New exact-name gates are additive to all older requirements. A compile is not a
cluster runtime test; local checks are not final PR or main qualification.

- [Kubernetes dry-run, generated values and authorization](https://kubernetes.io/docs/reference/using-api/api-concepts/#dry-run)
- [Kubernetes strict field validation](https://kubernetes.io/docs/reference/using-api/api-concepts/#field-validation)
