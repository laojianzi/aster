# Branch-retirement recovery

Tracking: existing #21; reuse `fix/mygo-03-widget-lifetimes` after merged #22.
This follow-up only completes its failed repository-maintenance scope.

The migration repair in #22 is already on main `18df85d`. Main CI #101
succeeded; branch cleanup run `38022814849` refused before any mutation.
The plan incorrectly equated a merged PR with ancestry of its original head.
PR #1 is merged at `247cbea1f73ddc914ad2ccf08d6e00a4363f4b41`, but its
reviewed head `365e6983a432df96ede79485db0d21815138ac42` is not an ancestor
of main. The refusal was correct for the old rule; no historical work was lost.

For a non-ancestor head marked merged, the amended classifier requires the
reviewed manifest's exact PR number, closed/merged status, same head repository,
branch and SHA, same main base repository, and ancestry of the actual merge
commit. It then **archives the original head** atomically with its retirement.
It does not treat non-ancestor history as part of the product, nor merge old code.
Missing/mismatched proof, later branch pushes, active PRs and protected refs still
abort the plan. The second preflight revalidates the proof. Existing exact leases,
atomic push, pre-cleanup Git bundle, main verification and one-time receipt remain.

Added tests cover rejected cross-fork/base/head/PR proofs and later pushes, and a
real local bare-Git squash integration with original-history preservation. Existing
atomic lease rejection tests remain. Reports include safe named execution stages,
not raw Git/API errors or credentials.

The reviewed JSON manifest and its candidate hashes are unchanged. Completion of
this repair branch remains tied to the exact expected-head merged PR. Do not open
a duplicate migration or dependency implementation. Cleanup is only considered
complete after the main workflow produces an `applied` receipt and remote refs
and archive tags have been checked. This document is a recovery record, not that
receipt. OIDC and enterprise/release scope remain separately tracked in #13.
