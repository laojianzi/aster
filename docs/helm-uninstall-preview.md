# Selected-revision uninstall scope preview

Scope #76 is the first, deliberately **NOT executable** slice of Helm lifecycle
preview. It reuses #70's metadata inventory and #72's sensitive-content decoder
and exact authenticated GET. It is not `helm uninstall --dry-run`, a forecast of
what Helm will delete, a live ownership check, or a deletion capability.

## Workflow

Connect to the intended cluster/identity and one namespace, open **Helm releases**,
then explicitly **Refresh Helm metadata** and **Select revision**. Selection and
opening the panel do not fetch sensitive content. Acknowledge **Allow reading this
revision's sensitive Secret content**, then choose **Preview uninstall scope**.
The same one-attempt consent can instead request the existing **Inspect selected
revision** summary; either action consumes it. Busy/current-model guards and a
selection/attempt-specific input key prevent queued input restoring old consent.

The preview replaces the metadata rows with an independently scrollable stored
inventory. Its explanatory observations scroll with the descriptors, while the
nonexecution warning and consent/discard controls remain outside that viewport.
A fresh preview identity starts a new scroll viewport; no text-scale clamp is used. Each row exposes only validated API version/kind, name, declared
namespace and risk flags. Missing namespace is explicitly unresolved, not assumed
to be cluster-scoped or to name a live target. Foreign namespaces are marked;
no requests to those namespaces are made. Secret objects expose identity only,
never data/stringData. Arbitrary annotation values, values, notes, templates,
raw manifests and hook content never enter UI state, history, clipboard or export.
Object names/chart identifiers themselves are untrusted observations.

The preview is bound to the workspace session, namespace, selected storage key,
UID/resourceVersion, revision and status. It owns its descriptors and returns
copies. Its random opaque identifier is not an exposed hash of secret content.
It expires after **2 minutes**, using the real clock, including time queued before
UI publication. Frame repaint scheduling does not replace this lifetime check.
**Discard inspection**, stop, refresh, select another row, connection/context,
namespace, backend/session changes and teardown clear it. A queued stale result
cannot restore it. Expiry never automatically rereads. Existing drafts, native
history, imported delivery inventories and Prepared plans are not modified.

## Limits and all-or-nothing behavior

The shared ten-second operation deadline includes rate-limit waiting, transport,
decode and projection. The existing #72 strict Secret type/metadata/labels,
canonical double base64, single gzip member/checksum, full duplicate-key JSON
validation and byte/depth/token limits all remain. It uses only a single exact
Secret GET, no full-content LIST, CLI, fallback, redirects, retries or writes.
`get secrets` authorization is required; consent is not an RBAC grant.

Stored manifests are limited to 2 MiB / 40,000 newline bytes, 128 reader segments
and 32 nonempty objects. Each object reuses the existing source AST and single
manifest decoder: at most 256 KiB / 5,000 newline bytes and its existing node/depth
budgets. UTF-8 with LF/CRLF only; aliases, merges, duplicate keys, lists,
generateName and unsafe/ambiguous identities are rejected. Names are restricted
to a DNS-subdomain subset; this is narrower than all possible Kubernetes names.
Versions and potential omitted/default namespace duplicate identities are
rejected conservatively. Without discovery, cross-GVK aliases remain unresolved.
No partial inventory is published on late malformed data, budget exhaustion,
cancellation or storage mismatch. Errors are fixed strings without parser/server
payloads. The bounded synchronous YAML parser is not an OS scheduling guarantee.

## Lifecycle boundaries

The selected revision is **not necessarily the latest** or deployed revision.
The report marks historical/pending/unknown status and does not infer eligibility.
Separate stored hooks are counted, never executed or treated as ordinary objects;
hook annotations in the stored manifest are marked without interpreting events,
weights or deletion policies. Exact `helm.sh/resource-policy: keep` is reported
as an observation, not a guarantee; unknown/noncanonical policies are rejected.
CRDs in the manifest are marked. Chart CRDs outside it, subchart behavior,
resource history, hook effects and dependent resources are not inventoried.
An empty stored manifest therefore does not imply an empty uninstall.

Rows are in **source order**, not Helm's deletion order. There is **no server dry-run**,
API discovery, live object UID/resourceVersion or live ownership verification.
No latest-revision locking, propagation policy, wait policy, hook execution,
release-history purge, write confirmation, mutation or automatic rollback exists
in this increment. A consistent storage writer can forge both labels and payload;
this is not authenticity, chart provenance, ownership or permission to delete.
Every report, including one without risk flags, remains nonexecutable. There is
no path from this type to operation.Prepared or manifest delivery.

The pinned Helm v4.3.0 [uninstall implementation](https://github.com/helm/helm/blob/v4.3.0/pkg/action/uninstall.go)
performs additional history, ownership and lifecycle work; its behavior must not
be inferred from these stored descriptors. See also [hooks](https://helm.sh/docs/topics/charts_hooks/),
[the strict content contract](helm-revision-inspection.md) and
[reviewed manifest delivery](manifest-delivery.md). Future install/upgrade/rollback/
uninstall and GitOps require separately reviewed executable boundaries.

Sensitive content enters process memory. Clearing buffers is best effort, not
secure erasure of Go strings, allocations, dumps or OS paging. No new persistence,
permission, dependency or tool pin is added. Enterprise authority remains deferred
**P3**, outside first MVP.

## Verification

Core tests cover immutable identity/lifetime/privacy, strict/late malformed input,
exact limits and cancellation. HTTP tests enforce one consented current-identity
GET, private errors, no redirects/retries and stale/recreated object rejection.
Native tests cover one-shot/busy consent, queued results across scope teardown,
expiry, failure, unchanged drafts/plans and scrollable 100/125/150% LTR/RTL-host
layouts. Both checked and production migration require the new native tests.
Real-cluster fixtures use synthetic Helm-shaped storage plus an unrelated live
ConfigMap canary, LIST-only RBAC, concurrent updates/recreation and native preview;
they are not actual Helm installation/uninstallation tests. Physical IME,
accessibility, all fonts/scale/locales/GPU/DPI and signed release remain separate.
Final exact-head PR and independent main evidence are recorded in #76, not inferred
from local test completion or this document.
