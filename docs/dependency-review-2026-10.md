# Dependency review — 2026-10-09

Scope: the user's explicit request to resolve Dependency Dashboard #11, move MyGo
Native to the latest stable release and consider additional low-risk upgrades.
One branch: `deps/mygo-latest-verified`. No parallel Renovate approval PRs are needed
for the same versions. #11 remains the recurring dependency dashboard, not a task to delete.

## Selected and locked

| Component | Previous | Selected | Decision |
| --- | --- | --- | --- |
| Go | 1.27.1 | 1.27.2 | Same toolchain series, official security/bug-fix patch; update all workflows and go.mod together |
| MyGo | v0.2.15 | v0.3.6 | Official GitHub releases/latest and exact release tag, not stale search results or a floating main revision |
| Kubernetes API / apimachinery / client-go / streaming | v0.37.0 | v0.37.1 | Same patch level for all four modules; preserve the tested Kubernetes server matrix |
| sigs.k8s.io/json | 2d320260d730 | v0.0.0-20260909141634-11ed52e25bc5 | Dashboard candidate; retain strict duplicate/unknown/trailing input regressions |
| fxamacker/cbor/v2 | v2.9.1 | v2.9.6 | Same minor series patch |
| go-logr/logr | v1.4.3 | v1.4.4 | Same minor series patch |
| go-openapi/jsonpointer | v1.0.0 | v1.0.2 | Same minor series patch |
| go-openapi/jsonreference | v1.0.0 | v1.0.3 | Same minor series patch |
| google/gnostic-models | v0.7.0 | v0.7.1 | Same minor series patch |
| go.yaml.in/yaml/v3 | v3.0.4 | v3.0.5 | Same minor series patch; strict-manifest tests retained |
| google.golang.org/protobuf | v1.36.12 prerelease revision | v1.36.12 | Prefer the published stable release in the same series |
| actions/checkout | v6.1.0 | v7.0.1 | SHA-pinned; keep read-only permissions and persist-credentials=false |
| actions/setup-go | v6.5.0 | v7.0.0 | SHA-pinned; reviewed ESM/cache migration on hosted runners |
| actions/upload-artifact | v6.0.0 | v7.0.2 | SHA-pinned; retain ZIP archives, names and hash verification |
| actions/setup-node | previous SHA | v7.1.0 | SHA-pinned; only runs the Renovate validator, no registry/auth token setup; keep Node 24 |

Actions pins: checkout `3d3c42e5aac5ba805825da76410c181273ba90b1`, setup-go
`b7ad1dad31e06c5925ef5d2fc7ad053ef454303e`, upload-artifact
`cf430e030ddbb5b0abf93d22962f4752f3646cd9`, setup-node
`949feb2413d6458794dcd2491c4babbbce0c15c1`.

The two temporary read-only dependency-kit runs resolve public modules in CI and
supply a hash-checked offline toolchain/vendor tree for local review. They do not
push automatic dependency commits, execute user kubeconfigs or change main. The
preparation-only workflow is removed from the final tree; existing offline-devkit
and source-evidence workflows remain the maintenance entrypoints.

## Native migration and security invariants

MyGo 0.3 changes build results from pointers to checked `ui.Element` values.
The product wrapper now returns the value API. The upstream terminal view uses
persistent `ui.Services` rather than retaining a build Context. Its original
28 source files are rehashed at v0.3.6; the reviewed hardening patch is rebased,
including the conflict where upstream moved hyperlink opening to Services.
Remote links **remain blocked**; they are not restored by the migration.

The pinned Ghostty commit and platform library hashes are unchanged. Bounded input,
scrollback, screen size, connection-only operation, lifecycle joins, OSC 52 denial,
no runtime executable downloads and constrained Windows DLL loading remain enforced.
Preparation now rejects a MyGo requirement that differs from the terminal source
pin, or a replacement of that module. Existing native tests remain mandatory.

CI additionally runs the MyGo lifetime analyzer and Python provenance/evidence
regressions. Go vet, race tests, the three real Kubernetes versions, the independent
two-cluster scenario, native OS interactions and two-window terminal smoke remain
required. Final results must be recorded against the exact head, generated source,
actual checkout and downloaded artifact hashes; compilation alone is not E2E.

## Deliberately not upgraded in this batch

No blanket `go get -u ./...`: broad zero-major/minor changes to OpenAPI/swag, x/*,
Kubernetes generator/util pseudo-versions and test-only modules are not necessary
to resolve the approved dashboard items. Their behavioral risk should be reviewed
separately rather than inferred solely from a newer number. This is not a claim
that the resulting dependency graph is vulnerability-free.

kind v0.33.0 and metrics-server v0.9.0 were already the current stable releases in
the upstream inventory; keep their tested checksummed fixtures. No speculative
Kubernetes node-image tags are introduced. Renovate automerge remains disabled.

## Primary references

- https://github.com/egoist/mygo/releases/tag/v0.3.6
- https://github.com/egoist/mygo/blob/v0.3.6/docs/ui/migration.md
- https://go.dev/doc/devel/release#go1.27.2
- https://github.com/actions/checkout/releases/tag/v7.0.1
- https://github.com/actions/setup-go/releases/tag/v7.0.0
- https://github.com/actions/upload-artifact/releases/tag/v7.0.2
- https://github.com/actions/setup-node/releases/tag/v7.1.0

A green development regression matrix is not signed-release, physical desktop,
enterprise identity or managed-governance qualification. Remaining product scope
continues in #13.
