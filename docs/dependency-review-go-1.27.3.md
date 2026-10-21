# Go 1.27.3 security-toolchain review

Review date: 2026-10-21 UTC. Related tracker: #11; sole implementation:
PR #98 / `renovate/go-1.x`. Baseline main is
`e64d22dcbe5cf28a99775ac63efe3fd268b8f1bb`, after completed #96 / #97.
This source document records the admitted change, not advance approval of its
final PR, actual main, or a signed product release. Final evidence belongs in
#98 and the current #13 checkpoint.

## Official change and applicability

Official comparison `go1.27.2...go1.27.3` contains two commits and four paths:
`VERSION`, `src/crypto/x509/verify.go`, `root_darwin.go` and `root_windows.go`.
The patch is `90617fb7ceb59cf08bee7826bb51b8c8fb8c11de`; the release tag resolves
to `2aae1000ea1d4b28923b96db28acf4e3b99d6aff`. The security change is
CVE-2026-111943. Go's Release History dates the release 2026-10-19; the Git tag's
committer timestamp is 2026-10-20T01:26:06Z. These are different source timestamps,
not a reason to rewrite either record.

The patch filters certificate chains containing one of eight SHA-256 SPKI hashes.
It applies to Go's verifier and the macOS/Windows system-verification paths;
Windows preserves a pre-existing verification error, or returns NoValidChains
if the distrust filter removes the remaining chains. Darwin rejects a returned
chain containing a listed key. The key-level filter also covers certificates
reissued/cross-signed with the same key; it is not a general revocation service.
No application bypass, alternate CA, fallback TLS mode or new credential policy
is introduced here.

Aster's OIDC HTTP client (`internal/oidclogin/client.go`) uses crypto/tls with a
minimum TLS 1.2 and optional x509 custom roots. Kubernetes connections use the
existing client-go transport and kubeconfig trust settings. Those paths can use
the updated standard-library verification when certificate verification is on.
This review does not establish that a particular user's server uses any affected
CA. An explicitly insecure kubeconfig remains outside certificate-validation
protection; this update neither enables nor removes that existing setting.
Already-built Aster executables, external Helm/kind executables, plugins and the
system browser are not retroactively patched by changing the Aster compiler.

## Exact repository scope

The bot's initial commit `b1d0786aaf25381dd4a184aa3e76ca2d67082ec7` updates ten
setup-go pins across six workflows. The reviewed continuation also changes
`go.mod` to require Go 1.27.3, updates the current README, adds this review and
strict toolchain regressions, and registers exactly PR98 for guarded retirement.
Other Go modules, go.sum, native library/source pins, Helm pins, Actions SHAs,
Kubernetes images, test selection and runner/application deadlines stay intact.
Older dated reviews and source references retain their historical versions.

`go_toolchain_policy.previous_toolchain_bytes` is an explicit migration ledger,
not a generic allowlist for arbitrary future versions. It first requires the
whole current file's reviewed SHA-256 and exact version-token count, reverses
only this version change, then requires the whole old file's SHA-256. The #96
journal baseline fixture remains byte-identical. Its old test/source and workflow
invariants continue to run against these verified restored bytes. Negative cases
reject changed Actions, permissions, budgets, required names, skipped race,
other dependency updates, removed/duplicated/floating/older/future version pins.

The new mandatory Python regression also executes `go env` with
`GOTOOLCHAIN=local`, verifies the active version and installed VERSION file, and
compares all three x509 files with independently fetched official-tag Git blobs:

| Source | Official Git blob |
| --- | --- |
| verify.go | `6485ce684d7ba26a3fb40870e319d9244c367077` |
| root_windows.go | `3be0c7839a1afe995137d59bc178ee641670ded5` |
| root_darwin.go | `509e5c11ae4f9320febf089f2716c82647fc2cd1` |

When CI supplies ASTER_TEST_ARTIFACTS, it retains a `go-toolchain.json` receipt
with the version and both Git-blob/SHA-256 identities. Wrong compiler versions,
mismatched VERSION and modified files fail closed. These are source/identity
checks, not an executed hostile certificate-chain test or a complete security
audit. Existing OIDC, TLS, credential, three-platform and real-cluster tests
remain necessary, and the new executables must identify Go 1.27.3.

## Initial failure and local provenance

The initial CI run 39554058437 fails four old workflow byte-identity assertions.
Its unit/race/coverage stages were skipped. This expected rejection is retained
as failed evidence; successful companion jobs cannot qualify this candidate.
The continuation does not delete those assertions or regenerate the old fixture.

The existing bot push produced offline kit artifact 12325205029 (81,719,183
bytes, SHA-256 `bc718b21c4d2565f3941d8b630f36e782f87f3e57d6125582066baa0feed99ad`).
Its API size/digest, ZIP CRC and internal tar SHA-256 were checked before local
use. It records Go 1.27.3 and the exact initial bot commit; the three installed
x509 files match the independent official blob identities above. Local package
checks use that compiler, public vendor sources and the approved native library.
The kit's archived source is not the final continuation or main evidence.

The official download catalogue lists the Linux amd64 archive as 70,588,678
bytes with SHA-256 `65efbc80fdc1fa2cd2631cad5b481777a68e8725f34e89f50fc6ac714e379b40`,
and the source archive as 35,134,035 bytes with SHA-256
`6e7749035cf360f06b2575d162ea79df9fda8ef671889e5c375f8e50777f26bb`.
These are independently read expected upstream metadata; the local kit is an
Actions-packaged installed toolchain, not either original download archive.
Do not represent its ZIP hash as the upstream archive hash.

## Qualification and cleanup contract

Final head and actual merged main each need CI, Source evidence, Native migration
verification, OIDC qualification and Renovate configuration. Preserve complete
raw tests and separately check Go version, exact checkout/source, generated
files, native libraries and all six application binaries. Product tests and
evidence counts are not unique-feature or release-completion percentages.

Retirement recognizes only PR98 / renovate/go-1.x, exact main, final head and
ancestry, no active referencing PR, and all five latest main gates. Existing
atomic lease and archive/receipt logic remain unchanged. If hosted Renovate has
already removed the merged branch, report the actual already-absent outcome;
do not invent a guard-generated bundle or deletion. Full merge ancestry must
still preserve the reviewed feature history. #11 stays open while purego and
JSON Patch approvals remain; do not approve-all or close it to hide that queue.

## Primary sources

- https://go.dev/doc/devel/release#go1.27.3
- https://go.dev/dl/?mode=json
- https://github.com/golang/go/compare/go1.27.2...go1.27.3
- https://github.com/golang/go/commit/90617fb7ceb59cf08bee7826bb51b8c8fb8c11de
- https://go.dev/issue/82345
