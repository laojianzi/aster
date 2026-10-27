# Versioned install write-ahead history

Scope: #107, from main 64ed3e4430b9a6fdcfda8bfa3f17dd946bbb9a08. This is a
local inert journal, not a persistent installer, reconciliation client or native
installation control. It reuses the existing private directory, permanent OS
lock, file identity checks, writer lease, durable sink and admission inventory.

## Format and compatibility

The length/checksum framing retains `ASTER-HELM-JOURNAL-V1\n`. This names the
framing, not the payload schema. Existing payload version 1 and its canonical
JSON encoding are unchanged. Payload version 2 uses an `install` plan and
explicit `install-start/intent/response/observed/finish` events; every file must
have exactly one version/action. A version-1-only reader rejects the version-2
start rather than ignoring its `.wal` or archived filename. Unknown versions,
canonical/duplicate-key errors, mixed actions, and corrupt tails fail closed.
Checksums detect corruption, not malicious alteration by the same OS user.

The initial plan contains only the existing connection-configuration digest,
namespace name/UID/resourceVersion, canonical new release and revision-1 storage
name, candidate/preparation IDs, frozen pending-Secret digest, ordered ConfigMap
names/content digests and timestamps. It has **no pre-create Secret UID/RV**.
All v1/v2 operations must share the existing connection binding function, even
though its historic purpose label mentions uninstall. A new per-action digest
or directory would invalidate the cross-operation guard and is not permitted.

There are no credentials, sessions, raw chart/values/Secret/ConfigMap payloads,
transport errors, execution grants or callbacks in the record. Metadata and
content hashes are still private local information, not an export/audit format.
Identifiers/digests are bounded and checked; application callers remain
responsible for computing them from the original approved immutable inputs.

## Ordered effects

`BeginInstall` durably records the plan using the same writer path as `Begin`.
The effects are the pending Secret create, 1–32 ordered ConfigMap creates, then
the deployed transition. `Intent(index, requestDigest)` must succeed before a
future executor sends that exact request. For creates its digest must equal the
frozen plan. The final transition digest binds a fresh exact conditional PATCH;
that PATCH cannot be constructed before the real pending Secret exists. Its
UID/RV are taken only from the first effect's recorded response and independent
observation, never from dry-run. The journal itself neither constructs nor
validates PATCH bodies and does not attest to the origin of caller-supplied facts.

Each intent initially recovers as `Unknown`, including a crash before dispatch.
A response may record `AcceptedPending` with the actual UID/RV, `Rejected`, or
`Unknown`. A separate matching `Observe` is required for `Completed`. Requests
cannot skip an earlier effect, repeat an intent, mutate a response, substitute
an identity, or proceed from rejected/pending/unknown effects. No later GET can
change Unknown into Completed. There is no resume, retry, adoption or rollback.

Finish means that the attempt ended, not that all effects succeeded. Results
may be recorded after cancellation/deadline to retain facts; journal methods
are never network authorization or freshness gates. The future executor must
check its independent current consent and cancellation before each dispatch.
Recovery returns defensive copies of the checked prefix, without a Writer or
network capability. Legacy uninstall recovery explicitly rejects version 2,
including corrupt version-2 prefixes and archived records.

## Shared admission, quotas and archival

Install admission matches connection binding + namespace name + release name,
including unresolved legacy uninstall history. An unresolved install likewise
blocks a later uninstall of that name even if its storage UID is different.
Namespace replacement, new revision, candidate or preparation IDs do not erase
this guard. When only legacy operations are involved, their original UID match
is unchanged. Old UID-only admission callers conservatively block *any*
unresolved install in the same binding/namespace because they supplied no name.
The current Backend now supplies the release name for precise explanations.

Active count/bytes, full 256 KiB reservation per new operation and retained
archive count/bytes remain the existing shared budgets (128/16 MiB each pool).
Every archived record must pass the same resolved terminal classifier; manual
renaming of Unknown/partial history cannot hide it. A closed no-effect or
confirmed rejection before any accepted/unknown effect is historical, not a
replay grant. A complete successful attempt is also historical. Unclosed,
partial, pending, corrupt and uncertain records stay blocked. Archive keeps
original bytes and file identity; it does not reclaim disk or clear uncertainty.

## Verification and remaining work

Mandatory additions run in full unit/race/coverage and the existing three-OS
journal matrix. Tests exercise strict framing, transitions, cross-operation
admission, legacy encoding, downgrade rejection, actual process kill and lock
exclusion, immutable recovery, archive classification, quotas, maximum profile,
file identity/content replacement and write/sync failures. Original Go tests,
workflow budgets and dependency pins are preserved.

A process-kill test is not power-loss qualification. A successful fsync cannot
prove arbitrary hardware/OS persistence, authenticity or correctness of caller
facts. The journal must not be used as an execution authority. Persistent
installer integration, current-identity observation, independent write consent,
conditional create/PATCH transport, recovery UI and signed-release qualification
remain separate. No new real-cluster mutations or desktop action are enabled.
