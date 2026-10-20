# Native restricted Helm uninstall and recovery

Scope [#94](https://github.com/laojianzi/aster/issues/94) follows the completed
[#92/#93 backend recovery scope](https://github.com/laojianzi/aster/issues/92#issuecomment-6280541769).
It connects the existing preparation, server dry-run, execution, private journal
and fresh GET observer to one native workflow. It does not replace those APIs or
broaden the admitted release profile. The sole implementation is
[PR #95](https://github.com/laojianzi/aster/pull/95) on `feat/native-helm-uninstall`;
completion evidence belongs to #94. This document describes the implementation
and acceptance contract, not a claim that a new candidate, merged main or release
has already qualified.

## Explicit preparation, dry-run and execution

Start from a selected deployed revision in the Helm metadata view. Opening the
restricted uninstall view keeps that selection but clears previous inspection,
preview and live-read acknowledgements. Merely opening a view performs no read
and cannot inherit consent from the broad read-only preview.

The native flow has three separate confirmations:

1. **Preparation reads.** Explicitly allow sensitive stored-release reads and
   restricted uninstall checks. The existing backend reads and validates the
   full source and live identities, returning an immutable preparation. The
   review shows the current context, namespace, release/revision, storage UID/RV,
   target identities and original expiry. No raw Secret, values or manifest is
   placed in the UI model.
2. **Server dry-run.** After reviewing the preparation, independently allow
   PATCH/DELETE dry-run and the fixed orphan policy. The actual backend performs
   bounded current reads and server dry-run. A successful result does not check
   the execution acknowledgement or fill the final confirmation text.
3. **Persistent execution.** Independently agree to delete only the reviewed
   ConfigMaps, retain release history and save the private recovery record, then
   type the exact release name. Active IME composition cannot submit. The
   captured backend, session, namespace, revision, successful dry-run and fixed
   journal path must still match. The first attempt consumes all UI execution
   acknowledgement and typed text and prevents a second attempt from that view.

The accepted profile remains one sole deployed Secret-driver revision and 1–32
same-namespace core/v1 ConfigMaps, with the existing full-content, identity,
ownership-marker, hook/keep/CRD, deletion/finalizer/owner and ambiguity restrictions.
Unsupported input is not reduced to a permissive subset. Marker consistency does
not establish authentic original Helm ownership or absence of external dependents.
The retained-history status changes, UID/resourceVersion-preconditioned individual
deletes, orphan propagation and actual-absence checks remain in the existing
[executor](helm-uninstall-execution.md).

Preparation expires thirty seconds after it started. Dry-run and execution retain
that original expiry and their existing shared ten-second operation deadline.
Painting a frame, finishing dry-run, typing a confirmation or changing a checkbox
does not extend either boundary. Expiry revokes pending authorization and requires
a new explicit preparation; it never starts one automatically.

## Worker lifetime and invalidation

Workbench state remains owned by the native UI thread. Workers capture immutable
inputs and publish only through epoch-checked callbacks. Backend, context/session,
namespace, selected revision and the fixed journal path are checked when an action
is admitted and again when its completion is presented. A result from an earlier
selection or workspace cannot populate a new view.

Stopping, invalidating the source selection, disconnecting, credential expiry and
window/workspace teardown cancel in-flight work and revoke its private consent.
A successful dry-run grant stays alive for its one independent execution mint;
discarding it immediately when preflight returns would invalidate that report.
The controller instead discards it when its lifecycle ends. Stop retains the
in-flight busy state until the accepted worker returns. A live workspace settles
pending-write accounting before deciding whether an old result can still be shown.

Each journal task owns its concrete handle. Execution opens the journal inside
the worker, acknowledges the already confirmed operation, connects context
cancellation to `Discard`, executes, then closes. History loading similarly opens,
lists/recovers, and closes inside its worker. These closes are independent of UI
publication: a window that drops callbacks does not strand the lock. New GET
observations use an immutable loaded recovery and do not keep a disk lock open.

Cancellation cannot retract an already dispatched request. Synchronous filesystem
work is not forcibly interruptible by a Go context, so the worker may take longer
to return than the network deadline; authority is still rechecked before further
dispatch. Other workspaces using the same directory receive a fixed busy error
while a journal task owns the OS lock. They do not share credentials or consent.

## Fixed private storage and retained uncertainty

The native app uses the single leaf `.aster-helm-uninstall` directly under
`os.UserHomeDir()`. Computing this default and opening the panel perform no
filesystem operation. Explicit local history loading or final execution opens the
private store; the final execution confirmation also authorizes saving its bounded
identity ledger. The existing platform implementation creates only that final
directory and validates current-user privacy, exact files and its OS lock.

The parent home must exist. The UI does not create arbitrary parents, repair
existing permissions, choose a cache or temporary directory, accept an alternate
path, or retry by generating a new directory. Invalid privacy, unavailable storage,
corruption and full storage fail closed. This fixed location avoids offering a
convenient UI bypass around the existing unresolved-record guard; it is not an
authentication boundary against another process running as the same OS user.

Records remain bounded by the existing 128-record, 16-MiB store contract. There is
no UI reset, resolve, delete, garbage-collect, purge, repair, replay or resume
operation in this scope. A full or damaged journal requires a separately reviewed
future retention/resolution policy; it must not be silently replaced.

The result view distinguishes **server outcome**, each ordered effect, and
**journal completion**. A final journal error does not erase independently known
server effects. Conversely, a saved terminal frame does not mean success:
`Complete()` can be true while the immutable historical outcome remains Unknown,
Partial or AcceptedPending. An interrupted or damaged tail keeps the checked
per-effect prefix and conservatively reports uncertainty.

## Offline history and new current observations

Local recovery is available while disconnected. Opening it has no kubeconfig,
credential, filesystem or network side effect. **Reload local history** is an
explicit local action and returns bounded records from the fixed store. Selecting
a record only displays its immutable historical identities and effects.

A damaged record remains listed with its integrity error and any fully checked
prefix. It cannot enable the GET confirmation. Other inventory/read/close failures
do not publish a partial inventory that appears complete. Reading the files never
repairs, truncates or rewrites them.

For current cluster facts, connect using the current identity, refresh Helm
metadata, select the matching current revision, reopen recovery and select the
record. Explicitly allow metadata GETs for that record. This creates a new
single-use thirty-second acknowledgement, independent of historical expiry or
old execution consent. The backend enforces the current session, configured
connection binding, namespace and release/revision/storage-UID lineage. Current
resourceVersion and deployed/uninstalling/uninstalled status can differ.

The existing bounded observer brackets the target GETs with complete Secret
metadata snapshots. It distinguishes original-present, original-modified,
replacement-present and exact absence, with the existing limited lifecycle flags.
Failures are not absence and produce no partial successful observation. Current
metadata appears beside historical results; it does not attribute causality,
authenticate a physical cluster or establish full-manifest agreement.

**Unknown remains Unknown after successful GET observation.** The same directory
still blocks a new execution for unresolved matching history. Closing/reopening
the view, reconnecting and rereading history do not clear that condition. This
scope retains that conservative behavior and provides no resolution capability.

## Required evidence

The final PR and actual merged main each require the original four workflows and
their new original source, unit/race, coverage, three Kubernetes versions, native
OS interactions, checked/production migration, OIDC, window and binary artifacts.
Previous #93 runs establish the starting baseline only.

All previous 828 unit and 83 real-cluster Helm contract names, 113 migration roots,
version pins, budgets and timeouts stay in place. The 56 new native/journal names
(19 roots and 37 named subcases) bring the unit Helm contract to 884 requirements.
The real-cluster contract also requires the two native E2E roots, for 141 total;
both migration modes require 169 full names while selecting 132 complete roots.
Their validator rejects missing, skipped, failed or unfinished cases even when a
parent test passed. The original dormant source assertions are migrated to exactly
one native controller and its journal worker; other desktop paths and the CLI
cannot acquire these call sites.

Real native tests cover the three confirmations, typed/IME input, original expiry,
immutable targets, invalidating transitions, worker cancellation and stale
completion, fixed private storage, OS-lock release, corrupt prefixes and separate
historical/current results. `TestNativeHelmUninstallAgainstRealCluster` exercises
the actual successful native path and independent real effects.
`TestNativeHelmRecoveryAgainstRealCluster` loses a reply after a real successful
first DELETE, verifies partial effects, closes the old Workbench and performs
offline load plus new-session GET observation from a fresh Workbench. Existing
backend begin/delete/finish response-loss and changed/replaced-target cases remain.

Issue completion additionally requires the actual-main artifact review, the exact
PR/branch retirement receipt, original full-history bundle restoration, safe local
cleanup and updated #13 checkpoint. Source definitions, local tests or compilation
alone do not satisfy these gates. Physical keyboard/IME, accessibility, GPU/DPI,
signing, installers and release qualification remain separate. Other Helm/GitOps
work remains in #13; #11 dependency decisions and deferred P3 enterprise authority
are unchanged.
