# Testing and evidence

Prepare pinned native source and library first: `python scripts/dev.py prepare`.
Use `python scripts/dev.py test` for local tests; it sets the test-only library path.
CI performs the same hash-pinned preparation before loading Go packages.

## Layers are not interchangeable

| Layer | Runner / command | What it establishes |
| --- | --- | --- |
| Unit and concurrency | `go vet ./...`; `go test -race -shuffle=on -count=1 ./...` | Pure models, cancellation, bounded queues, defensive protocol behavior |
| Headless native interaction | `ui.NewTester`, included in Go tests | Real MyGo widget tree, focus, typed text and commands without an OS window |
| Real-cluster API + native interaction | `bash scripts/e2e.sh` | Actual Kubernetes API server/controllers plus workbench actions; no fake client |
| Per-OS native interaction | `go test -json ./internal/ui/...` on all three OS runners | Native widget tests on each OS; not physical keyboard/IME certification |
| Per-OS real-window smoke | `scripts/window_smoke.py` | Built executable starts, renders an actual window and closes with the expected marker |
| First-MVP release qualification | Outstanding | Direct-client security, signed installer, upgrade/recovery, real IME, accessibility, mixed-DPI and soak |
| Enterprise authority qualification | Deferred P3, outside first MVP | Gateway/Connector, tenant isolation, authoritative policy/approvals/audit and enterprise session revocation |

`go test -tags=e2e ... -run '^$'` is compilation only. Never report it as passing E2E.
The window-smoke fixture does not connect to Kubernetes; native real-cluster tests are separate.
Deferring enterprise authority does not remove any existing credential, authorization,
mutation, transport or failure-injection test.

## Disposable-cluster contract

Every test fixture must call `testcluster.Config(t)` before accessing a cluster. It requires:

- `ASTER_E2E_ALLOW_DESTRUCTIVE=1`;
- a single absolute `KUBECONFIG` path, never a fallback to `~/.kube/config`;
- `ASTER_E2E_CONTEXT=kind-aster-e2e` or `kind-aster-e2e-<suffix>`, matching current context;
- no external exec or legacy authentication provider in the selected test identity.

This is an accidental-use guard, not cryptographic proof that a remote endpoint is disposable.
The local script creates and owns the cluster. CI creates the named cluster on an isolated runner.
Fixtures use unique namespaces. Test RBAC intentionally has both allowed and denied cases.
Never reuse a production kubeconfig simply by renaming its context.

## Workload rollout regressions

The health interpreter checks controller observation before ready counts, distinguishes
Deployment progress deadlines and pause, StatefulSet rolling partition/start ordinal and
minReadySeconds, OnDelete manual intervention, Pod readiness versus Running, and Job completion.
Unknown custom-resource kinds do not borrow built-in readiness semantics.

The observer pins the resource UID and desired generation, uses bounded polling with read and
overall deadlines, respects retry delay, and stops on authorization errors, deletion, replacement
or a newer generation. Cancellation does not undo a Kubernetes write.

Real-cluster tests create a restricted, non-root HTTP Deployment initially scaled to zero.
They prove dry-run does not persist, execute a reviewed scale, wait for actual controller
observation and availability, and verify the native UI's readiness result. Separate scenarios
replace the resource or advance generation and require tracking to stop rather than report success.

## Command regressions

The real-cluster command tests check separate stdout/stderr, exit code 7, literal shell-like
arguments, output overflow, explicit cancellation and the configured command deadline. A read-only
identity is denied exec; an independent authorized request verifies that the forbidden command
did not create its marker file. A native UI scenario runs a real command, observes its exit,
disconnects a second command and clears the selected resource. Unit protocol tests forbid replay
when the WebSocket handshake returns an unsupported or missing negotiated protocol.

These tests do not claim PTY/interactive terminal support or that disconnection kills the remote
process. Every required E2E scenario must pass without skip on each configured cluster image.

## Evidence rules

CI records `go version` or `kubectl version`, the actual checked-out commit, test JSONL,
`summary.json`, and screenshots. Pull-request CI normally tests GitHub's synthetic merge commit,
not the feature-branch SHA. Record both; do not silently equate them.

`scripts/test_evidence.py` rejects empty, malformed, failing, skipped, unfinished or truncated
package evidence and missing required named tests. A later retry cannot erase an earlier
failure/skip in the same evidence file. The shell uses `pipefail`; evidence validation never
replaces the original test exit code. Validator regressions run with Python's standard unittest.

GitHub Actions artifacts have finite retention. Download reviewed evidence before it expires.
Do not publish kubeconfig credentials, Secret bodies, or customer logs as artifacts. Current
fixtures contain synthetic data only. Development binaries are unsigned and must not be
redistributed as a qualified production release.

## Local Linux smoke

After a build, with the relevant display libraries installed:

```sh
xvfb-run -a -s '-screen 0 1600x1000x24' \
  python3 scripts/window_smoke.py dist/aster artifacts/window.log
```

An offline development-kit workflow can package the pinned toolchain and vendored dependencies
for restricted environments. Verify its SHA-256 manifest before use. It is a development aid,
not a substitute for source review or trusted release provenance.

## Interactive terminal regressions

TTY tests are separate from non-interactive Command tests. They cover interactive stdin,
remote `test -t 0`, terminal size propagation, Unicode, exit status 7, lease/cancellation,
RBAC denial with an independent no-execution check, and native input/lifecycle.
Native adapter cases require the actual hash-verified library, never skip for missing
dependencies, and cover OSC52, bounded input, implicit-shell rejection and cleanup.
Real OS smoke renders the same emulator in two windows; it requires both terminal
render markers in addition to the existing independent-window markers.

Generated source is not checked into Git. Source evidence additionally archives the
generated Go files and their hash manifest/patch hash. Native artifacts include the
verified platform library and primary licenses; these are not signed release artifacts.

## Bounded exec authentication

The native OS matrix also runs `internal/credentialexec`, using real subprocesses on each OS.
Required cases include child-group/job reclamation, output limits, deadlines/cancel, native trust and
expiry. Real Kubernetes coverage requires `TestRealExecCredentialTrustRBACAndExpiry` and
`TestNativeExecCredentialLiveLogsExpireAgainstRealCluster`. No live cluster is used by unit helpers;
E2E fixtures still require the existing explicit disposable-cluster guard.


## Native schema assistance (Issue #14)

Required evidence now includes bounded parser/transport/diagnostics unit cases;
Native Schema Edit, stale-result/expiry, cancellation and minimum-window cases;
`TestRealSchemaBuiltinsAndReadOnlyAssistance`,
`TestRealSchemaDiscoveryAuthorizationIsIndependent`,
`TestRealSchemaStructuralCRDAndPreservedFields`, and
`TestNativeSchemaAssistanceAgainstRealCluster` on each kind version.
Synthetic schema/HTTP fixtures remain unit tests. The independent discovery RBAC
fixture explicitly separates schema reads from object access. Local E2E compilation
is never counted as execution. Artifact summary and exact checkout identities must
be revalidated at the final commit; older counts in this document are historical.

## Issuer sign-out handoff (#37)

The independent Dex workflow additionally requires
`TestNativeOIDCSignOutWithDexAndRealKubernetes`. It verifies provider-owned browser
confirmation and rejection of the prior disposable session cookie, alongside real
namespace RBAC and local live-log teardown. Fixture cookies are private temporary
files and never artifacts. The UI itself claims only a handoff, not remote completion
or JWT revocation. Native sign-out contract tests also run in both migration modes.

## Reviewed manifest delivery (#44)

The new set boundary has required parser/service unit coverage plus the native cases
`TestNativeDeliveryImportPreviewAndConfirm`,
`TestNativeDeliveryPartialUnknownAndReviewAgain`,
`TestNativeDeliveryStaleWorkAndWorkspaceIsolation`, and
`TestNativeDeliveryMinimumWindowUnicodeAndBudget` on each OS and in both migration modes.
They cover real widgets, ordered review, exact confirmation, invalidation, partial outcomes
and workspace isolation. The set service additionally rejects malformed, wrong-identity
or oversized create dry-run responses before publishing a plan. Native result history
retains at most 16 attempts per object with a visible omitted-record count; error status
text is limited to a 2,048-code-point prefix plus a shortening marker. Changing inventory
selection clears the prior preview and confirmation. Synthetic input is not physical
keyboard/IME qualification.

`clipboard-preflight-retains-input` uses actual clipboard shortcut and menu-command paths.
It requires complete separately retained text, zero import requests until explicit discard,
an unchanged source buffer and selection, and preserved native Undo/Redo while the source
editor remains open. Conservative preflight adds current/incoming byte and newline counts,
and their longest-line byte lengths (8,192-byte upper bound), without deducting a selection.
Back retains source and pending paste, but import/inventory/Back do not preserve the previous
source widget's Undo/Redo history or selection. The byte-budget case sends 2 MiB + 1 byte
through real native Type with approximately 128 bytes per line and fewer than 160,000 newlines;
the separate newline case exceeds only the newline budget. Neither case qualifies arbitrary
giant single-segment direct text or IME submission: MyGo can still block in synchronous layout
before the application's next budget check. That upstream safe-editor/release boundary remains
unresolved and is not covered by clipboard preflight.

Every configured Kubernetes version requires
`TestRealManifestDeliveryReviewedSetSafety` and
`TestNativeManifestDeliveryAgainstRealCluster`. The API scenario imports a ConfigMap,
Service and Deployment through fresh discovery, creates each through an independent reviewed
POST plan, then previews/applies new fields and updates fields owned by `aster-apply`.
Changing fields still owned by the original POST manager must conflict. Additional subtests
exercise real UID/resourceVersion races including the last GET-to-PATCH window, concurrent
creation, denied reads and writes, actual committed-response loss, partial progress and
explicit fresh review. A same-named object in a second real namespace must remain denied,
without another dry-run or write, even when the first namespace permits its GET.
The native scenario uses Connect, paste/import, ordered previews,
exact-name submission, existing-resource SSA, accepted-UID inspection and Disconnect.

The shared fixture creates only an existing disposable namespace and provides rendered
manifests; the tested workflow creates the objects. Its Deployment has zero replicas:
these tests establish API delivery and inspection, not running application traffic or
workload availability. Existing real rollout/readiness scenarios remain required.
Local `-tags=e2e -run '^$'` is compilation only; final PR and exact-main raw evidence must
qualify these cases alongside every previously required test before scope completion.


## Exact Schema multipleOf (#46)

Retain the complete existing unit/native/real-cluster/OIDC/migration inventories.
Add mandatory `TestSchemaMultipleOfExactDecodedValues`,
`TestSchemaMultipleOfMatchesIndependentRationalOracle`,
`TestSchemaMultipleOfRejectsMalformedAndInapplicable`,
`TestSchemaMultipleOfBudgetsAndCancellation`, and
`TestSchemaMultipleOfInspectionPreservesInputsAndPrivacy`. The independent big.Rat
oracle uses bounded test exponents; production uses coefficient-bounded modular
arithmetic and tests the full signed exponent boundary without expanding powers.
The original formerly-unsupported multipleOf case retains its name and input,
with its expectation promoted to the exact established violation.

Require `TestNativeSchemaMultipleOfPreservesNumbersAndHistory` and
`TestNativeSchemaMultipleOfMinimumWindowAndIncomplete` in the full unit/native
suites and both checked/production migration modes on each OS. Actual keyboard,
selection, Copy, navigation, correction and Undo/Redo assertions cover the mounted
editor. Minimum-window screenshots and malformed metadata output are inspected;
they do not establish physical IME, all fonts or cross-view native history.

Require `TestRealSchemaMultipleOfAgreeWithDryRun` and
`TestNativeSchemaMultipleOfAgainstRealCluster` on Kubernetes 1.35.8, 1.36.4 and
1.37.0. Their dedicated CRD is established and its fresh current-identity OpenAPI
document is used. Binary-representable multiples are compared with actual strict
dry-run; a separately named nearby-decimal tolerance case observes the server
decision without changing the exact local rule. Draft and live object UID,
resourceVersion and spec must remain unchanged, and dry-run resources must not
persist. Compilation alone is not execution.

The sole PR must retain the exact prior mandatory names and obtain all four
final-head gates. Review original raw streams, archive/API sizes and hashes,
source/provenance, all six full normal/production binaries and window evidence.
After merge, repeat qualification on fresh exact-main artifacts. Complete the
Issue after checking actual guarded retirement and complete-history recovery.
A workflow summary or a waiting retirement report does not substitute for the
corresponding original evidence.

## Portable Schema patterns (#48)

Retain every previously required unit/native/real-cluster/OIDC/migration case.
Add mandatory `TestSchemaPatternPortableMatching`,
`TestSchemaPatternMatchesIndependentFiniteLanguages`,
`TestSchemaPatternRejectsMalformedAndUnsupported`,
`TestSchemaPatternBudgetsAndCancellation`, and
`TestSchemaPatternInspectionPreservesInputsAndPrivacy`. The independent oracle
enumerates six finite languages and uses exact equality or substring membership,
without a regex parser or matcher. Boundary cases cover BMP/astral input, classes
crossing the surrogate interval, strict no-flags anchors, nullable values, wrong
types, unsupported metadata, expansion/work/byte/depth limits and cancellation.

Require `TestNativeSchemaPatternPreservesDraftAndHistory` and
`TestNativeSchemaPatternMinimumWindowAndIncomplete` in the unit/native suites and
both migration modes on each OS. Six minimum-window subcases exercise malformed,
unsupported, wrong-type and over-budget metadata or inputs. Actual source navigation,
copy at the original selection endpoint, correction and mounted Undo/Redo must retain
the draft/Unicode/history contract. The input-budget case sends 16,385 decoded bytes
through native Type with source lines around 128 bytes; it does not qualify a giant
single-segment Type/IME path. Screenshots and byte/history evidence remain separate
from physical keyboard, font and IME release qualification.

Require `TestRealSchemaPatternAgreeWithDryRun` and
`TestNativeSchemaPatternAgainstRealCluster` on every configured Kubernetes version.
The dedicated five-field CRD has 24 create and two live dry-run subcases. Invalid
responses must identify the actual cause fields; successful previews must preserve
the expected spec, including Unicode, empty strings and nullable null. The native
flow corrects two field hints through the real editor and previews actual newline
content decoded from YAML escapes. No create dry-run candidate may persist, and
the live resource must retain its UID/resourceVersion/spec. Empty pattern metadata
is tested locally because the CRD API's serialization omits an empty string; `^$`
is used for the real empty-value constraint.

The older literal-pattern regression retains its name/input and now expects the
known hint. The older native constraints fixture retains its incomplete/privacy
purpose through an unsupported format annotation and additionally requires the
pattern location. No previous mandatory name or old multipleOf case is removed.
Apply the same four final-head and four fresh-main original-evidence gates,
complete binaries, actual guarded retirement and independent recovery requirements
described above before explicitly completing #48.

## Bounded Schema string formats (#50)

Keep every previous mandatory unit/native/real-cluster/OIDC/migration name and
the existing email/private-format incompleteness regressions. Add the five
mandatory core tests `TestSchemaFormatSupportedStringValues`,
`TestSchemaFormatMatchesIndependentExpectations`,
`TestSchemaFormatRejectsMalformedAndInapplicable`,
`TestSchemaFormatBudgetsAndCancellation`, and
`TestSchemaFormatInspectionPreservesInputsAndPrivacy`.
Independent checks compare 5,544 Gregorian date cases using calendar arithmetic,
608 Base64 encoder/ASCII cases, and 576 UUID separator/ASCII cases using the
pinned server's grammar. These 6,728 comparisons are internal oracle iterations,
not separately named Go test records. Boundary cases preserve year zero,
noncanonical Base64 padding bits and every optional UUID separator combination.

Require `TestNativeSchemaFormatPreservesDraftAndHistory` and
`TestNativeSchemaFormatMinimumWindowAndIncomplete` in unit/native suites and both
migration modes on each OS. Four minimum-window subcases cover unknown metadata,
nonstring metadata, type plus malformed metadata and decoded input above 16 KiB.
The oversized value uses native Type with many short source lines; it does not
qualify the upstream giant single-segment Type/IME path. Native source selection,
Copy at the original endpoint, actual corrections, and mounted Undo/Redo must
preserve draft bytes and Unicode history. Review one positive and four minimum
format screenshots from each native OS artifact.

Require `TestRealSchemaFormatAgreeWithDryRun` and
`TestNativeSchemaFormatAgainstRealCluster` on every supported Kubernetes version.
The new four-field CRD publishes `date`, `byte`, `uuid` and nullable `date`. The
backend has 28 create and two live-update cases. Empty byte input must fail while
`AB==`/`AAB=` and mixed UUID separators succeed; field causes must agree with
local `format` or independent `type` hints. Use fresh current-identity OpenAPI,
`FieldValidationStrict` and `DryRunAll`; create candidates must remain NotFound,
and live UID/resourceVersion/spec must not change. The native flow reads the
actual discovered resource, corrects date and byte through the editor, preserves
Undo/Redo, previews without persisting and disconnects through the native control.

Final PR and fresh exact-main qualification each need independent original
artifacts, complete normal/production binaries, paired JSONL names and actual
visual review. Preserve previous source, migration, OIDC, vault, pattern and
multipleOf guards. After merge and fresh main qualification, verify actual guarded
retirement and an independent complete-history restore, then explicitly complete
#50. Premerge references remain neutral so merging cannot close the Issue early.
