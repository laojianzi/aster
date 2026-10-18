# Restricted Helm uninstall server dry-run

Scope #88 continues the qualified #84 codec and #86 preparation. It adds no
persistent product mutation, desktop uninstall UI or credentialed Helm CLI.

## Independent, single-use acknowledgement

`AcknowledgeHelmUninstallDryRun` requires an explicit new acknowledgement of full
storage re-reads and PATCH/DELETE **dry-run** requests against the exact prepared
release and targets. GET consent, metadata agreement and a prepared object cannot
substitute. Dry-run uses the same server RBAC permissions as real PATCH/DELETE;
this code neither grants those permissions nor escalates identity.

A copied consent handle shares the same private consumption state. The first
attempt consumes it before network access, including failed/canceled attempts.
`Discard` revokes the handle, cancels in-flight work and invalidates a returned
report. A future UI must call it on input, selection, session, namespace, backend
or connection changes and teardown. The original preparation's 30-real-second
expiry is never renewed. The complete preflight additionally shares the existing
10-second deadline, including rate limiting, decoding and all requests.

## Exact request sequence

1. Validate original backend/session/namespace/revision bindings, consume the
   independent acknowledgement, and perform a fresh complete preparation. The
   original target sequence, names, UID and opaque resourceVersion must match.
2. Read the exact release Secret again and construct the qualified conditional
   **begin** status patch. Send `PATCH ?dryRun=All`, JSON Patch content type,
   current credentials and the exact name/namespace/UID/RV/prior-status tests.
3. Check the returned full Secret under the existing two-MiB bound. All data,
   including encoded release bytes, and unrelated metadata must equal the
   expected transformation. Server-generated RV/managedFields are not reused as
   future preconditions. Unexpected admission mutations reject this profile.
4. For each prepared ConfigMap in source order, send one `DELETE ?dryRun=All`
   with `DryRun:["All"]`, exact UID **and** RV and fixed orphan propagation.
   Metadata-only content negotiation has no full-data fallback. Check either an
   exact successful Status identity or the simulated terminating metadata with
   only the expected orphan finalizer. This is admission, NOT disappearance.
5. Repeat the complete bounded preparation and compare original storage/target
   identities and restrictions. Any changed state rejects the whole result.

There is no mutation retry, redirects, collection deletion, change of policy,
real-write fallback, CLI or parallel batch. 401/403, 404 (including a proxy 404),
conflicts, unsupported media/dry-run, throttling, unexpected warnings, malformed
or oversized responses, deadline, revocation and cancellation fail closed.
DELETE response bodies are at most 64 KiB each; combined dry-run response bytes
are at most four MiB. Existing read budgets are retained. Error bodies are not
read into diagnostics. The result contains defensive identities and expiry,
never request/response bytes, status patches or an execution capability.

Outgoing bodies have separately owned, synchronized Read/Close buffers: an early
server response cannot race patch-buffer clearing with the HTTP writer. Sensitive
byte buffers are cleared on release; this is best-effort minimization, not a
claim that Go strings, runtime copies or external server logs are securely erased.

## Deliberate limits

Successful dry-run is not a transaction lock, a promise of later admission,
original Helm ownership, absence of dependents, observed target deletion or
execution consent. The server and admission webhooks remain a trust boundary;
final reads establish sampled non-persistence, not proof of no external effects.
Orphan propagation can leave dependents outside the reviewed target list.

Only the deployed-to-uninstalling update can be preflighted against current
storage. The final retained-history transition needs the actual later
uninstalling UID/RV and must be separately dry-run/revalidated by the future
executor. This implementation does not pretend to admit a hypothetical future
storage version. Independent execution confirmation, ordered persistent effects,
validated acknowledgements/exact absence, partial/Unknown outcomes, explicit
reconciliation and crash-recovery policy remain prerequisites before activation.

## Evidence

Deterministic HTTP tests exercise exact request identity/options, ordinary and
orphan metadata responses, all consent bindings, copied/concurrent consumption,
revocation, original expiry, transport/body ownership, error privacy, budgets and
changed state. The old required suites remain, with new names additive to the
full unit/race, independent coverage and real-cluster evidence validators.

The explicit disposable-kind fixture verifies real RBAC rejection of PATCH and
DELETE independently, server-side UID/RV conditions, unchanged storage/targets/
canary and rejected stale preparation. Existing pinned Helm v4.3.0 install tests
also run the new preflight on permitted actual Helm-created releases. Compilation
or an unexecuted definition is not server acceptance; exact-head CI and fresh
merged-main artifacts must be reviewed independently before #88 closure.

### Race-run scheduling correction

The initial PR89 head `bb1633a` failed the existing real renderer's final output
case at its shared 30-second deadline (CI39109284858); the original archive hash
is correct and the failure remains nonqualifying evidence. The race command now
uses `-p=1` so separate package test binaries do not compete for runner resources.
This is a test-orchestration change, not a change to the renderer, dry-run code,
assertions or timeouts. All packages, race instrumentation, shuffle, uncached
execution, the existing five-minute package timeout and the single complete raw
stream remain; in-package goroutines and concurrent-consumption tests still run.
The renderer's 15-second operation and 30-second test-parent deadlines are
unchanged. Independent coverage and all original E2E cases remain mandatory.
This reduces package-level contention; it does not turn a later timeout into a
pass or certify arbitrary runner performance. A new exact-head run is required.

Primary contracts: Kubernetes API concepts (dry-run, authorization and generated
values), fixed v1.37.0 core ConfigMap storage and generic registry/delete handlers.
The fixed orphan finalizer can return simulated deleting metadata without any
persistence; that is neither a failed request nor a completed uninstall.

- https://kubernetes.io/docs/reference/using-api/api-concepts/#dry-run
- https://github.com/kubernetes/kubernetes/blob/v1.37.0/pkg/registry/core/configmap/storage/storage.go
- https://github.com/kubernetes/kubernetes/blob/v1.37.0/staging/src/k8s.io/apiserver/pkg/registry/generic/registry/store.go
- https://github.com/kubernetes/kubernetes/blob/v1.37.0/staging/src/k8s.io/apiserver/pkg/endpoints/handlers/delete.go

Other Helm/GitOps/editor/physical-device/release objectives stay in #13.
Enterprise authority remains deferred P3, outside the first MVP. #11's separate
JSON Patch major-version proposal is neither approved nor implemented here.
