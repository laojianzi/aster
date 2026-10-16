# Selected Helm revision content inspection

Scope #71 builds on completed #69 / #70 metadata-only inventory and #67 / #68
local rendering. It adds a **single selected revision's read-only consistency
summary**, not a release operation, manifest preview/import, full Helm validation,
cryptographic authenticity check, health assessment or write authorization.

## Explicit workflow

Connect using the intended identity and namespace. Open **Helm releases** and
explicitly **Refresh Helm metadata**. **Select revision** binds the chosen row's
namespace, canonical storage name, release/revision/status labels, UID and object
resourceVersion; selection itself does not request content. A separate
**Allow reading this revision's sensitive Secret content** acknowledgement and
**Inspect selected revision** action permit one exact Secret GET. Consent is
consumed on the attempt, including failure. There is no polling or automatic retry.

The result contains only chart name/version (bounded display identifiers),
manifest byte count, hook count and top-level supplied-values count. Values,
manifest text, notes, chart templates and hook content are not displayed,
exported, imported or retained in UI state. Counts refer to the stored strings
and containers, not workloads, effective merged values, readiness or validity of
those contents. Chart identifiers themselves are untrusted observations, not
proof of chart origin or version validity.

**Discard inspection** clears selection, consent and summary. **Stop content
inspection** also cancels the current read. Selecting another row, metadata
refresh/stop/close, namespace/context/backend/session changes and workspace/window
teardown cancel or invalidate content work. Queued results cannot restore a
cleared or newer selection. No manifest buffer, imported inventory, existing
Prepared plan or native Undo/Redo history is changed by inspection.

## Transport and object consistency

The existing resolved identity, authenticated HTTP transport, redirect rejection
and shared rate limiter issue only
`GET /api/v1/namespaces/<namespace>/secrets/<canonical-storage-name>` with JSON
Accept and no list/filter query. There is no CLI, discovery fallback, other
namespace/driver, permission request/grant, full-content LIST, retry or write.
Metadata refresh still negotiates only PartialObjectMetadataList; it never
invokes this decoder or silently falls back to Secret content.

The actual response must be a `v1/Secret` with type `helm.sh/release.v1`, the
selected namespace/storage name/UID/object resourceVersion, matching canonical
owner/name/version/status labels and no deletion timestamp. Missing, replaced,
changed or terminating storage is rejected and the UI requires a new metadata
inventory and selection. Payload name/namespace/version/info.status must match
that selected identity. The check binds the response version only: it does not
lock storage, monitor future changes or establish the latest deployed revision.

**GET secrets authorization is required**, separately from LIST secrets. UI
acknowledgement is consent to a sensitive read, not a permission grant. Secret
content enters process memory even though the UI only retains a summary. Byte
buffers are cleared best-effort; Go strings, allocator copies, crash dumps,
operating-system paging or other process access are not guaranteed erased.

## Strict bounded representation

Kubernetes' base64 field must decode to Helm's canonical padded base64 string,
which must decode to exactly one gzip member containing a JSON release object.
The client validates the gzip trailer/checksum and rejects truncation, extra
bytes or concatenated members. Legacy uncompressed Helm storage, non-Secret
storage drivers and other representations are explicitly unsupported; no
alternate decoder or storage fallback is attempted.

| Limit | Value |
| --- | --- |
| JSON Secret response | 2 MiB |
| Stored Helm base64 string after Kubernetes decoding | 1 MiB |
| Compressed release | 768 KiB |
| Decompressed release JSON | 4 MiB |
| Stored manifest string | 2 MiB |
| JSON nesting / tokens | 64 levels / 100,000 tokens per JSON document |
| Hook entries / top-level supplied values | 128 / 1,024 |
| Complete operation, including rate waiting | 10 seconds |

Both JSON documents are checked completely for duplicate keys, invalid UTF-8,
trailing data and depth/token exhaustion before typed projection. Interpreted
identity fields are case-sensitive; alternate casing is rejected. Unknown
release fields remain inert, but their JSON must still satisfy the budgets and
unambiguous structure. No unknown field is assumed to grant a capability. The
wire Secret envelope is strict. Late malformed input or budget exhaustion
invalidates the entire result even after apparently valid early identity fields.

Chart names use a bounded DNS-style subset; version strings use a 128-byte
ASCII alphanumeric/dot/plus/hyphen display allowlist, not a semver assertion.
Raw manifest/template/value/hook semantics are not validated. All application
errors are fixed messages without server bodies, payload text, credential
errors or URLs. Context cancellation is checked during reads/decompression/JSON
walks and before result publication. Limits bound client work and allocations;
they are not a hard operating-system scheduling or peak-memory guarantee.

## Separate lifecycle scope preview

The separately consented [uninstall scope preview](helm-uninstall-preview.md) (#76)
reuses the same strict read/decoder and exposes bounded stored object identities
without raw payload. The existing **Inspect selected revision** summary remains
unchanged. Neither action is a live dry-run, a write plan or approval.

## Trust boundary and later work

A writer can change both labels and payload consistently. Therefore **consistent
storage/content is not authenticated Helm state, chart provenance, ownership,
readiness, admission acceptance or approval to install/upgrade/rollback/uninstall**.
There is no new Prepared plan, server dry-run, mutation, cluster CLI or automatic
connection to the already-rendered-manifest delivery channel. Lifecycle preview,
real release mutations, cross-revision comparisons and GitOps remain separate
scopes with their own authorization, concurrency, failure/Unknown and confirmation
contracts. P3 enterprise authority remains deferred outside the first MVP.

## Sources and tests

The inspected subset follows Helm v4.3.0's
[Secret driver](https://github.com/helm/helm/blob/v4.3.0/pkg/storage/driver/secrets.go),
[storage codec](https://github.com/helm/helm/blob/v4.3.0/pkg/storage/driver/util.go)
and [release shape](https://github.com/helm/helm/blob/v4.3.0/pkg/release/v1/release.go),
with [Kubernetes API object identity and reads](https://kubernetes.io/docs/reference/using-api/api-concepts/).
No Helm SDK or module/pin change is introduced.

Core tests cover identity/privacy, strict encodings, checksum/trailing/member
rejection, malformed/ambiguous JSON, exact limits and in-flight cancellation.
HTTP/native tests exercise explicit consent, exact current-identity GET, no
fallback/redirect/retry, stale/recreated storage, failure cleanup and queued
scope changes. The real-cluster fixture uses **synthetic Helm-shaped payloads**
in an explicitly opted-in disposable API server, tests LIST-only RBAC versus
GET, proves read nonmutation and concurrent-version/recreation rejection, and
exercises the native inspection flow on the supported Kubernetes matrix. It is
not an actual Helm installation or a claim of complete release compatibility.

All new names are additive to old unit/native/cluster and ordinary/production
migration requirements. Final exact-head PR and independent fresh-main evidence,
guarded retirement and recovery are separate acceptance steps tracked in #71.
Runner tests/screenshots are not physical IME, accessibility, font, GPU/DPI or
signed-release certification.
