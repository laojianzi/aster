# Restricted uninstall eligibility and target preparation

Scope #86 follows #84/#85. **Desktop uninstall execution is not enabled.**
This is a full stored-content restriction check plus an immutable current-target
preparation, not another general inventory and not `operation.Prepared`.

## Supported sampled profile

The complete bounded Secret/release decoder is reused, including exact namespace,
release/revision, storage name/UID/opaque RV/status, gzip, all JSON fields, duplicate
keys and budgets. The sole selected deployed revision must occur in both complete
namespace metadata snapshots; another release may coexist, but an additional
revision of this release rejects the operation even when it is the latest.

The first profile permits 1–32 exact core `v1/ConfigMap` manifests in the same
namespace. Omitted manifest namespace resolves only after exact v1 namespaced
ConfigMap discovery. No guessed plural, preferred-version substitution, foreign
namespace read, absent object, filtering of unsupported documents or partial
result is allowed. Manifest fields are restricted to ConfigMap fields and stored
metadata name/namespace/labels/annotations; duplicates and omitted/explicit
namespace aliases, generated names, lifecycle fields, owners, hooks and resource
policies are rejected. Data keys/types and binary/data key overlap are checked.
A ConfigMap's immutable data flag does not prevent its deletion and is allowed;
immutable release **storage** is rejected because retained status cannot change.

Full stored hooks must be empty. Chart root structure, file lists, canonical paths,
base64 contents, declared dependencies and policy annotations are checked. Any
`crds`/`charts` path segment, locked or declared dependency, library chart,
unsupported root/chart field, ambiguous field spelling or malformed content
rejects the profile. The pinned Helm v4.3.0 chart representation does **not**
serialize its complete dependency tree, so dependency charts are not accepted.
Templates are not rendered or executed. Config/default values are inert, fully
JSON-budget-checked payload, never a second source of executable instructions.

These checks describe the supplied stored representation. They cannot establish
original chart provenance, that the stored release was not edited, historical
CRD absence, authentic ownership, original installed UID, or no external
dependents. Source and live snapshots are not a multi-object transaction. The
future first executor must retain history, use disclosed orphan propagation and
independently confirm the **current reviewed identities**, not imply recursive
release ownership. The general inspector/preview remains intentionally broader.

## Read path and immutable result

`Backend.PrepareHelmUninstall` requires separate full-content acknowledgement.
It reads a complete before snapshot, one exact full Secret, exact-version
ConfigMap discovery and metadata-only individual GETs, then a complete after
snapshot. It reuses the existing current identity/rate limiter/redirect protection
and ten-second **shared** deadline. Existing per-page/content/discovery/object
limits remain; at most 32 live GETs, no full-object fallback or retry. An invalid
before snapshot blocks the sensitive GET; invalid content or discovery blocks
object reads. Denial, cancellation, stale source and malformed responses yield
no preparation.

Every live target must be present, have a safe nonempty UID/RV and exact name/
namespace, match all three Helm metadata markers and have no deletion timestamp,
finalizers, owner references, hooks or resource-policy marker. Markers are checks
of editable metadata, not authentication. Observed timestamps must lie between
the two snapshots; duplicate live UIDs and unresolved scope are rejected.

Results retain only a random opaque ID, source and target identity projections,
summary, session binding and times. Accessors copy slices. They expire 30 real
seconds after the preparation **started**, not 30 seconds after slow work ended.
Backend matching uses a unique nonzero-sized identity token with no credentials;
it does not retain a backend/client merely to compare connection identity.
Zero/nil values, changed session/backend/source, backward time and expiry cannot
match. Cancellation during construction produces no result. A caller must discard
its model reference when inputs, selection, window or connection change. A match
only checks local lifetime/scope; it cannot detect subsequent cluster mutations.

No raw manifest, values, chart, Secret, status patch, token or arbitrary annotation
is kept in the public result. No production UI or HTTP mutation calls this API.
A successful preparation is **not server dry-run or execution permission**.

## Tests and remaining execution gates

Unit/HTTP tests exercise complete-content exclusions, limits, precise routing,
permission, paging/history changes, mutation-free requests, cancelled/expired
construction, defensive slices and backend identity. New named cases are additive
in the existing contract evidence validator: old unit/race/coverage and UI gates
are retained. Disposable-cluster tests check exact current identities, rejection
and unchanged storage/ConfigMap canaries. Each of the seven existing real pinned
Helm install/uninstall cases also checks preparation against actual Helm-created
storage; keep/hooks/missing or mismatched markers reject, and recreated resources
are displayed only with their new current UID. Test fixture writes are isolated
setup/teardown, not product preparation effects.

Before execution activation, separate work must implement last-moment revalidation,
server dry-run for the conditional storage and DELETE requests, independent
single-use execution confirmation, exact conditional effects, validated responses
and absence, ordered partial/Unknown outcomes, explicit reconciliation and private
crash recovery. Existing #84 encoding alone is not authorization. Other Helm
lifecycle/GitOps and release qualification remain separate; enterprise authority
stays deferred P3/outside first MVP. No physical UI/IME/GPU/accessibility/signing
certification follows from these tests.

## Primary sources

- [Pinned Helm chart wire representation](https://github.com/helm/helm/blob/v4.3.0/pkg/chart/v2/chart.go): stored files/templates, unexported dependency tree.
- [Pinned Helm release representation](https://github.com/helm/helm/blob/v4.3.0/pkg/release/v1/release.go): manifests, hooks and apply method.
- [Helm hook lifecycle](https://helm.sh/docs/topics/charts_hooks/): hooks and their separate lifecycle.
- [Helm CRD guidance](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/): CRDs outside ordinary rendered resources.
- [Kubernetes ownership and dependents](https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/): reverse dependencies are not implied absent by an empty ownerReferences list.

The online Helm docs may describe a newer patch. The fixed v4.3.0 source is the
wire-format authority; no dependency update is included in #86.
