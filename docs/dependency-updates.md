# Dependency Dashboard operations

Dependency Dashboard is a recurring Renovate control surface, not an unresolved
application error. Enable `dependencyDashboardAutoclose`: let Renovate close it
when no actionable updates remain and ensure a dashboard when new work appears.
The next dashboard need not reuse Issue #11's number. Do not disable monitoring,
remove pending approvals, or close failed update PRs merely to empty the queue.
Check actual refs/PRs before approval; continue an existing update branch instead
of opening a manual duplicate.

This lifecycle-only change retains `dependencyDashboardApproval: true` and
`automerge: false` globally and for MyGo. It does not switch presets, introduce
selective approval or automatically merge dependencies. Those are separate policy
reviews, not incidental effects of autoclose.

## MyGo tracking and trust

Both the Go module and `third_party/native-terminal/UPSTREAM.json` version are
extracted using the same Go datasource and grouped as `mygo native framework`.
Dashboard approval and manual merging remain mandatory. This replaces the old
`enabled: false` rule, which hid the framework entirely. The custom manager can
change only the version string; it cannot update source hashes, patches or library
hashes. The existing module/spec equality check and source/library checks remain
fail-closed. A new upstream version that changes source must be reviewed, not
accepted by regenerating all hashes automatically.

Before merge, resolve and commit verified go.sum changes, review the upstream
compatibility diff, adapt the terminal only where needed, and verify the final
head through CI, Native migration verification, OIDC qualification, Source
evidence and Renovate configuration. Check the merged main afterwards. No floating
latest versions, disabled signature checks or runtime native downloads.

## Verification

The read-only Renovate configuration workflow runs the pinned official validator,
then the actual Renovate CLI in local extract-only mode. The evidence checker
requires exactly one enabled MyGo entry from go.mod and one from UPSTREAM.json,
with equal reviewed versions; it rejects missing, disabled or duplicated entries
and error logs. Python contract tests also verify extraction scope, grouping,
manual approval, unchanged trust hashes after a simulated version replacement,
and module/spec mismatch rejection. This is extraction verification, not a
live hosted-bot lookup, security scan, or full future-update compatibility proof.

Hosted Renovate owns #11's body. After merging config, request its next scan via
the existing manual checkbox and inspect the resulting Detected Dependencies.
Inspect the hosted issue's state and closing actor after a successful scan.
Do not replace the bot body with a handwritten all-clear or close it to hide work.
Only having zero open PRs is insufficient: uncreated approvals, failed, blocked,
rate-limited and scheduled updates are still work. Upstream also retains a
dashboard for deprecated dependencies or replacements without update branches.

The lifecycle suite executes the **unmodified, Git-blob-verified dashboard writer
and checkbox reader from Renovate 44.148.3**, transpiled with Node's built-in
TypeScript stripper. Its platform and unrelated formatting/advisory dependencies
are memory-only test doubles. It verifies close requests on an empty queue;
ensure-issue requests and unchecked approval controls for future/pending work;
error, schedule, rate-limit, blocked and open-PR visibility; completed work,
deprecations/replacements, checked approval parsing and dry-run behavior.
This is an upstream-component contract test, NOT a live GitHub/hosted Renovate
E2E, vulnerability scan or proof of reopening the same issue number. Hosted
closure must be observed separately after merge. An updated upstream source hash
must be reviewed rather than automatically refreshed to get tests passing.

Official behavior: https://docs.renovatebot.com/configuration-options/#dependencydashboardautoclose
and https://docs.renovatebot.com/key-concepts/dashboard/.

Other hash-pinned test images, the native library and build tools remain reviewed
build inputs; absence from the dashboard does not imply there are no newer or
vulnerable versions. Their update qualification is separate from this tracking fix.
