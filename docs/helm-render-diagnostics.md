# Bounded local Helm capture diagnostics

[Issue #125](https://github.com/laojianzi/aster/issues/125) records repeated Windows
capture failures that blocked [PR #124](https://github.com/laojianzi/aster/pull/124).
The two attempts of the same candidate are preserved as failures. One returned
the fixed rendering error; the other reached the renderer context deadline.
Those observations do not identify the slow stage or establish a common cause.

## What a failure can explain

The existing local-render UI keeps its original error and appends a compact
`Helm capture diagnostic v1:` summary. It describes the stages actually entered,
their elapsed times and fixed outcome categories. An omitted or unentered stage
does not acquire an invented timing. A new successful capture clears the old
failure status, and an old callback cannot publish diagnostics into a newer
review.

The explicit `RenderWithDiagnostics` and `RunRenderHelperWithDiagnostics` APIs
return a report owned by that invocation. The original APIs retain their error
identities and ordering. There is no shared last-error record, telemetry upload,
diagnostic file, additional render or automatic retry.

Renderer stages cover input snapshots, archive reading and digest verification,
private directory creation, executable extraction, private input/environment
preparation, helper execution, output validation and cleanup. The helper records
fixed startup phases, waiting and reclamation. Windows startup still creates and
configures a Job Object, starts suspended, assigns the process to that job and
then resumes the initial thread. Other platforms mark inapplicable phases as
not entered.

Public outcome, sampled context state and cleanup result are separate facts.
For example, cleanup may cross a context deadline after a result was already
selected. Diagnostics must not relabel the original return or replace the
failure stage with cleanup. Context state is sampled before the implementation's
own deferred cancellation, and cancellation callbacks are joined before the
helper records its final outcome. Output overflow retains its original priority.

## Privacy and bounds

Reports use fixed enums and monotonic elapsed durations. There are at most
sixteen fixed phase slots, including the nested helper, and the encoded report
and summary are limited to 4 KiB. The helper report is separately limited to
2 KiB. Reaching a diagnostic bound truncates only the diagnostic; it cannot
change the execution result, suppress cleanup or accept output.

The report does not retain or format raw error strings, chart or values content,
manifests, stdout/stderr, paths, arguments, release/namespace identifiers,
environment values, credentials or hashes of those private inputs. Ordinary
render result summaries retain their separately documented chart fingerprint;
that fingerprint is not added to the failure diagnostic.

## Existing execution contract

The complete approved distribution is read and verified on every capture. Its
executable is extracted into a new private directory for that invocation. No
executable cache, PATH fallback, shell, inherited credential environment or
containment bypass is introduced. The render context remains fifteen seconds,
the child helper deadline ten seconds, and all input/output budgets stay fixed.
OS calls, file operations and cleanup still lack hard real-time guarantees.

The diagnostics identify observed stages and categories. They do not prove that
antivirus, the runner, filesystem latency, a Job Object operation or another
component caused either historical failure. A later successful CI attempt also
does not prove the original cause was repaired.

## Verification scope

The increment adds three precise Go test files for the helper, renderer and
native UI. They exercise actual child processes and approved Helm where needed,
privacy canaries, public-error compatibility, early cancellation, context
sampling, output limits, cleanup, report bounds and stale UI callbacks. Applicable
names are mandatory in the existing unit, native, migration and cluster streams.
No existing Go assertion or workflow budget is removed or weakened.

The separate native-install census remains 42 ordinary names plus four real
cluster names. Diagnostic evidence is additional; it cannot substitute for the
install flow, its per-stage consent or its final PR/main qualification. Exact
completed run identities and acceptance remain in #125, #123 and PR #124.
