# Restricted uninstall contract and Helm v4.3.0 characterization

Scope #80 continues #77/#79. **Desktop uninstall execution is NOT enabled.**
This increment specifies the first execution profile and tests the already
approved external Helm binary. It does not add another inventory, a product CLI
bridge, a delete button, or an executable capability. Existing stored/live
observations, drafts, history and Prepared plans are unchanged.

## Why the CLI cannot be used as an executor

The fixed comparison target is **Helm v4.3.0**, from the existing
`internal/helmrender/distributions.json`. No dependency or native pin changes.
A successful command, successful dry-run, matching metadata markers, and a
successful Aster metadata observation are four different facts. None is a
complete deletion authorization or proof of the original installed identity.

| Behavior | Pinned-binary characterization | Consequence for Aster |
| --- | --- | --- |
| Dry-run | Reads storage/discovery/live resources; sends no mutations in the observed cases, including a malformed manifest for which it returns success. | Do not accept exit 0 as a complete plan or as server DELETE admission. |
| Markers missing/mismatched | Resource is retained while the command succeeds and history becomes uninstalled. | A successful exit cannot mean every reviewed object disappeared. |
| GET denied | Ownership is unverifiable; the observed resource is retained without turning the command into failure. | Permission denial is not absence and must block an Aster batch. |
| Storage update denied | Resource deletion can succeed while the release remains deployed; the command can still succeed. | Record and verify each release-storage effect independently. |
| Object replaced after the ownership GET | DELETE has no UID/resourceVersion preconditions in this pinned path; a replacement can be deleted. | Bind and enforce current reviewed identity in the actual DELETE request. |
| Partial DELETE failure | One object can be deleted while another remains and storage stays uninstalling. | No transaction, rollback, blanket failure, or automatic retry claim. |
| Cancellation after DELETE commit, before its response | Killing the client does not restore the object or reset history. | Report Unknown for the in-flight effect and reconcile explicitly. |
| keep/history/hooks | Real-cluster cases distinguish retained resources, retained/purged history, repeat uninstall and pre-delete hook suppression/execution. | Do not silently filter objects, purge history or disable hooks. |

The loopback tests use a real approved executable against synthetic API/storage
responses; they are NOT a real Kubernetes server. The separate E2E test performs
actual Helm installation/uninstallation against an explicitly opted-in disposable
kind cluster, checks resource UID/content and release storage, and also runs the
existing Aster readers against the actual Helm-created release. It uses static
ConfigMaps only (including an inert pre-delete ConfigMap hook), no remote chart,
image, Job, plugin, custom resource or cluster-scoped chart resource.

Runtime claims apply only to the named completed tests for an exact commit.
Compilation, unexecuted test definitions and source inspection are not runtime
acceptance. CRD behavior, Jobs, dependent garbage collection, provider-specific
admission and response-loss on a real cluster are not claimed by these fixtures.

## First execution profile: proposed, not activated by this increment

A future executor must reject anything outside this deliberately small profile,
not silently shrink it into a different operation:

- One current session, connection, namespace and explicitly selected release.
  The selected deployed revision must be the sole stored revision for this
  initial profile. Other statuses, multiple revisions and non-Secret storage
  drivers require separate contracts. Preserve release history; no purge.
- Between one and 32 exact `v1/ConfigMap` targets in that namespace. Reject
  Secrets, controllers, service accounts, PVCs, namespaces, CRDs, custom types,
  foreign namespaces, missing objects and duplicate resolved targets.
- Require fresh complete source inspection and discovery; no separate hooks,
  hook markers, chart CRDs, keep/resource-policy markers, deletion timestamps,
  finalizers or owner references. #79's minimal projection alone cannot prove
  all chart-level exclusions. Add a bounded full-content eligibility check;
  an absence of a warning is not proof that a chart contains no such feature.
- All three observed Helm markers must match. This is still not authenticity.
  Confirmation must explicitly identify the *current reviewed objects*, without
  pretending their sampled UID is the UID originally installed by Helm.
- Use orphan propagation for the initial profile. A namespaced object can have
  dependents not represented by its own owner references; do not infer their
  absence. Potential orphaned dependents must be disclosed, not recursively
  deleted or implicitly labeled release-owned.

Hooks/CRD/keep rejection is an Aster restriction, NOT a claim that Helm rejects
those features. `--no-hooks`, ignored unknown objects and preferred-version
substitution cannot be used to make an unsupported release fit the profile.

## Review, consent, and conditional effects

The future API must use a new immutable, short-lived, single-consumption reviewed
capability. A #77 preview or #79 observation cannot be passed as write permission.
Capture session/connection identity, namespace, release storage name/UID/RV,
reviewed object GVR/name/namespace/UID/RV, exact fixed policy, expiry and all
acknowledged limitations. Accessors must not return aliases or expose raw release
payloads; no tokens, values, notes, hook bodies or arbitrary labels are logged.

Refresh/read consent is distinct from execution consent. A dedicated confirmation
must show the exact cluster/namespace/release, each current target and retained
history/orphan consequences. Changed input, selection, expiry, connection,
namespace, backend, teardown or queued old results revoke the capability.
A canceled or consumed confirmation cannot be revived by a late callback.

Required effect order for the first profile:

1. Revalidate exact selected storage and all reviewed identities with current
   credentials; an intervening change rejects the plan. Two snapshots are not a
   lock. No guessed resource route, fallback identity or privilege escalation.
2. Request server dry-run for the conditional release-storage update and each
   individual DELETE, including the exact UID/RV preconditions and fixed orphan
   propagation. Reject incomplete or unsupported dry-run; this is still not a
   guarantee of subsequent admission or of external webhook side effects.
3. Consume the independently confirmed capability once. Conditionally record
   uninstalling in the exact release Secret, preserving all unrelated payload
   fields and the selected identity; never construct a new release from the
   lossy inspection summary. Only proceed after a validated acknowledgement.
4. Send each individual DELETE with `metav1.DeleteOptions.Preconditions` for
   the reviewed UID **and** RV. Keep an ordered per-effect ledger. No collection
   deletion, shell/CLI, force, retry, inferred rollback or parallel write batch.
5. A DELETE acknowledgement means accepted, not necessarily disappeared. Use
   separately bounded reads to establish exact absence; do not turn timeout,
   403, proxy 404 or an unrecognized response into success. Stop scheduling
   further writes on a failure, cancellation, stale identity or Unknown.
6. Only after every admitted target is verifiably absent may the executor
   conditionally mark the retained release uninstalled. Failure/ambiguity of
   this final storage write remains a distinct incomplete/Unknown effect.

Admission and release-storage mutation tests, a safe payload-preserving rewrite,
all current-model confirmation/lifetime guards, UI and actual executor are future
work. This document and its comparison tests do not implement those steps.

## Outcome and recovery contract

| Observation | Required classification |
| --- | --- |
| Canceled before sending the first mutation | Not started; no mutation was attempted. |
| Definitive server rejection for a conditional request | Rejected for that effect; retain prior accepted effects. |
| Successful DELETE acknowledgement, object still visible/terminating | Accepted, pending disappearance; not completed. |
| Transport loss, deadline or cancellation after a request may have been sent | Unknown for that effect; the server might have committed it. |
| Some effects verified, later failure/Unknown | Partial; retain the exact verified outcomes and pending identities. |
| All resource absences and final retained-history update verified | Completed for this restricted profile only. |

No automatic retry after partial/Unknown; no restoring old manifests to emulate
rollback; no resetting release status merely because the client stopped.
Explicit reconciliation must freshly read exact storage and affected targets,
report replacements as replacements, and require a new independent review for
any further writes. An expired ledger is still historical information, never
renewed authority. A restart/crash after dispatch cannot be described as no-op;
crash-recovery storage/privacy policy must be settled before exposing execution.

## Test isolation and mandatory evidence

`uninstall_contract_test.go` invokes the pinned binary with bounded archive
verification/extraction, private temporary paths, an environment rebuilt from
scratch, explicit kubeconfig and disabled plugins. Hermetic tests obtain their
endpoint from `httptest`, inject no cluster credential, limit request/output
sizes and join canceled processes. The API fixture enforces supplied DELETE
preconditions; the race result is not manufactured by a fake ignoring them.

`uninstall_contract_e2e_test.go` first uses the existing explicit destructive-E2E
opt-in and dedicated kind context. It additionally requires a TLS literal-loopback
endpoint and certificate identity, reconstructs only that test config, creates
unique fixture namespaces and registers cleanup. Ambient auth/proxy/plugins are
not inherited by Helm. These guards prevent accidental default-context usage;
opt-in/context naming alone is not proof that a cluster is disposable.

The real-cluster DELETE comparison waits at most three additional real seconds
for **typed NotFound** of the reviewed ConfigMap. This is bounded read-only
observation, not a repeated DELETE, and does not change the Helm command's
existing timeout or wait strategy. Because `--wait=hookOnly` can return while
deletion remains observable, an immediate successful GET is not proof of a
permanent resource; persistent objects, unexpected replacements, 403 and
cancellation still fail the test. The first actual-main test found a remaining object
in this immediate sample on Kubernetes v1.36.4 and v1.37.0; its original failure
artifacts remain nonqualifying evidence. A deterministic test checks eventual
absence, permanent presence, replacement, denial and cancellation.

Typed NotFound must also be a failure envelope for the exact target name and
core ConfigMap resource, with code 404 and no unexpected-server causes. A
client-go-generated proxy 404 is not Kubernetes absence. The predicate checks its
context both before and after each read, so a late NotFound after cancellation or
deadline cannot qualify. Successful GETs must match the requested name/namespace
and reviewed UID. Additional deterministic regressions cover proxy/status/target
ambiguity and cancellation/deadline during the read; these are mandatory in the
full unit/race and coverage gates. The three-second/50-ms observer, original
per-case/CLI deadlines, command flags, and seven real-cluster cases are unchanged.

The real-cluster launcher preserves the original complete E2E/UI package set,
then appends a separate invocation of the new contract test root with all seven
subcases to the same raw stream. It does not rerun unrelated Helm-renderer unit
tests inside the busy cluster job. Those remain mandatory in the full unit/race
and coverage suites. Either invocation's failure fails the job; the second
stream cannot overwrite the first, and the new root/subcases remain mandatory.
No original test is filtered from the original suite or given a longer deadline.

New unit and real-cluster required names and subcases are additive. CI cannot
qualify by skipping the archive, omitting a subcase, hiding failure or only
compiling. Existing UI/migration names stay unchanged because no native UI or
production application behavior is changed. The default binary excludes all
new `_test.go` code. Enterprise authority remains deferred **P3**, outside MVP.

## Fixed primary sources

- [v4.3.0 uninstall action](https://github.com/helm/helm/blob/v4.3.0/pkg/action/uninstall.go): dry-run path, storage updates, ordering, history, partial errors.
- [v4.3.0 ownership validation](https://github.com/helm/helm/blob/v4.3.0/pkg/action/validate.go): exact marker comparisons and unverifiable targets.
- [v4.3.0 Kubernetes client](https://github.com/helm/helm/blob/v4.3.0/pkg/kube/client.go): individual delete options and waiter strategies.
- [v4.3.0 hook execution](https://github.com/helm/helm/blob/v4.3.0/pkg/action/hooks.go): hook ordering/policies and additional effects.
- [Kubernetes API concepts](https://kubernetes.io/docs/reference/using-api/api-concepts/): resource versions, preconditions, dry-run and asynchronous deletion.

The moving online command documentation may describe a newer patch; it is not
the version authority for these tests. Full Helm install/upgrade/rollback/GitOps,
physical IME/AT/GPU/DPI/fonts, signed releases and cleanup diagnostics remain
separate. No first-MVP or production-release completion is claimed here.
