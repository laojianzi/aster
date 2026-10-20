# Persistent restricted uninstall history and fresh observation

Scope [#92](https://github.com/laojianzi/aster/issues/92), sole
[PR #93](https://github.com/laojianzi/aster/pull/93), continues the
[restricted executor](helm-uninstall-execution.md). Execution now requires a
private persistent journal selected in its independent confirmation. Reopening
that journal is offline and inert. A separate, newly confirmed GET operation
can observe current metadata. Scope #94 integrates these already qualified APIs
through the separately reviewed [native controls and lifecycle](native-helm-uninstall.md).

## Required execution journal

`OpenHelmUninstallJournal` opens an explicitly selected absolute directory. Pass
the concrete handle as the last required argument to
`AcknowledgeHelmUninstallExecution`. The consent freezes its own wrapper and
shared store state. Changing the caller's wrapper, copying it, or changing an
accessor result cannot redirect the operation. Closing any copy closes the same
store; execution then fails before network I/O. The previous unrecorded execution
signature is not retained as a fallback.

The first execution attempt consumes its original single-use consent before
opening an operation file or making a network request. The journal stores only
the original preparation ID, connection-configuration digest, namespace,
release/revision/Secret UID and opaque RV, ordered ConfigMap identities, original
attempt/expiry times and fixed effect outcomes. It does not store sessions,
consents, clients, kubeconfig, credentials, certificate keys, exec arguments,
Secret contents, chart/values, manifest, patches, request/response bodies or
arbitrary server errors.

Each operation uses a new random ID and an exclusively created append-only WAL.
There is no snapshot replacement, automatic truncation, repair, compaction,
garbage collection, retry, resume, replay, override or history-purge operation.
The protocol applies these steps in the existing executor:

1. Save and sync the bounded original plan before current execution reads.
2. Save and sync the exact begin/delete/finish intent before each persistent
   HTTP request. Recheck cancellation, revocation and the original deadlines
   after disk I/O and before dispatch.
3. Save a strictly validated `AcceptedPending`, `Rejected` or `Unknown` result.
4. Save `Completed` only after the required independent observation. The next
   intent cannot precede this durable observation of the previous effect.
5. Save a terminal frame and close the operation file when the attempt returns.

An intent with no recorded authoritative result recovers as `Unknown`, including
the conservative window after sync but before HTTP dispatch. Already checked
earlier effects remain visible and later entries remain `NotStarted`. The finish
intent records the actual acknowledged uninstalling RV, never a dry-run RV.

Any short write, write/sync/close failure, damaged prefix, unexpected file
identity or lost private-store qualification stops further effects and poisons
the writer/store. Files remain available for later offline inspection; the
executor never retries persistence and then quietly continues mutation.
`JournalID()` identifies the operation file. `JournalComplete()` means this
invocation saved its terminal frame and closed its file, independently of server
success. Thus a normally returned `Unknown` attempt can have a complete journal.

Conversely, a final persistence failure must not erase server facts already
established by strict responses and independent observations. If all effects
were observed complete, the in-memory result remains `Completed` alongside the
journal error and `JournalComplete()==false`. Reopened bytes may contain an older
checked prefix. Both pieces of information are necessary; neither substitutes
for the other.

The same store rejects a new attempt for the same binding/namespace/Secret UID
while its history has an unfinished, partial, pending or unknown operation.
Malformed history that cannot be safely classified blocks new execution. A
fresh GET observation does **not** clear this guard. There is no resolution or
override API in this scope. A different directory or an independent Kubernetes
client is not coordinated by this local lock.

## File and process boundaries

`internal/helmjournal` enforces a 64 KiB frame limit, 256 KiB operation limit,
128 operation files and a 16 MiB store budget. Each length-framed canonical JSON
record has a version, consecutive sequence, predecessor digest and payload
checksum. Strict parsing rejects duplicate/unknown/null fields, invalid UTF-8,
unknown versions, impossible effect transitions, changed target routes/order,
checksum mismatch, torn frames, oversize data and any frame after a terminal.

`IDs()` lists canonical IDs. `Recover(id)` returns defensive value projections.
A damaged file can return only its fully checked prefix plus an explicit error
and `Corrupt()==true`; it cannot mint a network observation. An unfinished or
corrupt attempt never becomes a clean aggregate success or no-op. No load path
constructs a Backend, preparation or consent. These records remain untrusted
history: checksums detect corruption, and do not authenticate Aster, prove
causality, or prevent another process running as the same user from rewriting a
whole file or restoring an older disk snapshot.

On Unix, directory/file permissions must be `0700`/`0600` with current-user
ownership. On Windows, creation and validation use a protected current-user DACL
on an ordinary local fixed volume. Both implementations reject nonregular
files, symlinks/reparse points, multiple hardlinks and changed opened identities.
The directory is anchored, checked before use and checked again around file I/O.
Existing unrelated permissions are rejected, rather than silently rewritten.

The permanent private empty lockfile uses nonblocking `flock` or `LockFileEx`.
No PID timeout, stale-lock deletion or forced takeover exists. A live operation
lease makes `Store.Close` return busy without releasing its lock. Store/Writer
copies share private state and cannot split the lease. Process death releases
the OS lock; a new opener must actually obtain it before proceeding.

The supported claim is **process-crash recovery** with actual file sync and
reopen tests. Arbitrary power loss, broken disks, network filesystems and
malicious same-user rollback are not qualified. Synchronous OS filesystem calls
cannot be forcibly canceled by a Go context. All network authority still shares
the original expiry and ten-second execution deadline; a blocked filesystem
call can delay return, but after it returns an expired operation cannot dispatch.
Windows file sync is used without claiming universal directory-entry durability.
See the primary contracts for [Go File.Sync and filesystem operations](https://pkg.go.dev/os),
[Windows LockFileEx](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-lockfileex)
and [FlushFileBuffers](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-flushfilebuffers).

## New GET-only reconciliation authority

The current caller selects an offline recovery record, establishes the current
Backend/session, selects the corresponding current release and independently
calls `AcknowledgeHelmUninstallReconciliation`. The new consent lasts thirty
seconds and allows one `ObserveHelmUninstallRecovery` attempt with a shared
ten-second read budget. The first attempt consumes it before any I/O, including
failed, stale and canceled attempts. Copies share consumption/revocation;
`Discard` cancels in-flight reads. Reassigning caller-owned wrappers cannot
replace the frozen history. The old execution expiry neither grants a new read
nor prevents a newly confirmed observation long after a crash.

The connection digest is purpose-bound over the configured endpoint/prefix,
TLS server-name/insecure/CA-data/protocol settings and impersonation scope. It
stores none of those fields in cleartext. It rejects differences in this
configuration; it is not physical cluster identity, credential identity, a TLS
root attestation or portable authority. Credentials are excluded because a new
current identity requires its own explicit authorization. Default system trust
roots and application `WrapTransport` code are not authenticated by this digest.
Wrappers remain reviewed application code; the digest cannot attest their
closures or routing behavior. Custom Transport/Dial/Proxy callbacks and a CAFile
without an in-memory CA snapshot cannot qualify this recovery binding. Other
ordinary Backend use is unaffected. Encoded/ambiguous endpoint shapes, userinfo,
queries, fragments and opaque URLs are rejected.

The new grant freezes the actual current Backend identity, session, namespace,
selection and checked history digest. The current release name, revision number,
storage name and storage UID must match the recorded lineage. Current RV and
deployed/uninstalling/uninstalled status can differ from history, because those
are now being observed. A different Backend requires a newly minted read grant,
even when its connection configuration matches.

The separate runner has only GET methods. It reads a complete namespace release
metadata snapshot, each recorded ConfigMap's exact metadata, then another
complete snapshot. Both snapshots must contain the same sole selected revision
for the release. Pagination preserves the existing 16-page/512-revision/256 KiB
page/2 MiB snapshot bounds. Targets use metadata-only negotiation, at most 32
objects and 64 KiB each, with 6 MiB total response budget. Unexpected response
headers, full-object fallback, ambiguous JSON, permission failures, redirects,
budgets and source/history changes fail without a partial success report.

| Current target fact | Meaning |
| --- | --- |
| `original-present` | Same recorded UID and opaque RV are currently visible. |
| `original-modified` | Same UID, different opaque RV; no numeric RV ordering is inferred. |
| `replacement-present` | The same name currently has another UID. |
| `absent` | A bounded, exact typed NotFound identifies the requested resource/name/group. |

Termination, finalizer, owner, hook, keep and limited marker flags accompany the
current facts. Failure is never absence. Results contain no client or raw
metadata. They keep the historical outcome unchanged: observing uninstalling
storage after an unknown begin does not prove that attempt caused the status.
Even uninstalled metadata and all recorded targets absent do not establish full
manifest agreement, historical causality or eligibility to resume writes.

## Backend evidence and native integration

The original 172 required execution names and all older Helm gates remain.
New file/HTTP integration tests check actual write-ahead bytes at the server,
failure before/after each persistent effect, frozen journal destinations,
reopened defensive history and connection binding. Actual executor subprocess
termination separates a server commit from its lost response or later
observation. The parent server and a separate recovery process verify history
and absence of replay. File-only subprocess tests also cover pre-dispatch intent,
locking, corruption and injected disk failures.

The fresh observation tests cover independent consumption, expiry/revocation,
current identity, complete metadata races, exact absence, changed/replaced
targets, bounds and privacy. Real Kubernetes cases drop a successful begin,
DELETE or finish response, reopen the journal and use a new explicit GET grant;
independent clients verify actual server effects. Additional server cases modify
RV or replace UID and require the correct separate current facts with zero new
mutation attempts. These synthetic releases qualify Aster's restricted profile;
the original pinned-Helm lifecycle tests remain mandatory separately.

All new required names join the full unit/race, independent coverage and three
real Kubernetes streams. Linux/macOS/Windows native jobs additionally execute
the journal package on their actual filesystem with a separate strict raw JSONL
validator. Their three streams are included in the existing migration ZIPs:
the normal set remains fourteen artifacts, now with twenty-eight raw streams.
Cross-compilation alone cannot qualify another platform's locks or ACLs.

Final PR and actual merged main each require their own original four gates and
raw source/test/coverage/Kubernetes/native/binary artifacts. The Issue closes
only after actual-main review, guarded branch retirement and independent
full-history bundle restoration. The backend scope is completed in
[#92's receipt](https://github.com/laojianzi/aster/issues/92#issuecomment-6280541769).
Scope #94 separately qualifies native execution/recovery presentation and lifecycle
teardown. It retains unresolved records and their admission block; an API or UI
policy to resolve, clear or purge them remains outside this increment.
Other Helm/GitOps and release targets remain in #13;
enterprise authority remains deferred P3 and #11 dependency decisions separate.
