# Reviewed delivery of rendered manifests

This workflow imports a bounded set of already-rendered Kubernetes objects and submits
each object only after its own server preview and exact-name confirmation. It reuses the
existing [immutable operation boundary](apply.md), direct Kubernetes identity and resource
detail views. Scope [#44](https://github.com/laojianzi/aster/issues/44) tracks its final
commit-specific qualification; local compilation or an implementation plan is not that evidence.

## Workflow

1. Select a context and one **already-existing namespace**, then **Connect**. Wildcard or
   empty namespaces are not delivery targets. The delivery panel shows the active context
   and resolved namespace; every object must belong to that namespace.
2. Choose **Deliver manifests**, paste the rendered YAML into **Delivery manifests**, then
   **Import manifests**. This explicitly fetches discovery using the current connection,
   resolves exact API versions/kinds and checks the namespace. Nothing is submitted.
   The current identity needs GET permission on that Namespace; a denied read fails import.
   Aster does not request or grant additional cluster permissions.
3. Review the complete inventory in source order. Missing object namespaces visibly resolve
   to the selected namespace. Import locks the input so a later edit cannot silently change
   a pending approval; **Change manifests** starts a new import/review boundary.
4. Choose **Preview next object**. A fresh GET selects POST/create-only only for an actual
   NotFound. For an existing object, acknowledge **I understand omitted fields may be
   removed**, then preview or **Review remaining again**. Existing-resource intent uses
   strict no-force SSA and includes field-ownership/omission review.
5. Read the actual server dry-run result. Type the exact resource name in **Delivery
   confirmation**, then **Submit reviewed object**. A successful response records that
   object's actual UID and API acceptance. It does not automatically submit the next object.
6. Choose **Preview next object** and repeat the review for each remaining object. Selecting
   another inventory row selects an inspection target and clears the prior preview and
   confirmation; it does not bypass source order. Preview again before submitting.
7. **Delivery outcomes** shows each object's state and its attempts. **Inspect accepted
   resource** pauses the delivery and opens the existing detail view pinned to the accepted
   UID. **Health** is read-only; API acceptance is separate from workload readiness.

**Back to resources** pauses and retains this workspace's source, ordered set, outcomes and
any separately retained clipboard paste. Return through **Deliver manifests**; incomplete
progress requires **Review remaining again**. Source bytes survive leaving the source editor,
but its prior Undo/Redo history and selection are not preserved across import, inventory review
or leaving the panel. Disconnect, credential expiry and workspace closure clear the private
delivery input and invalidate its reviews. Leaving the panel does not undo accepted objects.

## Example input

Choose an existing namespace such as `team-preview`. The missing namespace on these objects
will be resolved to that explicit target and shown in the inventory:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: example-settings
data:
  greeting: hello
---
apiVersion: v1
kind: Service
metadata:
  name: example-web
spec:
  selector:
    app: example-web
  ports:
    - name: http
      port: 8080
      targetPort: 8080
```

This creates no workload by itself. A Service's API acceptance does not prove that matching
Pods or reachable endpoints exist. Adding a Deployment is a separate object and needs its
own review and confirmation in the supplied order.

## Input and discovery limits

| Boundary | Contract |
| --- | --- |
| Entire input | At most 2 MiB of UTF-8, 160,000 newline bytes and 32 nonempty objects |
| Each object | Existing 256 KiB / 5,000-newline limits; resolved JSON must also fit the object byte limit |
| YAML traversal | At most 4,096 scanned document segments; bounded source inspection before decoding |
| Source inspection | Existing 64-level / 20,000-node / 2 MiB index limits; aliases, merge keys, ambiguous keys and incomplete indexing are rejected |
| Accepted text forms | LF, CRLF and JSON objects; BOM and unsupported YAML line endings are rejected |
| Discovery | Fresh current-identity discovery for every import; at most 8,192 supplied resource descriptors |
| Namespace | One explicitly reviewed existing namespace; a conflicting supplied namespace is an error |
| Identity | Exact discovered API group/version/kind; unknown or ambiguous types are errors |
| Repeated objects | Duplicate stored resource identities are rejected, including versions that alias the same resource |
| Per-object attempt history | Latest 16 attempts retained; the count of omitted older records remains visible |
| Error status text | First 2,048 Unicode code points, with a visible shortening marker appended when needed |

Blank/comment-only YAML segments are ignored within the scan budget. Indented block-scalar
text is not interpreted as a document separator. Each object must have a fixed name;
generateName, UID, resourceVersion, managedFields, status, finalizers and ownerReferences are
not accepted as declarative import metadata. Metadata is limited to name, namespace, labels
and annotations, with the lifecycle restrictions below.

The workflow rejects cluster-scoped resources, Namespace/CRD installation, Secret writes
and List-style wrappers. A previously installed, discoverable namespaced custom resource
can use the same reviewed API path; custom admission remains authoritative. It does not
infer dependency order, install a CRD or namespace first, or expand a List into a hidden batch.

The rejected annotation keys/prefixes are `helm.sh/hook*`, `helm.sh/resource-policy`,
`meta.helm.sh/*`, `argocd.argoproj.io/*`, `kustomize.toolkit.fluxcd.io/*`,
`helm.toolkit.fluxcd.io/*`, `reconcile.fluxcd.io/*` and `fluxcd.io/*`.
`kubectl.kubernetes.io/last-applied-configuration` is also rejected: the existing resource
review hides that historical annotation, so importing it would not provide a complete value
review. This is a bounded recognition policy, not a universal detector of every controller
convention. The desktop does not fetch URLs/repositories or invoke a renderer, shell,
hook or plugin. A reviewed Kubernetes workload can run its declared containers;
that is governed by the submitted manifest and server admission.
Inputs that require template rendering or lifecycle orchestration need a separate workflow.

Local parse errors use fixed reasons and the document ordinal, not source fragments or supplied
values. Resource names and resolved targets are deliberately visible in the inventory. The
reviewed diff still contains the user's submitted resource content; arbitrary application
fields can be sensitive. This feature does not introduce a universal redactor or DLP guarantee.

### Native text input and clipboard

Clipboard paste has a separate, conservative preflight before insertion. It checks the current
draft plus incoming text against the 2 MiB and 160,000-newline limits. It also holds a paste when
the current draft's longest line plus the incoming text's longest line exceeds 8,192 bytes.
This is an insertion upper bound: it does not subtract selected text or reconstruct where new
lines would join. A paste that would replace a selection or begin a new line can therefore be
held even when its eventual result would fit. The 8,192-byte check is a clipboard safeguard,
not an additional server-manifest or parser limit.

A held paste remains complete in one separate pending slot. **Delivery retained paste** shows
its byte count, the original draft becomes read-only and **Import manifests** stays disabled.
A second paste cannot replace it. Choose **Discard retained paste** to explicitly discard that
pending input and resume with the original draft; Aster does not truncate the paste or silently
import the old draft. While the source editor remains open, interception preserves its buffer,
selection and native Undo/Redo. The cross-view history limitation above still applies.

Direct native text input that exceeds the total byte/newline budget is retained in full and
import is disabled once the application observes it; **Clear oversized manifests** explicitly
clears that draft. This does not bound every native input path before layout. MyGo v0.4.5 can
still block synchronously on a sufficiently large single text segment delivered through direct
typing or an IME commit, before the next application budget check. Clipboard preflight does not
cover those events. Resolving that upstream editor boundary remains part of safe-editor and
release qualification; this workflow does not claim that arbitrary large input is responsive.

## Create, ownership and updates

Existence lookup, strict dry-run and later execution use the same operation service's client
and session. Forbidden, timeout, malformed response and other read errors never become
NotFound and never trigger a create fallback. Import/discovery is not authorization to write:
Kubernetes RBAC and admission decide each actual API request.

New objects use the existing POST path with field manager `aster-desktop`. A concurrent
creator produces a conflict; Aster cannot silently switch that reviewed plan to apply.
An explicit new review of the now-existing object requires the SSA acknowledgment and pins
its actual UID/resourceVersion.

Before exposing a plan, delivery checks that the complete before/after preview fits the
256 KiB review limit and that the server's after object has the exact name, namespace,
API version and kind. Existing previews must also retain the pinned UID. Empty, malformed,
wrong-target or oversized preview responses do not yield an approvable plan; this check
also covers the create-only dry-run response.

Existing objects use field manager `aster-apply`, `FieldValidation=Strict`, and `Force=false`.
The intent is the imported document, not a copied live object. Omitted fields previously
owned by this workflow can be removed or defaulted. The preview includes the resulting values
and field ownership; a no-value-change apply can still change ownership. Each field that the
workflow should keep managing must remain in its deliberate intent.

**Aster-created objects do not have an automatic ownership migration.** POST-owned fields
can remain owned by `aster-desktop`; changing them through `aster-apply` can conflict. A
new unowned field can be acquired and subsequently updated by `aster-apply`, while another
owner's fields remain protected. Resolve conflicts with the owning workflow and review the
revised intent explicitly. There is no force switch, manager-name shortcut, managedFields
rewrite or implicit fallback to ordinary Edit.

Prepared plans own their exact bytes, session/service identity, operation kind, reviewed
UID/resourceVersion, expiry and one-use capability. Existing-object execution checks the
baseline again and retains UID/RV conditions in the request, including the last read-to-write
race. A deleted or replaced object cannot be silently recreated by the old apply review.
Dry-run does not reserve the resource or guarantee later admission will be identical.

## Partial outcomes, cancellation and re-review

The imported Set is immutable input, not an execution queue. The native workbench owns the
ordered progress and permits only the next uncompleted object to be previewed/submitted.
It does not automatically retry, advance, roll back, prune or claim multi-object atomicity.

Successful earlier objects remain accepted when a later preview or submission fails.
Failed, Unknown and unsubmitted items remain visible separately. Failure or cancellation
invalidates the pending review and pauses progress. **Review remaining again** explicitly
re-reads the current object and runs a fresh dry-run; it is not a resend of the consumed plan.
It still needs a new exact-name confirmation. Earlier successful items are not submitted again.

After a response is lost, the API may have committed even though the UI records **Unknown**.
Reconcile the actual resource and its UID; a fresh review may now select existing-resource
SSA and require ownership acknowledgment. Cancellation never proves that a submitted write
did not happen. Kubernetes and intermediary behavior do not provide an exactly-once or rollback
guarantee through this desktop workflow.

Source edits, namespace/context changes, connection replacement, action-epoch changes, cancel,
expiry and workspace teardown invalidate the corresponding approval. Workers capture the
immutable source/set and original service; stale callbacks cannot populate a different source,
connection or workspace with a usable plan. Submission completion can record the actual old
attempt without granting a new approval. Session history is bounded, in memory and not an audit log.

## MVP scope and evidence

This is reviewed delivery of **already-rendered manifests**. Actual Helm rendering and release
lifecycle, Git credentials/repository synchronization, GitOps reconciliation, dependency-driven
CRD/namespace installation, rollback and pruning remain separate work. It does not complete
the advanced Schema backlog or the upstream safe-completion transaction.

Enterprise authority is deferred **P3 and excluded from the first MVP**. Existing credential
handling, current-identity authorization, reviewed operations and local teardown remain required.
A desktop holding direct cluster credentials is not the enforcement boundary for future tenant,
policy, approval, audit or enterprise revocation services.

Required real-cluster coverage includes three-object create/update, nonmutating dry-run, exact
UID/RV races, ownership conflict, RBAC, committed-response loss, partial progress and explicit
re-review through the new set API. A native E2E drives Connect, paste/import, ordered review,
exact confirmations, existing SSA, accepted-UID inspection and Disconnect. Its test Deployment
is deliberately scaled to zero; application traffic/readiness is not claimed by this fixture.
Existing rollout tests remain required for actual controller and availability behavior.

Unit/native evidence additionally covers parsing/privacy/budgets, source and approval
invalidation, minimum-window controls, stale work, cancellation and workspace isolation.
Clipboard shortcut/menu cases separately check complete retained input, the blocked import,
explicit discard and selection/Undo/Redo within the still-open source editor. The direct-input
byte-overflow case uses 2 MiB + 1 byte with short lines so the byte limit is tested independently
of both the newline limit and the unresolved giant-segment layout path.
Three-OS synthetic native input is distinct from physical keyboard/IME, accessibility, font,
DPI and signed-release qualification. See [testing](testing.md) for the evidence layers and
the final PR/main records linked from #44 for actual qualification.
