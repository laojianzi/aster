# Native local Helm journal archival (#101)

This integration reuses the qualified #99/#100 core. It adds no purge, export,
uncertainty resolution, replay, cluster mutation, dependency migration or general
filesystem reclamation. An archive retains the original WAL bytes on disk and
only frees active quota. Active and archived inventories are independently capped
at 128 records / 16 MiB. A new operation still reserves 256 KiB.

## Loaded identity and worker ownership

A recovery projection now carries a private, inert identity captured through open
file/directory handles and a complete byte digest. It cannot authorize an archive
by itself. Replacing a WAL with identical bytes, or replacing the directory while
preserving the same WAL, invalidates the loaded identity. Corrupt prefixes remain
historical information but receive no identity usable for archive review.

`StartHelmJournalArchive` opens only an existing private directory and lock; it
never initializes or repairs storage. It uses `PrepareLoadedArchive`, which
reuses the existing terminal/unresolved predicate and original core review.
The worker keeps that exact Store open for at most the review's 30-second lifetime
(or the earlier parent deadline). The UI never receives a Store or core consent.
Copies of the session share private command, completion and consumption state.

One separate exact-ID acknowledgement queues one attempt. Wrong confirmation,
cancellation, expiry and parent teardown stop the worker; a second confirmation
cannot create a new attempt. Every worker exit closes the Store before publishing
its result. A discarded UI callback cannot prevent cleanup. A verified Archived
result remains a fact even when cancellation occurs late. An uncertain result is
not retried, and the core's poisoned-Store behavior is unchanged.

## Native interface and lifecycle

The existing recovery pane displays active and retained archived originals with
separate labels. Merely browsing grants no current cluster observation or write
permission. Archive review shows active and archived capacities, retained bytes,
expiry and an exact-record-ID input, plus a separate acknowledgement. Uncommitted
IME composition cannot confirm. The review holds the local journal lock; other
journal actions remain disabled until it finishes or is cancelled.

Workspace/Backend, connection selection, lifecycle epoch, fixed directory and
selected immutable history must still match. Closing the pane/window cancels the
worker even when queued UI callbacks are discarded. Completion invalidates the
local admission snapshot and requires reloading history before another review;
it never silently retargets a record. Historical Unknown remains Unknown and is
not made eligible by a later cluster GET.

## Qualification status

The original unpublished recovery package was not available after interruption.
This candidate was reconstructed from the verified #100 archive, not represented
as recovery of that missing local commit. Old tests, fixed dependency versions,
workflow deadlines and historical evidence fixtures are retained. New test names
are an independent additive census in `helm_native_archive_evidence.py`.

Local race/coverage, Python policy, actual three-platform runs and disposable
Kubernetes comparisons have distinct status; compilation alone is not platform
or cluster runtime evidence. The Issue must remain open until final candidate and
merged-main evidence, actual branch archival/recovery and the project checkpoint
are verified. This document is a candidate contract, not completion evidence or
power-loss/physical-input/signed-release qualification.
