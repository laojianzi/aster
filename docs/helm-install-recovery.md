# Read-only install recovery and mixed journal history

Scope: [#111](https://github.com/laojianzi/aster/issues/111), sole branch
`feat/helm-install-recovery`, based on qualified main
`c178e3fbadd5f8e0688d0a4350baebd416d3dc81`.
The [#109/#110 completion record](https://github.com/laojianzi/aster/issues/109#issuecomment-6460505239)
is the accepted persistent-install foundation. This increment reuses that executor,
the v2 journal, existing v1 recovery transport bounds and the native recovery
worker. It adds no install execution controls, replay, resume or new write authority.

## One inventory, explicitly typed history

The existing private journal can contain active and archived uninstall v1 and
install v2 records. Passing every ID to v1 `Recover` rejected valid v2 records and
hid the entire mixed inventory. `RecoverHistory` now performs one checked load
under the shared Store lock and returns an explicitly typed inert projection.
It never retries a failed schema as a different version. Existing v1 `Recover`
and `RecoverArchived` continue to reject v2; existing `RecoverInstall` semantics
and the original WAL bytes remain intact.

Checked corrupt prefixes remain identifiable beside their error. An invalid
archive can have a valid prefix while its load returns `ErrCorrupt`; both v2
projection entry points also mark the copied projection corrupt. A caller that
discards the accompanying error cannot turn that history into read authority.
Unsupported versions or an unsafe inventory fail closed rather than disappear.

`HelmInstallRecovery` exposes defensive metadata: journal/candidate/preparation
IDs, namespace identity, release and canonical storage name, ordered targets,
per-effect UID/resourceVersion/outcome, original deadlines, completion and archive
state. Separate loads of the same ID have separate private identities. It contains
no Store writer, Backend, old session, request payload, credential or reusable
execution confirmation. An unfinished checked prefix remains historically Unknown.

The established native **Reload** contract may initialize a missing private
directory and its lock file. A canceled load creates neither. Reload does not
create, append, repair or rewrite WAL files and makes no automatic cluster request.
The current-metadata observer uses its frozen history without reopening a Store.
This is separate from the existing-only local admission explanation, which leaves
a missing store uninitialized.

## Independent current-read confirmation

Reading offline history grants no network access. The user must separately
acknowledge metadata reads for the exact selected installation record and current
connection. `AcknowledgeHelmInstallReconciliation` binds the grant to the concrete
Backend identity, current connection binding, session, namespace and private
history snapshot. Minting it performs no HTTP request. It does not require a current
Helm revision and does not revive or extend the original execution deadline.

The fresh grant expires after thirty seconds and permits one observation attempt.
Copies share consumption and revocation; invalid or canceled attempts also consume
it. Reassigning a public wrapper or reloading the same journal ID cannot redirect
an existing grant. The worker snapshots its concrete Backend, checks the live
binding again and applies the existing ten-second read timeout, the grant deadline
and the caller's cancellation. Revocation cancels an in-flight request. Dispatch
after rate limiting and result publication both recheck the authority.

Only fixed metadata GET routes derived from the checked plan are reachable.
There are no POST/PATCH/DELETE calls, dry-runs, discovery-based write grants,
background watches, retries, full-Secret fallback or raw release/ConfigMap payload
reads. Existing per-object, per-history-scan and total read budgets remain unchanged.
Content negotiation requires the metadata-only media type; redirects, warning or
encoding anomalies, truncated/oversized/invalid bodies and read/close failures
cannot qualify as a complete observation. Only a bounded, exact typed Kubernetes
NotFound response establishes absence; a bare proxy 404 does not.

## Current facts and original effects stay separate

For one target and a one-page history, one successful observation issues exactly:

1. GET the original namespace metadata and require its recorded UID, with no
   deletion in progress. A later resourceVersion for the same namespace is allowed.
2. Read the complete bounded `owner=helm` Secret metadata history.
3. GET the exact reserved revision-1 Secret metadata, including when it has lost
   its `owner=helm` label and therefore is absent from the filtered history.
4. GET each recorded ConfigMap's metadata in its original order.
5. GET the exact reserved Secret again.
6. Read the complete bounded history again.
7. GET namespace metadata again and verify the same original UID.

Pagination retains the existing per-scan limits. The reserved Secret must agree
with its history entry when labeled as Helm storage. Changed storage or relevant
release history during the read rejects the whole report; no partial result is
published as a successful observation. Namespace replacement, termination or
absence also rejects it. Targets are sampled once each: these reads do not lock
multiple Kubernetes objects or establish an atomic snapshot or continuing readiness.
Newer release revisions already present and stable across the read are reported
as current facts; recovery does not require the original revision to remain sole.

| Report state | Meaning of the sampled metadata |
| --- | --- |
| `original-present` | Recorded UID and last actually acknowledged resourceVersion match |
| `original-modified` | Recorded UID matches, but resourceVersion has changed |
| `replacement-present` | The same name now has another UID |
| `absent` | The exact named route returned a qualifying typed NotFound |
| `unattributed-present` | The object exists, but history contains no accepted UID for that effect |

The Secret uses the accepted create UID and observed resourceVersion, superseded
only by an actually recorded final transition response/observation for that same
UID. The final PATCH's precondition resourceVersion is never treated as its result.
Thus a committed final transition with a lost response can show a changed Secret
while the installation remains historically Unknown. A lost create response leaves
a present object unattributed even if its labels and contents might look plausible.

Metadata markers, owner/finalizer/hook/keep/termination flags and sample times are
bounded explanatory facts. They do not prove creator identity, ownership rights,
unchanged content, a deployed release or workload health. No observation changes
historical effects, completion, WAL bytes, unresolved admission or archive eligibility.
Absent objects do not clear Unknown, and current presence does not convert it to
success. Any later execution requires its own independently admitted scope.

## Native recovery lifecycle

The existing history panel shows both versions and active/archive location, with
installation steps and historical results in a dedicated v2 presentation. Current
metadata appears beside, separately from, those original facts. A new explicit
confirmation starts the read worker; no current revision selection is needed.

Stop, closing the pane/window, or changing Backend, connection, session, namespace,
context or path cancels the read and revokes its grant. Results must still match
the current epoch, selected ID and original private history pointer; an old callback
cannot populate a newly loaded record with the same ID. Failed or canceled current
reads preserve the historical panel. Existing v1 behavior and archive controls
remain intact. V2 records are browsable in both inventories, but this increment
adds no v2 archive, resolution, purge, export or install execution action.

## Regression and qualification

New regression files preserve every one of the 256 accepted Go test files,
all nine workflows, module pins, native source/license pins and hardening inputs.
An independent content-and-mode digest freezes those 271 existing paths. Earlier
scope validators exclude only the seven explicitly named new Go test files; their
original frozen counts and hashes remain unchanged. Previous mandatory test-name
sets are independently frozen before additive recovery requirements.

Backend cases cover checked histories, defensive snapshots, corruption and archive
eligibility, independent copied/revocable/expiring grants, route/media/response
boundaries, namespace and storage/history races, UID attribution, final transition
resourceVersions, byte/page/time bounds, cancellation and unchanged WAL/admission.
The native suite adds 31 exact parent/subtest names across mixed history, all
historical outcomes, corrupt/busy/canceled loads, fresh reads, scope changes and
late callbacks. These names are required in ordinary/native OS/real-cluster-native
and both checked/production migration evidence.

Real disposable Kubernetes regression requires 20 exact parent/subtest names on
each supported version. The real executor establishes successful effects, and
fault injection discards actual successful server responses after Secret creation,
target creation and final PATCH. Additional cases modify, replace or delete targets
and storage, remove the storage owner label, advance namespace resourceVersion,
and reject wrong Backend/session/namespace/snapshot or canceled/revoked grants
without HTTP. The observer audit requires exact GET headers/routes/query and all
seven ordered reads. Every case verifies unchanged history/WAL and, when Unknown,
continued rejection of a second installation by the shared admission guard.

Compilation alone does not establish these cluster results. Final exact-head PR
and actual merged-main workflows each require their own original artifacts, raw
test streams, native ordinary/production binaries and source provenance. The
actual PR number must be registered for recoverable branch retirement; a new
full-history bundle must be restored into an independent empty repository. Those
acceptance facts and their true order belong in #111 and the current #13 checkpoint.
This source contract does not predeclare the later qualification or issue closure.
