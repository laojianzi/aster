# Bounded live metadata observations for a stored Helm preview

Scope #78 builds on #76/#77 and is **NOT executable**. It does not uninstall,
create an operation.Prepared, run Helm, acquire a cluster lock, import manifests,
or authorize any mutation. Its success is not a "safe to delete" judgment.

## Explicit action and identity

Create the existing stored-scope preview using its separate sensitive-read consent.
Then opt in to **Allow metadata reads for this preview's live objects** and choose
**Check live metadata**. This consumes only one live-check consent. It does not
reread raw release content. The current backend identity is used throughout.

The result is immutable, uses defensive copies, and belongs to the exact preview
object, session, namespace and selected Secret UID/resourceVersion/revision/status.
It lasts at most **30 seconds**, never past the original two-minute preview expiry.
Production wall/monotonic time controls this lifetime, not the UI test clock.
Refresh, selection, namespace/connection/backend/session changes, Stop/Discard and
teardown revoke the result and consent; queued old callbacks cannot restore them.
A failed new attempt drops any old live result. A changed selected revision drops
the stored preview and requires explicit metadata refresh and new preview consent.
The preexisting editors, Undo/history, delivery inventory and reviewed plans are
not touched.

## Reads and consistency

The check obtains **two snapshots** with the existing complete, bounded,
metadata-only Secret-driver pagination: one before and one after the object reads.
The maximum numeric revision for the selected release must be the exact selected
row in both snapshots. All projected rows for that release must agree, regardless
of order. New/deleted/recreated/changed revision rows fail the check. Resource
versions are opaque strings; collection resource versions can differ between the
two snapshots and are never compared numerically. Unrelated release changes do
not change the selected history, although malformed unrelated metadata still
fails the reused strict decoder.

Between snapshots, fetch fresh discovery for each *exact* stored API version
and require a unique nonsubresource Kind mapping that supports GET. Do not use
preferred versions, guess plurals, substitute another version or fall back to a
broad partial discovery cache. Resolve omitted namespaces only for explicitly
namespaced mappings; explicit namespaces on cluster-scoped mappings are rejected.
Foreign namespaces are **not requested** and are reported as outside this slice.
Cluster-scoped objects with no declared namespace can be read under the explicit
metadata consent and actual current identity. Duplicate resolved group/resource/
namespace/name targets (including different served versions) fail the batch.

Each live GET negotiates only `meta.k8s.io/v1 PartialObjectMetadata`. The exact
name and resolved namespace must match, and present objects need bounded UID and
resourceVersion. Only an exact typed Kubernetes NotFound Status with matching
resource/group/name is absence. HTML/proxy 404s, unknown discovery, RBAC denial,
failed negotiation, malformed/duplicate JSON, unsupported full-object envelopes,
redirects, budget exhaustion and cancellation are not absence or partial success.
No full-object fallback, automatic retry, watch, object LIST or writes are added.
All earlier response bodies are discarded if any stage fails.

This is **not a transaction**: discovery, each GET and the two snapshots happen
at distinct instants. Unrelated API kinds have no shared snapshot here. Objects
may change immediately after reading and labels may lie. Matching end snapshots
cannot exclude transient changes between reads or later changes. A live UID is a
sampled *current* identity, not proof it matches the originally installed object:
the stored preview does not retain an original installed UID. A repeated explicit
check may therefore report a new UID for an object recreated since the last check.

## Projection and limits

Only source identity, resolved resource/namespace, sampled UID/resourceVersion,
observation time, presence, three-way Helm marker result and boolean lifecycle
risks survive. Markers compare `app.kubernetes.io/managed-by=Helm`,
`meta.helm.sh/release-name` and `meta.helm.sh/release-namespace`. Missing and
conflicting values are distinguished, but actual differing values are withheld.
No arbitrary annotations, labels, finalizer names, owner references, spec/status,
Secret data, notes, values or manifests enter the UI projection/log/export.
Metadata itself can be sensitive; the action asks for consent. Byte clearing is
best effort and not a Go/OS secure-erasure guarantee.

Existing 32-object cap applies. There are at most 8 exact-version discovery reads,
512 resources per discovery response, 256 KiB per discovery response, 64 KiB per
object response (4 KiB for typed 404), and 2 MiB combined discovery/object bytes.
Each Secret snapshot retains its existing 16-page/512-row/2 MiB cap, so the whole
operation is bounded by 72 requests and 6 MiB accepted response bytes. A single
10-second deadline includes both snapshots, rate limiting, discovery and GETs,
and cannot extend the original preview lifetime. Byte-cap sentinels may read one
additional byte solely to reject overflow. Over-budget data yields no result.

Termination, finalizers, owner-reference presence, and live hook/resource-policy
annotation presence are risk observations, never a deletion ordering or ownership
guarantee. Stored hook/keep/CRD warnings remain. Hooks, release history, chart CRDs
outside the manifest, dependents, admission and execution are still unplanned.
An empty inventory or all markers matching does not imply an empty or safe uninstall.

## Evidence and remaining qualification

Core, exact-HTTP, native lifecycle/scaled-scroll and disposable-real-cluster cases
are additive and required in the corresponding evidence inventories. Native cases
are required in both checked and production modes. Synthetic Helm-shaped Secret
fixtures are not actual Helm install/uninstall tests. Compilation alone is not
real-cluster execution. Physical IME/AT/GPU/DPI/fonts and release qualification
remain independent, as does cleanup diagnostics hardening. P3 enterprise authority
stays deferred and excluded from the first MVP.

## Primary references reviewed for this slice

- [Kubernetes API consistency, pagination and metadata-only responses](https://kubernetes.io/docs/reference/using-api/api-concepts/).
- [Helm v4.3.0 exact marker comparisons](https://github.com/helm/helm/blob/v4.3.0/pkg/action/validate.go).
- [Helm v4.3.0 uninstall implementation](https://github.com/helm/helm/blob/v4.3.0/pkg/action/uninstall.go).

The above upstream actions are not imported or executed by Aster. Their broader
lifecycle behavior must not be inferred from this metadata-only observation.
