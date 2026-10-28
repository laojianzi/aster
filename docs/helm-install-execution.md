# Independently authorized restricted install execution

Scope: [#109](https://github.com/laojianzi/aster/issues/109), sole
[PR #110](https://github.com/laojianzi/aster/pull/110) and existing
`feat/helm-install-execution`, based on qualified main
`b8c69749c60417df877b277ee83713e5684694a2`. This is a backend-only
implementation candidate, not native install activation or release qualification.
It reuses the original capture, GET-only preparation, server preflight, storage
codec, connection binding and version-2 journal. There is no second journal,
release inventory, request renderer or recovery classifier.

## Authority and immutable inputs

A complete original `HelmInstallDryRun` permits one explicit
`AcknowledgeHelmInstallExecution`. This acknowledgment covers persistent writes,
sensitive bounded reads, local journal use and possible partial/unknown effects.
It is not implied by capture, preparation, preflight success or opening a Store.
The grant retains the original Backend identity, session, candidate pointer and
private immutable snapshot, exact preflight request bodies and original expiry.
Reassigning public wrappers cannot redirect or renew the grant. Copies share a
single mint/consume/revocation state. Failed or cancelled execution attempts also
consume the grant; it cannot be serialized, reconstructed from WAL, or reminted
from the same preflight. Revoking either the execution grant or its original
preflight authority cancels an in-flight call.

The fixed operation timeout is the existing ten seconds, further bounded by the
original expiry and parent context. The dispatcher checks authority again after
rate limiting and after the durable intent returns. Cancellation after dispatch
cannot retract a possible server effect. A complete validated response is still
retained as a fact; no new observation or mutation follows expired/revoked
execution authority. Blocking filesystem sync itself is not forcibly interrupted.

### Refuse provably impossible work before effects

Consuming a grant does not immediately create a journal record or send a request.
A local admission check first rejects three provable capacity failures without
raising any existing deadline, rate, read or request-size limit:

- For a token bucket created by `Backend.New`, the best single-page path requires
  `16*N + 40` requests for `N` ConfigMaps. Admission compares that lower bound to
  a full initial bucket plus conservatively rounded refill before the actual
  remaining deadline. The default 20 QPS / burst 40 cannot complete the 552
  requests for 32 targets in ten seconds, so that input is refused before effects.
  Opaque caller-supplied limiters do not expose a trustworthy burst contract;
  their capacity is not inferred from `QPS()` alone.
- The pending Secret's required release data appears in at least `2*N + 4`
  responses before the final transition. If those data bytes alone exceed the
  unchanged 26 MiB shared read allowance, execution is refused.
- The final conditional patch carries both the old and new encoded release
  values. If those data bytes alone exceed the unchanged 2 MiB request limit,
  execution is refused. The size proof and real patch share the same pure codec;
  no hypothetical or dry-run UID/resourceVersion is introduced.

Refusal returns a fixed budget error with `NotStarted` effects, no journal ID,
no new WAL and no execution HTTP requests. The grant remains consumed and the
same preflight cannot mint another one. Local encoding time is included by
checking the actual remaining deadline again before opening the WAL.

These are lower-bound refusals, not reservations or a guarantee of completion.
The 1–32 profile is an input limit, not a promise that every size fits the
default connection. Actual remaining tokens, pagination, other response bytes,
server-generated metadata, network delay and concurrent changes can still stop
an admitted operation under the original Partial/Unknown rules.

## One private Store, ordered possible effects

The existing historically named `HelmUninstallJournal` handle supplies the same
Store for both operations. `BeginInstall` uses the shared process lock, writer
lease, same-release unresolved guard, private file checks and active/archive
budgets. The plan records metadata and digests, not credentials, raw object
payloads, transport errors or authorization. The Store remains leased throughout
the attempt. Every possible write is preceded by a successfully synchronized
intent containing the exact request digest:

1. Create the canonical revision-1 `pending-install` Secret.
2. Create the original ordered 1–32 ConfigMaps in the original namespace.
3. After every preceding effect is confirmed, apply the existing codec's exact
   conditional `pending-install` to `deployed` JSON patch to the actual Secret.

The last patch uses the real observed Secret UID/resourceVersion and original
stored content. It cannot use identities generated during dry-run. There is no
apply/adoption, forced field ownership, DELETE, rollback, resume, transparent
retry, polling, or re-rendering of mutable chart/values files.

Requests use the fixed core-v1 routes, `fieldValidation=Strict` and
`fieldManager=aster-helm-install`. Persistent requests have no `dryRun` parameter,
redirect permission, replayable `GetBody`, or retry capability. Only the last
request is PATCH; all creates are POST. Unsupported responses or uncertain
transport outcomes stop the sequence, even if the server may have committed.

## Fresh reads are not a transaction lock

Before writing, current-identity GET-only preparation must still establish the
original namespace UID/resourceVersion, absent targets and empty release history.
The dispatcher additionally requires unique Secret/ConfigMap routes and their
create/patch verbs. API discovery is not an RBAC grant: every actual request uses
the current configured authentication and can be denied by the server.

Each stage rechecks namespace identity, complete bounded metadata history, exact
object absence before creation and, once created, the original pending Secret.
Before and after the final transition, all created ConfigMaps and the sole
release revision are independently rechecked. An unlabeled Secret at the
reserved name is still a conflict. Reads do **not** lock several Kubernetes
objects. External changes can occur between or after observations; this is not
an atomic multi-object transaction or a claim of continuing readiness.

Sensitive response/observation reads share a finite byte allowance and original
operation deadline. Metadata inventory retains its existing per-scan page and
byte limits; it does not become an unbounded list or background watch. Sensitive
object bodies live only in the execution worker and are cleared on exit. Output
contains bounded identities/digests/outcomes, not raw Secret/ConfigMap data or
arbitrary server diagnostics.

## Response, observation and journal outcomes are separate

An exact successful persistent response must contain real nonempty UID and
resourceVersion, match the complete reviewed request apart from explicitly
allowed generated metadata, and preserve supported ownership metadata. The
conditional transition response must match the exact codec patch applied to
the original observed Secret, preserve UID and change resourceVersion. A
`FieldsV1` ownership entry must contain a JSON object, not an arbitrary value.
Warnings, unexpected encodings/media types, truncation, excess data, read/close
failures, malformed envelopes or untrusted error pages do not qualify.

A valid response is recorded as `AcceptedPending`. A separate exact GET of the
same UID/resourceVersion/content/metadata is required before `Completed` and
before progressing. A typed bounded API rejection can be `Rejected`; a bare
proxy status cannot. A missing/invalid response remains `Unknown`, including a
crash after intent but before dispatch. Later GETs do not clear it. A prior
completed prefix is preserved when a later effect fails or cannot be observed.

The in-memory result can retain an observed fact even when appending it fails.
`JournalComplete` explicitly distinguishes a fully closed durable record from
those in-memory facts. On write, sync, integrity or close failure the executor
stops, does not retry/truncate the WAL, and preserves the earlier checked prefix.
`Finish` closes an attempt, not a promise of successful installation. Offline
`RecoverInstall` returns defensive data and no execution capability.

## Verification and qualification boundaries

Mandatory unit/race/coverage requirements include exact frozen payloads, the
32-target limit, independent consent and copied-grant concurrency, typed
rejections, response loss, observation changes, cancellation/revocation at each
write phase, malformed final ownership metadata, actual WAL-before-HTTP,
journal-boundary failures and three actual process-kill barriers after server
effects but before acknowledgement. Those process tests also verify lock
release, inert recovery, preserved completed prefixes and same-release blocking.
They do not prove power-loss durability or resistance to same-user tampering.

The mandatory execution inventory retains its original 97 names and adds 23
budget/codec cases. It checks real default-limiter refusal with zero HTTP/WAL,
default small-input success, known sufficient capacity and opaque limiters,
parent/original deadlines, independent read/patch byte failures and one-shot
consumption. Successful fixture requests are counted against the formula, and
the valid high-entropy create whose patch data exceeds 2 MiB is reproduced.

Disposable Kubernetes tests cover successful creation/deployment, a same-name
Secret race, a ConfigMap race and committed response loss. Compilation alone is
not actual cluster verification. Existing real-cluster contract gates require
all five names (the parent and four scenarios) on each supported version.

The 250 existing Go test files, workflows, module/native pins and runtime budgets
are frozen by an additional 265-path content/mode checksum. Old mandatory names
are retained. Previous census tests exclude only explicitly enumerated new test
files; their original frozen checksums remain unchanged.

Final exact-head CI, three-platform native regression, actual disposable
Kubernetes execution, independent final-PR/main original evidence, recoverable
retirement/full-history restoration and explicit #109 closure are still required
before this increment can be described as delivered. Native install controls,
network recovery/reconciliation, upgrades/rollback/GitOps, dependency upgrades,
export/purge and deferred P3 authority are separate scopes.

Retirement is registered only for actual PR #110 and its exact feature branch.
Issue #109 and previous PR #108 are rejected by dedicated regression guards.
Final PR and actual-main acceptance, artifact identities and the real retirement
and offline-restoration outcome belong in #109's completion record and #13's
current checkpoint. This contract does not predeclare those results.

Reference: Kubernetes API concepts, request processing/dry-run/resource versions:
https://kubernetes.io/docs/reference/using-api/api-concepts/
