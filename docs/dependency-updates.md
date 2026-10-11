# Dependency Dashboard operations

Issue #11 is an ongoing Renovate dashboard, not an unresolved application error.
Keep it open even when its queue is empty. Check actual refs/PRs before approval;
continue an existing update branch instead of opening a manual duplicate.

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
Do not replace the bot body with a handwritten all-clear or close it to hide work.

Other hash-pinned test images, the native library and build tools remain reviewed
build inputs; absence from the dashboard does not imply there are no newer or
vulnerable versions. Their update qualification is separate from this tracking fix.
