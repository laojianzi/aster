# Bounded Helm release metadata

Scope #69 continues the completed local-rendering slice #67 / #68. This is an
explicit **read-only Secret-driver revision-label inventory**, not `helm list`
equivalence, decoded release state, install, upgrade, rollback or uninstall.
The implementation does not invoke Helm or add a module dependency.

## Workflow and identity

Connect with the intended identity, select **one namespace**, close any resource
detail editor, then choose **Helm releases**. Opening the panel does not read the
cluster. **Refresh Helm metadata** issues a bounded snapshot request; **Stop Helm
metadata** cancels and clears the result. **Close Helm metadata** returns to the
resource list. There is no polling, retry, credential fallback or permission
request/grant. Namespace, connection/context, backend and workspace changes
invalidate in-flight results. Refresh clears the older snapshot before reading;
errors never leave old or partial rows presented as a successful new result.

The panel is unavailable while a detail editor or write is active. It does not
alter manifest buffers, imported delivery sets or Prepared plans. This does not
extend the editor's separately documented history-across-unmount guarantees.
Rows are ordered by release name, then **numeric revision descending**. Every
row explicitly describes the status as a label. No row authorizes a mutation.

## Data minimization and limits

The existing resolved identity, authenticated transport, redirect rejection and
shared rate limiter issue only `GET /api/v1/namespaces/<namespace>/secrets` with
`labelSelector=owner=helm`, `limit=64`, and the exact Accept media type
`application/json;as=PartialObjectMetadataList;g=meta.k8s.io;v=v1`.
There is no fallback Accept type or request for Secret data, individual objects,
ConfigMap/SQL storage, arbitrary selectors or other namespaces.

The server still authorizes **list secrets**. Content negotiation is not a
field-level authorization boundary or a way around Kubernetes RBAC. A hostile
server can ignore the requested representation; the client bounds received bytes
and rejects wrong envelopes or unexpected data fields, but cannot prevent bytes
already transmitted by that server from reaching the process.

Each successful JSON page is limited to 256 KiB of decoded response bytes. A
complete read is bounded to 2 MiB, 16 pages, 512 revision records, 4,096 bytes per
continuation token and ten seconds including rate-limiter waiting. This is a
bounded client operation, not a hard OS scheduling/memory guarantee. Error bodies
are not read or echoed; requests are not redirected or automatically retried.

A complete consistent list resourceVersion is required across all pages. Looped
tokens, duplicate object names or UIDs, expired pagination, oversize/incomplete
responses, malformed JSON/duplicate fields, unexpected full-object fields or
inconsistent metadata fail without returning a partial inventory. An explicit
new refresh is required after failure. The snapshot does not observe later
changes, and even a successfully empty list means only no matching labels in
this namespace's Secret-driver snapshot.

## Interpretation boundary

Only validated namespace/release/storage names, positive canonical decimal
revision (at most 2,147,483,647) and known Helm status labels reach displayed
rows. The canonical name must be `sh.helm.release.v1.<release>.v<revision>`;
namespace, UID, object resourceVersion and list identity must be present.
Deleting objects, noncanonical names, unknown statuses and malformed labels fail
the entire read. Other annotations and labels are discarded after bounded
metadata validation; values, chart data and manifests are never decoded.

These metadata labels are editable and are **not proof of a valid Helm release,
its payload, current deployed revision, workload health or ownership**. Metadata
responses omit Secret type and data, so neither is authenticated here. A valid
label snapshot can include storage with invalid release data. ConfigMap/SQL
release drivers, alternate storage layouts, deleted revision gaps, release
content comparison, chart/values recovery and mutation lifecycle need separate
admitted scopes. The completed renderer's memory-only local execution contract
is unchanged; it is not connected to cluster release storage.

## Sources and verification

The wire contract follows [Kubernetes metadata-only fetches and paginated lists](https://kubernetes.io/docs/reference/using-api/api-concepts/).
Label projection and naming were checked against Helm v4.3.0's
[Secret driver](https://github.com/helm/helm/blob/v4.3.0/pkg/storage/driver/secrets.go)
and [storage key construction](https://github.com/helm/helm/blob/v4.3.0/pkg/storage/storage.go).
These sources describe the observed subset, not a new library pin.

Core/HTTP tests cover numeric sorting, private field projection, malformed and
ambiguous inputs, full-object refusal, redirects, RBAC errors, pagination,
byte/item/page budgets and cancellation. Native tests cover explicit refresh,
minimum-window controls, error-cleared rows, scope/teardown and queued stale
results. Real Kubernetes fixtures create disposable Helm-shaped Secret metadata
with deliberately invalid release payloads, prove metadata-only pagination and
current-identity denial, and re-read UID/resourceVersion/data after inspection.
The native real-cluster test verifies the same read-only panel flow. These are
real API-server metadata tests, not a claim that a real Helm release was installed.

New names are mandatory in the existing unit/native/three-version cluster and
checked/production migration gates. Final PR and fresh-main evidence and guarded
recoverable retirement are separate acceptance steps recorded in #69. Physical
IME, signed releases, GitOps and P3 enterprise authority are not completed here.
