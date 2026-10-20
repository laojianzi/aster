# Read-only Helm journal admission explanation

Scope [#96](https://github.com/laojianzi/aster/issues/96) continues the completed
[native restricted uninstall and recovery](native-helm-uninstall.md) on qualified
main `aa84d7adb3a3c4d19d58d7a39443e49b2eb51490`. It explains the existing local
journal policy through the same native panes. The implementation and acceptance
contract here do not pre-qualify a candidate, actual main or release.

## Explicit local check

With current Helm metadata selected, open restricted uninstall or local recovery
and choose **Check local journal admission**. The current Backend's frozen
connection-configuration digest, selected namespace and storage UID identify the
local guard. A valid metadata row, including `uninstalling`, is sufficient: the
check needs no new release payload, preparation or cluster request. This does
not establish that the selection satisfies the supported uninstall profile.

The action reads only an existing private directory and existing permanent lock
at the native app's fixed home location. It never creates a directory, lock or
WAL, changes permissions, repairs history or replaces an unavailable store. A
missing directory or lock means **not initialized / not checked**. A missing
lock beside retained WAL files is not an empty successful inventory.

The same nonblocking exclusive OS lock serializes cooperating readers and
executors. A busy store is reported without waiting for a live operation. All
handles are released before UI publication, including cancellation, failed reads
and dropped callbacks. A cleanup failure cannot publish a NoBlocker result.
Existing privacy, no-follow/reparse, ordinary-file, single-link and opened-file
identity checks continue to apply on each supported platform.

The report is a local historical snapshot with a check time. It does not verify
the server's present state, identify a physical cluster, authenticate old bytes
or grant any execution permission. The Backend wrapper retains only inert
identity/report values and cannot mint or consume a preparation, dry-run or
execution consent. The complete selected row, current Backend identity, session,
namespace and fixed path bind its display; changing them invalidates the result.

## Shared policy and first known blocker

`Begin` and `ReviewAdmission` call the same lock-protected admission predicate.
Existing execution checks for usable storage, active writer and a valid complete
plan still precede admission. The shared order is:

1. Inspect the bounded directory inventory and individual file sizes.
2. Reject an exhausted record count.
3. Load records in canonical ID order, rejecting corruption or the first matching
   unresolved record.
4. Require room for a complete new maximum-size operation.

The report explains the **first known blocker**, not every possible problem.
Unrelated corrupt history still blocks the store. A matching earlier unresolved
record can stop the scan before later records are parsed. `InventoryComplete`
means only that the displayed count and byte inventory are complete; it does not
mean that every WAL has passed parsing and integrity checks.

| Report | Meaning |
| --- | --- |
| NoBlocker | This bounded check found no local journal blocker at that time. |
| NotInitialized | Existing private storage or its permanent lock is missing; nothing was created or admitted. |
| Busy / Closed / Unavailable / Invalid / Cancelled | The local check could not establish a safe current result. |
| Corrupt | Damaged or unclassifiable local history prevents execution. |
| RecordLimit | No record slot remains under the existing 128-record limit. |
| OperationBytes / StoreBytes | An existing operation exceeds 256 KiB or retained bytes exceed 16 MiB. |
| ReserveBytes | Current bytes plus the full 256 KiB reservation exceed the 16 MiB store budget. |
| Unresolved | A record matching binding, namespace and storage UID remains a guard against a new attempt. |

Only canonical record IDs are exposed. Arbitrary filenames, filesystem error
text and payloads are not displayed. Count/byte totals appear only when the
inventory is complete. Both the retained-byte budget and the full operation
reservation are shown; the size of a first WAL frame is not the reservation.

The existing unresolved rule is unchanged:

- Without a checked terminal frame, any matching record blocks, even if its
  checked effects are all NotStarted or all Completed.
- A closed record with only NotStarted/Rejected effects, or with all effects
  Completed, is historical and does not block.
- Other closed records with Unknown, AcceptedPending or partial Completed
  effects remain unresolved. The report distinguishes these reasons.

The guard keys are exactly connection binding, namespace and storage UID.
Changing resourceVersion, status, preparation ID or target details does not
release a matching historical guard. A new GET observation also cannot do so.

## Existing confirmations and history

The check is optional information. It neither enables execution nor becomes a
new prerequisite that would prevent a first valid execution when the local
journal has not yet been initialized. Final execution still requires the
original preparation, independent successful server dry-run, retained-history
acknowledgement and exact typed release name within their original lifetime.
Its worker initializes the journal only under that existing explicit execution
confirmation and calls `Begin`, which rechecks the actual current store.

Checking does not renew or consume an existing preparation or dry-run grant.
New checks clear older reports. Scope/lifecycle changes, stop, expiry and
execution start invalidate displayed reports. A previously observed NoBlocker
cannot bypass a record created before the later attempt.

**Review blocking record** navigates through the existing explicit history
loader and detail view. It creates no parallel record table and does not start
current GET observation. The existing history action uses its original `Open`
behavior: if storage disappears between actions, that separate explicit load
may initialize a container and lock. The strict existing-only promise applies
to the admission check, not to that pre-existing independent history action.

Offline history remains available without a connection. A target-specific
admission explanation requires connecting and selecting current metadata first.
Historical effects and separately confirmed current GET facts remain distinct;
successful reads never rewrite Unknown or remove its guard.

## Evidence and remaining policy

The independent `helm-journal-admission` evidence group extends the qualified
aa84 baseline. It retains every old required full name and the #94 native group,
adds meaningful core/Backend/native regressions, and runs a real-cluster scenario
where a successfully applied DELETE loses its response. That scenario checks
that local explanation makes no HTTP request or WAL change and still reports
Unknown after separately authorized current GET observations.

Core coverage includes existing-only paths, lock exclusion, cancellation,
unchanged stored bytes/identities/permissions, exact matching, every unresolved
class, valid-WAL capacity boundaries, cleanup failure and a stale report followed
by a refused actual Begin. Tests also check immutable Backend display binding,
native lifecycle invalidation, minimum-window controls and lack of execution
authority. See [testing requirements](testing.md) for exact evidence consumers.

The final PR candidate and actual merged main each need their own original
source, unit/race/coverage, three-system native/migration, real Kubernetes and
OIDC artifacts. Cross-compilation and earlier original artifacts establish
baselines only. Existing workflows, dependency pins, budgets and timeouts are
unchanged in this scope.

Retention and disposition remain an independent policy decision. This feature
provides no purge, resolve, guard override, repair, replay or resume operation.
Future policy must first define whether any history can be removed or marked
resolved, the exact operation/identity and evidence required, explicit user
confirmation, and conservative behavior after interruption or failed storage.
