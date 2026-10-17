# Payload-preserving Helm status transitions

Issue #84 continues the completed #80/#81/#82/#83 baseline. This is a **storage
codec**, not another inventory and not an activated uninstall executor. No
production call site sends its output to a server. No desktop execution,
credentialed CLI, permission change, retry, history purge or release claim.

## Narrow lifecycle and sensitive output

`BuildUninstallStatusPatch` accepts the complete bounded Secret response and its
exact selected namespace/name/UID/resourceVersion. It reuses the complete strict
inspection decoder, including all ignored JSON, ambiguity, gzip CRC/single-member,
base64, token/depth/byte and cancellation checks. Storage marked immutable,
terminating, finalized or owned by another object cannot be encoded here.

Only `deployed -> uninstalling` and `uninstalling -> uninstalled` exist. Begin
sets `info.status`, a fixed description and `info.deleted` to the supplied UTC
time. Finish requires an existing nonzero, nonfuture deletion time; it preserves
that original time and changes only status and the fixed completion description.
Contradictory status/time, ambiguous aliases, unsupported shapes or cancellation
produce a private error and **no patch**, not partial output. The caller supplies
its real operation time; the codec does not establish an authorization lifetime.

The result is **sensitive `application/json-patch+json` transport bytes**, not a
reviewed capability. It contains the encoded complete release; do not print,
export, cache in UI state or put it in telemetry. Clear buffers after use;
Go/OS erasure, immutable-string erasure and physical memory secrecy are not claimed.

## Preservation and conditional semantics

The codec splices only the declared `info` values. Every other uncompressed JSON
byte survives, including unknown chart/release/info fields, values, hooks, notes,
exact number spellings, property order, whitespace, escapes and manifest CRLF/LF.
It never rebuilds a release from the lossy `Inspection` summary. Recompression
uses deterministic single-member gzip and the existing base64 wire format;
compressed bytes naturally differ and are not promised identical.

An eight-operation JSON Patch first tests exact Secret name, namespace, UID,
opaque resourceVersion and prior status. It then replaces the status label,
adds/replaces only `modifiedAt`, and replaces only `data.release`. Other Secret
labels, annotations, owners, type and identity are not rewritten. All existing
compressed/release/Secret limits still apply, including growth after rewriting.

The server must apply these tests atomically. A stale/replaced/absent Secret
cannot be accepted as update-or-create. A successful local encoding does NOT
establish server admission, successful persistence, authenticity, target absence,
a transaction over multiple objects or permission to mark uninstall complete.
Each later step must start from newly read and validated storage with the server's
new RV; it cannot reuse the first patch or infer a resource version.

## Evidence and remaining executor prerequisites

Unit tests compare exact lexical bytes, independently apply the patch, test
identity conditions, lifecycle/alias rejection, bounds and cancellation across
validation/output stages. New requirements are additive to the existing unit,
race, coverage and real-cluster evidence validators. No UI test is substituted.

Disposable-kind tests exercise server dry-run without persistence, conditional
fixture updates, stale replays/finishes, denied patch permission, payload/history
retention and an unchanged ConfigMap canary. The existing pinned Helm install
comparison adds a required dry-run against a REAL Helm-created Secret, then
continues the original seven uninstall scenarios unchanged. These mutations are
isolated fixture setup/verification, not product execution or user-cluster access.
Compilation and unexecuted tests do not count as real-server verification.

Still required before an executor/button: sole-revision/full-content eligibility,
restricted ConfigMap profile and hooks/CRD/keep rejection; live UID/RV revalidation;
server dry-run; separate single-use confirmation; ordered conditional effects;
validated acknowledgements and exact absence; partial/Unknown and crash recovery.
The finish codec must not be called merely because a command returned successfully.
The broader contract remains in [helm-uninstall-contract.md](helm-uninstall-contract.md).
Enterprise authority remains deferred **P3**, outside the first MVP.

## Fixed sources

- Helm v4.3.0 `pkg/action/uninstall.go`: status, description and deletion timestamp transitions.
- Helm v4.3.0 `pkg/storage/driver/util.go` and `secrets.go`: gzip/base64 release storage and status/modifiedAt labels.
- Kubernetes API concepts: JSON Patch conditions, resourceVersion and server dry-run.

Source/runtime qualification binds each accepted commit separately; this document
is not a claim that the full executor or a signed release has shipped.
