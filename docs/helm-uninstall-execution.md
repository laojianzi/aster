# Restricted retained-history Helm uninstall execution

This backend scope continues the qualified status codec, restricted preparation,
and server dry-run preflight. It introduces ordered, conditional persistent
effects for the same narrow ConfigMap profile. There is no desktop or CLI call
site, automatic resume, operation persistence, or crash-recovery UI. Those
activation and recovery policies must be qualified separately.

## Independent execution confirmation

The sequence is `PrepareHelmUninstall`, an independent dry-run acknowledgement,
`PreflightHelmUninstall`, a new `AcknowledgeHelmUninstallExecution`, and one
`ExecuteHelmUninstall` call. A successful read or dry-run does not grant execution
authority. The execution acknowledgement covers the exact current backend,
session, namespace, release/revision, reviewed identities, retained history,
orphan consequences, fresh sensitive reads and admission checks, and possible
partial or Unknown effects.

Only one execution consent can be minted from a successful dry-run report.
Copies of that consent share its private consumption and revocation state. The
dry-run and execution grants own their preparation wrapper values; reassigning
a caller's wrapper cannot replace the reviewed target or extend its expiry.
The first execution attempt consumes the consent before I/O, including
canceled, stale or failed attempts. `Discard` revokes in-flight work; discarding
the original dry-run consent also revokes execution. Neither operation retracts an already
dispatched request or erases the record of earlier effects.

Execution retains the original preparation's thirty-second expiry. Its entire
request/read/observation sequence additionally shares the existing ten-second
deadline. It cannot renew either boundary by preparing again. The backend,
session, namespace and original selection must match at each admission point.

## Supported profile and ordered effects

The existing complete profile remains mandatory: one deployed revision for the
selected release, supported Secret-driver storage and stored chart content,
and one to thirty-two distinct namespaced core/v1 ConfigMaps with the exact
reviewed UID/resourceVersion and matching Helm metadata. Hooks, keep policy,
foreign or cluster scope, CRDs, current owner references/finalizers/deletion,
ambiguous storage and unsupported shapes remain ineligible. Metadata agreement
does not authenticate original Helm ownership.

The executor operates in this order:

1. Revalidate the complete original preparation and unchanged source/targets.
   Perform current server dry-run admission without renewing the review.
2. Re-read the exact source and conditionally patch deployed to uninstalling.
   The qualified codec preserves unrelated payload bytes and uses atomic tests
   for name, namespace, UID, opaque resourceVersion and prior status. Validate
   the full bounded reply and independently re-read retained storage/history.
3. Process individual targets in their reviewed source order. Before each
   delete, revalidate source/history and the exact target. Each request uses
   both original UID and resourceVersion preconditions, fixed orphan
   propagation, and no collection deletion. Validate the bounded response, then
   observe exact target absence before submitting the next target.
4. Recheck all target absences and exact retained source/history. Build and
   separately dry-run the final status patch against the **actual observed
   uninstalling UID/resourceVersion**. A hypothetical future version cannot
   substitute. Revalidate immediately before the conditional persistent finish.
5. Validate the finish response and freshly observed uninstalled storage,
   retained payload/history and target absences before reporting Completed.

There is no parallel batch, force, history purge, mutation retry, redirect,
credentialed Helm CLI, rollback, automatic second attempt, or fallback that
turns an unsupported dry-run into a persistent request. Kubernetes preconditions
protect each individual write; this sequence is not an atomic transaction. A
later denial or identity change can leave uninstalling storage and earlier
targets already deleted. Orphan propagation can preserve dependents outside the
reviewed target list.

## Effect and aggregate outcomes

The immutable result retains the original review identity, latest strictly
verified storage identity, original expiry, attempt times, target identities,
and ordered begin/delete/finish entries. Unsubmitted entries stay NotStarted.
Returned slices are defensive copies; reports contain no client, credential,
release data, patch, response body or renewed execution authority.

| Outcome | Meaning |
| --- | --- |
| NotStarted | No persistent effect was dispatched for that entry. Initial checks can fail before the first effect. |
| Rejected | The server rejected the particular conditional request; no success is inferred. |
| AcceptedPending | A strict acknowledgement was accepted, but the required later observation has not established completion. |
| Completed | That effect met its acknowledgement and observation requirements. A DELETE needs exact absence, not just a terminating object. |
| Partial | At least one earlier effect completed, while the complete ordered operation did not. Inspect the individual entries. |
| Unknown | A dispatched effect may have committed but its authoritative acknowledgement cannot be established. Earlier verified effects remain recorded. |

Unknown takes precedence over an aggregate Partial result. A success-shaped but
malformed, wrong-identity, unexpected or oversized persistent response is not a
known rejection. A lost response, cancellation or deadline after dispatch can
therefore be Unknown even if an independent observer later sees a real change.
The pinned Kubernetes JSON Patch handler returns an anonymous Invalid/422 when
a patch test fails; its resource/name details are empty. This fails the exact
rejection envelope check and remains Unknown even when a separate fixture read
proves that the concurrent source prevented the write. The executor does not
promote a status code alone into authoritative rejection.
AcceptedPending is not a claim that a finalizer/controller will eventually
finish; a completed begin followed by a pending DELETE yields an aggregate
Partial result while preserving the DELETE's pending entry.

Absence requires the bounded, typed NotFound response for the exact resource,
name and group. A generic proxy 404, wrong identity, forbidden read, replacement
UID, or malformed/truncated response cannot establish deletion. Each observation
has a short local budget inside the unchanged overall deadline. The executor
does not replay a write after ambiguous observation or expose a resume token.

The report remains historical information after expiry or Discard. A future
reconciliation and crash-recovery policy must distinguish persisted server state
from lost local state and obtain new explicit authority before any further
mutation. No existing desktop path calls these APIs until that policy and the
native confirmation/lifecycle contract have been qualified.

## Evidence and its limits

The added unit/HTTP/concurrency tests use an independently stateful HTTP server
to exercise consent bindings and single consumption, current source/target
checks, ordered effects, strict replies, original expiry/shared budgets,
revocation, retained outcomes, exact absence and response loss. They remain
additive to the complete uncached shuffled race stream and independent complete
coverage stream. Existing renderer scheduling and all old test names stay intact.

`e2e/helm_uninstall_execution_test.go` uses the existing explicit disposable-kind
guard and unique namespaces. Its ordinary client observes the real server
independently of the fault-injected product transport:

- A complete reviewed ConfigMap sequence must disappear while the same release
  Secret, unrelated metadata, raw non-lifecycle payload
  fields and an unreviewed ConfigMap canary remain. Retained labels and decoded
  release status must agree, and a consumed execution must issue no new write.
- Concurrent source resourceVersion and target UID/resourceVersion changes are
  injected after the last product read, immediately before the real conditional
  request. The real API must reject it with Conflict/Invalid; a replacement and
  every later target must remain intact.
- An impersonated identity first passes real dry-run. Fixture RBAC is revoked
  before begin, the second target's DELETE, or finish. A SubjectAccessReview
  confirms the denial is effective, then the original request reaches the real
  server and must receive Forbidden. Earlier effects remain, later effects stop.
- For begin, DELETE and finish separately, the real server first returns a
  successful persistent response. The transport then replaces that response
  with a retryable 503. Independent reads prove what actually committed, while
  the product must report Unknown, stop and reject replay of the consumed grant.

These fixtures contain synthetic stored releases and exercise Aster's restricted
executor. The existing seven real pinned-Helm lifecycle scenarios and the later
preparation/dry-run checks remain independently mandatory; this scope does not
claim full Helm behavior or rewrite their evidence. Tests run on every configured
Kubernetes version. Defining or compiling them is not actual cluster execution.

New required execution cases join the existing Helm contract validator at all
three existing call sites: full unit/race, independent coverage, and real-cluster
evidence. Final PR and new actual main each require their own four successful
workflows and original source/raw-test/coverage/server/native/binary evidence.
Explicit Issue completion follows actual-main qualification and observed
recoverable branch retirement. Previous successful heads and development
binaries do not establish the new head's qualification or a signed release.

Other Helm operations, GitOps, safe editor/Schema work and release qualification
remain in #13. Enterprise authority remains deferred P3 outside the first MVP;
the independent #11 JSON Patch major update is not approved by this scope.

Related contracts: [preparation](helm-uninstall-preparation.md),
[server dry-run](helm-uninstall-dry-run.md),
[status transitions](helm-status-transitions.md), and
[pinned Helm comparisons](helm-uninstall-contract.md).

The precise Invalid/422 behavior is defined by the pinned Kubernetes
[JSON Patch handler](https://github.com/kubernetes/kubernetes/blob/v1.37.0/staging/src/k8s.io/apiserver/pkg/endpoints/handlers/patch.go)
and [generic status constructor](https://github.com/kubernetes/kubernetes/blob/v1.37.0/staging/src/k8s.io/apimachinery/pkg/api/errors/errors.go).
