# Trusted local Helm rendering

Scope [#67](https://github.com/laojianzi/aster/issues/67), sole [PR #68](https://github.com/laojianzi/aster/pull/68), extends the existing [reviewed manifest delivery](manifest-delivery.md). This is **local template rendering**, not Helm release install/upgrade/rollback or GitOps reconciliation. The Issue records exact-head/main qualification and recovery; implementation and local test results alone do not establish completion or a production release.

## User workflow

Select an existing namespace, **Connect**, and open **Deliver manifests**. Choose **Render Helm chart**. Supply absolute paths to the approved Helm distribution archive for this OS/architecture, a trusted local chart `.tgz`, and optionally a values YAML file. Enter the render release name and an explicit supported Kubernetes version (`1.35.x`, `1.36.x` or `1.37.x`). The namespace is the visible delivery target, not a separate hidden Helm target.

The Kubernetes version is a **capability assumption**, not a discovered server version or proof that its APIs exist. Local Helm `lookup` does not read the cluster. Existing current-identity discovery and server dry-run remain necessary after rendering. There is no additional API-version override, arbitrary flag, shell command or post-renderer field.

Confirm **I trust this local chart (not sandboxed)**, then choose **Render local Helm**. Aster captures one bounded output and displays its release, namespace, capability assumption, byte count and chart SHA-256. Rendering does not alter the current input, import objects, create approval or contact the cluster. Changing source text, render inputs, namespace, context or connection invalidates the corresponding result. Close/cancel/teardown cancels outstanding work; stale completion cannot restore a usable result.

**Discard Helm result** leaves the original input and its mounted native history intact. To use a capture, explicitly acknowledge **Replace current input; start a new Undo/review history**, then choose **Use Helm result**. This is an intentional replacement boundary, not a history-preserving edit transaction. It uses the captured bytes without re-rendering or re-reading chart/values files, even if those files changed after capture. Review those bytes in **Delivery manifests** before importing. Subsequent editing clears the render-origin label and invalidates review.

**Import manifests**, **Preview next object**, and exact-name **Submit reviewed object** remain separate actions for every object. Import performs fresh discovery and Namespace GET with the current identity. Actual NotFound alone selects POST; existing resources require ownership/omission acknowledgment and no-force SSA with UID/resourceVersion protection. Neither accepting Helm output nor confirming the first object authorizes a later object. Existing conflict, cancellation, partial/Unknown outcome and explicit reconciliation contracts are unchanged.

## Tool provenance and process boundary

Only the embedded [Helm v4.3.0 distribution pins](../internal/helmrender/distributions.json) are admitted: Linux/macOS/Windows, amd64/arm64. The **complete archive SHA-256** must match before Aster extracts and executes its single expected binary in a fresh private temporary directory. The desktop does not download a binary, chart, dependency or values file and does not search the user's PATH for Helm. The manifest links the official release; changing any pin is a separate dependency review, not automatic trust of a new version.

Invocation is fixed client-side `template --dry-run=client`, with the selected namespace/capability version, values through stdin and the preflighted snapshot. CRD inclusion is enabled defensively, but CRDs and subcharts are rejected at input; hooks remain in the output so delivery can reject unsupported lifecycle annotations instead of silently omitting them. Schema validation is disabled only inside this constrained local renderer, because values schemas are rejected. It does not disable Kubernetes strict server dry-run.

The process receives a new environment, empty kubeconfig/registry/repository/plugin locations and no inherited cloud credentials, proxies, loader overrides or user Helm configuration. `HELM_DRIVER=memory` uses a fresh `HELM_MEMORY_DRIVER_DATA` file containing `[]`. Helm v4.3.0's [root initialization](https://github.com/helm/helm/blob/v4.3.0/pkg/cmd/root.go) reads this list even for `template`; omitting it makes the CLI exit before rendering. The correction does not fall back to a user's release store or cluster credentials. Windows retains only its required system-directory environment fields in addition to the fixed private settings.

One process-wide admission slot covers preparation, rendering and reclamation. The existing platform process-tree containment bounds child shutdown and pipe waiting. Temporary cleanup runs on return; OS/filesystem failures can prevent deletion, and no secure-erasure guarantee is made. `GOMEMLIMIT=256MiB` and `GOMAXPROCS=2` are runtime hints, **not a hard memory/CPU sandbox**. File I/O, OS scheduling, cleanup and filesystem-backed storage are not hard real-time guarantees. Local templates must be trusted: this feature does not claim OS-level network/filesystem confinement, comprehensive data-loss prevention, or protection against a compromised user account or pinned executable.

## Accepted inputs and limits

| Boundary | Limit / policy |
| --- | --- |
| Distribution | 64 MiB compressed; complete embedded digest; one expected executable, at most 256 MiB |
| Chart | One dependency-free `apiVersion: v2` application chart; 1 MiB compressed, 4 MiB file payload, 256 KiB per file, 128 entries |
| Archive paths | One root matching chart name; portable ASCII components, at most 256 path bytes / eight separators; no traversal, duplicates/case collisions, links, special files or reserved device names |
| Chart files | Chart.yaml, values.yaml, README.md, LICENSE, templates/ and files/ only; reject charts/, crds/, Chart.lock, .helmignore and values.schema.json |
| Chart metadata / values | Bounded mapping preflight; reject aliases, merge keys, duplicate/ambiguous keys, multiple documents and incomplete source indexes; optional values file at most 256 KiB |
| Release / namespace | Valid bounded lowercase labels; release at most 53 characters, namespace at most 63; selected existing namespace is still checked at import |
| Time / concurrency | 15-second render context including preparation; child helper has a 10-second deadline; one render process per application, cancellation retains admission until reclamation returns |
| Output | At most 2 MiB, 160,000 newlines and 8,192 bytes per line; nonempty valid UTF-8, no NUL; stderr capped at 16 KiB and withheld |
| Delivery | Existing 32-object / one-namespace inventory and separate bounded immutable preview rules; no automatic batch execution |

These deliberately conservative input rules may reject otherwise valid Helm charts. No dependency fetching, namespace/CRD installation, post-renderer, plugin, DNS enabling, secret-store write, automatic retry, rollback, pruning, force or ownership takeover is added. Secret objects, cluster-scoped objects, List wrappers and the existing recognized Helm/GitOps lifecycle annotations are refused by delivery. Rendered text/diffs can still contain sensitive application fields: they are deliberately visible to the user, not universally redacted. Failures use fixed private errors rather than arbitrary template or tool stderr.

## Evidence and development

Core tests exercise actual approved Helm rendering, values/release expansion, offline lookup, immutable capture, fixed failures, output overflow, malicious inherited driver/kube paths, input archives, local-file checks and process cancellation/pipe budgets. Native tests exercise actual controls and Helm, preserved original bytes/history on discard/failure, replacement consent, stale-result rejection, minimum-window visibility and per-object confirmation. Native E2E runs on the existing three-version Kubernetes matrix and checks that render/accept/import/dry-run leave both objects absent, then that each explicit confirmation creates only its own object.

All prior unit/native/migration/cluster requirements remain mandatory. CI prepares a digest-verified Helm archive in every job that executes these tests, retains the original Linux archive in unit evidence and records per-platform provenance. Skipped or missing required Helm tests fail qualification. Captured-result and delivered-result screenshots use distinct names so one cannot overwrite the other.

For local development, explicitly run `python scripts/prepare_helm.py` to fetch the pinned tool archive, then set `ASTER_HELM_ARCHIVE` to the resulting absolute `.aster-helm/helm-v4.3.0-<platform>.<extension>` path before running the tests. This developer-only download is not used by the desktop. Without this environment variable the actual-Helm tests explicitly skip locally; that run is not Helm acceptance evidence. Use the normal pinned terminal preparation as documented in [testing](testing.md).

Physical keyboard/IME, arbitrary giant direct input, cross-view editor history, complete fonts/accessibility/GPU/DPI, signed installation/update and Helm release/GitOps lifecycle remain separate. Enterprise authority stays P3 deferred and outside the first MVP.
