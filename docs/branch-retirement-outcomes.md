# Guarded branch retirement and uncertain write outcomes

This maintenance scope continues the recorded #13 cleanup diagnostic gap. It
changes the existing scoped retirement state machine, not Aster application
permissions, Helm behavior, release status or workflow credentials.

## Preconditions remain mandatory

The script handles only the registered PR whose merge equals the exact current
main. It does not select predecessors by age, name or ancestry alone. The PR's
repository, base, head name and tip, current branch protection, active head/base
PR references and complete feature ancestry remain checked. All applicable
latest exact-main push gates must be successful, including Renovate validation
for the existing dependency scopes. A main change fails closed.

The existing full-history `before.bundle` precedes the write. After the archive,
the main, gates, exact branch and absence of the receipt are checked again. One
atomic push leases BOTH the expected branch tip and the nonexistent receipt,
deleting the branch and creating its `maintenance/completed-pr-N` tag together.
No automatic second push, branch recreation, force without lease, or discarded
historical failure report is introduced.

## Reported phases and outcomes

Reports contain fixed phase names, public ref/SHA identities and exception
classes only. They do not contain token values, environment, command output or
transport exception text. The caller retains the report and original archive
on every outcome, including failure after a write.

- `applied`: the write returned successfully and exact remote effects were
  subsequently confirmed while main, gates and completed-PR conditions held.
- `applied-reconciled`: the write raised an exception, whose class is retained
  as `write_error_type`; a single read-only reconciliation then proved the same
  exact branch absence and receipt SHA plus all other conditions. This is not
  an unqualified claim that the original write returned successfully.
- `outcome-unknown`: a write was attempted but the exact effects could not be
  established. This fails the job and does not retry the mutation. A later run
  may inspect the unchanged remote state, but cannot rewrite the earlier report.
- `already-recorded`: an exact receipt is present, the registered branch is
  absent, feature history is reachable, no active PR references it, and current
  main and all gates are rechecked. A tag alone is never sufficient.
- `already-absent`, `waiting-for-verification` and `no-registered-completion`
  remain observations, not deletion or archive receipts.

Wrong receipts, a branch still present beside a tag, replacement/protected
branches, failed gates, active PRs and unavailable/ambiguous reads cannot produce
a qualifying success. Tests inject acknowledged writes, committed response
loss, denied writes, inconsistent partial observations, late main/gate changes
and later read-only reconciliation without contacting GitHub.

## Continuation from the Helm contract work

The accepted correction #82 does not retroactively make failed #81 main
`8bc09922...` a passing checkpoint. Its still-retained
`test/helm-uninstall-contract` branch can be fast-forwarded, with an exact old-tip
lease, to this independently reviewed maintenance continuation after corrected
main qualification. This is branch reuse, not a repeated implementation or a
rewrite of #81's history. Only the actual new maintenance PR number will be
registered after creation. After its own merge and fresh-main verification, the
normal guard can retire this reused branch under that PR's exact receipt. No
`completed-pr-81` tag or successful old-main result is manufactured.

The previous #77 generic ValueError remains historical evidence; this change
does not establish its precise root cause. New diagnostics prevent a similar
future ambiguity from being silently reported as a clean write. Physical-device
and signed-release qualification remain separate, and enterprise authority
remains deferred P3 outside the first MVP.
