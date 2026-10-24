# Restricted Helm install capture and read-only preparation

Status: **implementation candidate for #103**, not a persistent install feature.
This increments the qualified `d19a69201e46c615dc51dadb8b565938158a8d38`
baseline. Final PR, actual-main qualification and recoverable branch retirement
are still independent completion gates. No desktop install control is enabled.

## Separate stages and authority

`helmrender.CaptureInstall` obtains one complete client-only release.
`helmrelease.DecodeInstallCapture` validates inert bytes; calling the decoder is
not proof that they came from an authenticated Helm binary. Neither function
creates an `operation.Prepared`, authorizes a cluster write or opens a journal.
`kube.PrepareHelmInstall` is separately acknowledged, current-identity **GET-only**
preparation. Its result is sampled metadata, not server dry-run or a transaction
lock. The two storage codecs return sensitive request bytes but dispatch nothing.

Do not convert the old manifest-only `Render` result to an install or rerender
a chart after review. No installed release is claimed from these prerequisites.

## Pinned client, input snapshot and capability assumption

The approved distribution remains Helm **v4.3.0**, verified by the existing
platform archive SHA-256 allowlist, extraction policy and single-process
admission. Trusted local chart and values files are each bounded and copied into
one private snapshot. The child receives private configuration/cache/data paths,
a memory driver, values on stdin and no inherited kubeconfig, credentials,
plugin search path or proxy configuration. Trusted templates are **not a
sandbox**; explicit input trust remains mandatory.

The only capture command is:

```text
helm install RELEASE PRIVATE_CHART --dry-run=client --namespace NAMESPACE \
  --values - --output json --skip-schema-validation --disable-openapi-validation
```

The existing child-process helper bounds execution, output and cancellation.
Capture checks the full JSON output rather than a display line-length budget.
Child stderr is never returned as a payload-bearing diagnostic. Sources are not
read again after capture; source deletion does not alter the frozen release.

The pinned `install` command does **not** support `--kube-version`. Its client-only
default capability is **v1.37.0**, confirmed with the actual approved executable.
Callers must explicitly accept `InstallCapabilityAssumption`. This assumption is
not live discovery, a compatibility certificate or a hidden flag override.
`lookup` in this mode cannot be substituted for a live read. An actual-binary
regression renders the capability and a lookup while a credential-bearing
canary endpoint remains unused. No `--no-hooks`, `--hide-secret` or `--skip-crds`
switch silently removes unsupported content from the validation surface.

Primary pinned implementation references:
- [v4.3.0 install command](https://github.com/helm/helm/blob/v4.3.0/pkg/cmd/install.go)
- [v4.3.0 install action and client-only memory path](https://github.com/helm/helm/blob/v4.3.0/pkg/action/install.go)
- [v4.3.0 Secret storage driver](https://github.com/helm/helm/blob/v4.3.0/pkg/storage/driver/secrets.go)
- [v4.3.0 storage encoding](https://github.com/helm/helm/blob/v4.3.0/pkg/storage/driver/util.go)

## First profile and privacy

A candidate is one new revision-1 release: matching bounded namespace/name,
`pending-install`, the actual client's `Dry run complete` capture description,
equal nonfuture first/last timestamps, complete chart/config/manifest, no
deletion, and the pinned default `apply_method: ssa`. This field records the
client's output; it does not authorize Aster to execute SSA.

The reused complete profile validator permits **1–32 core/v1 ConfigMaps**, in
one namespace, with unique identities and supported fields. Hook/keep
annotations, CRDs, dependencies, foreign namespaces, non-ConfigMap resources,
server-assigned identity fields, conflicting Helm markers and malformed chart
metadata fail closed. Exact chart times and case/duplicate-member ambiguities
are checked through existing bounded validators. Unknown root/info fields,
duplicate JSON keys, malformed UTF-8, excessive depth/tokens/bytes and partial
manifests do not produce a candidate.

The full captured release is private immutable content. Its formatter and JSON
marshaler expose no chart/value/manifest bytes. Target accessors return copies.
The two-minute candidate lifetime is bounded by the existing preview lifetime.
This is process-local privacy, not encrypted-at-rest storage or guaranteed
erasure of all Go string copies. Temporary byte buffers are cleared where
possible; the immutable candidate remains sensitive memory.

## Pure revision-1 storage and completion codec

`BuildInstallSecret` encodes the frozen release with Helm's gzip/base64 payload
inside Kubernetes Secret JSON/base64, type `helm.sh/release.v1`, exact
`sh.helm.release.v1.NAME.v1` name, namespace and owner/name/version/status labels.
It changes only the dry-run description to `Initial install underway`.
Chart, supplied config, manifest and timestamps remain the single captured
original. No UID/resourceVersion is invented and no existing Secret is adopted.

`BuildInstallDeployedPatch` requires an independently obtained pending Secret:
matching name/namespace/version/status, nonempty exact UID/resourceVersion,
validated storage metadata and **byte-identical decoded pending payload**.
It rejects immutable, terminating, finalizer/owner or unsupported retention
metadata. JSON Patch tests bind type, API/kind, identity, owner/name/version/
status labels and the exact encoded `/data/release` value; only then can the
patch change status/description and update the modification label.

This codec does **not** prove that ConfigMaps exist or that installation
completed. A future executor must establish that fact independently before
using it. Returned JSON bytes contain sensitive data and must not enter logs,
UI, ordinary model serialization or evidence exports. Pure codec tests apply
the patch locally and verify exact preserved member bytes plus UID/RV conflicts;
that local operation is not a server-side qualification.

## GET-only preparation and race boundary

A separate acknowledgement is checked before network access. The original
Backend identity, nonempty current session, exact candidate handle and expiry
are bound to the returned inert result. Invalid/cancelled/expired inputs return
no preparation and cannot silently retarget an operation.

The existing bounded current-identity, rate-limited transports perform:

1. Exact core/v1 discovery for namespaced ConfigMaps/Secrets and cluster-scoped
   Namespaces. All three must advertise `get`; Secrets must also advertise
   `list` for complete history. A second kind claiming the same collection path
   is ambiguous and is rejected before any namespace, object or history read.
   These advertised verbs describe API support, not identity authorization.
   Only then is a present, nonterminating namespace UID/RV sampled.
2. A complete bounded, paginated metadata-only `owner=helm` history. Install
   preparation also accepts the API server's terminal `items:null` empty list:
   the typed metadata envelope, nonempty snapshot resourceVersion, absent
   continuation and absent/zero remaining count must all validate. Missing
   items, contradictory counts, duplicates and payload fields remain rejected.
   The original inventory decoder stays strict; pagination and row validation
   are shared, not reimplemented. **Every**
   status/revision of the requested release blocks a new install, including an
   old uninstalled revision. Partial pages, duplicate identities, inconsistent
   page RVs, continuation errors or denied reads fail closed.
3. Exact-name metadata GET for the reserved revision-1 Secret, which also
   detects an **unlabeled** collision missed by the history selector, then all
   planned ConfigMaps.
4. A second complete history, a repeated reserved-Secret absence read, and a
   second namespace read. History identities and namespace UID/RV must remain
   unchanged.

Absence requires the existing strictly attributed Kubernetes `Status` shape
with code 404, reason NotFound, matching resource/group/name. A proxy 404, denial,
throttle, redirect or malformed response is not absence. Present objects are
never adopted, overwritten or read as permission to proceed.

The total preparation lifetime is 30 seconds (capped by candidate expiry), with
the existing ten-second read deadline and existing discovery/object/page/count/
byte budgets. Bounded histories and live reads have their existing separate
finite budgets. The transport and projection contracts are reused; this scope
does not claim a new universal HTTP response-policy implementation.

The result remains **a sample, never a lock**. Creation/replacement after the
last read, or an ABA change entirely between observations, cannot be ruled out.
A future create must fail on `AlreadyExists`, not turn into update or SSA
adoption. A namespace UID is not a native precondition on a namespaced create;
the later transaction design must explicitly handle namespace replacement
rather than pretend these reads make it atomic.

## Required later scopes before persistent install

Strict current-identity server dry-run must cover immutable reviewed target and
storage bytes, complete admission responses and side-effect uncertainty.
Separate single-use confirmation must bind those exact results and expire on
identity/session/candidate changes. Durable pre-effect journal admission must
precede **every** persistent effect. The uninstall journal schema is unchanged;
it is not repurposed as install recovery without a reviewed schema/migration.

Conditional create/state transitions need verified actual response identity,
observed outcomes and durable partial/Unknown records. Do not guess a committed
result from an HTTP error, mark deployed after an uncertain target creation,
auto-replay/retry a consumed authorization, or automatically rollback/delete
objects after cancellation. Helm install/upgrade/rollback/GitOps/native install
controls remain disabled or separate until their contracts and qualification
are implemented.

## Evidence requirements and current limits

New exact-name requirements supplement `helm_uninstall_contract_evidence`
without removing old names, changing workflow bytes, dependency pins or budgets.
Both unit/race and coverage must include the actual approved-client capture;
a skip due to a missing archive is not qualifying evidence.

The disposable-cluster test independently guards every Backend request as GET,
checks unchanged full canary objects including UID/RV/data, and tests denial,
existing objects, an unlabeled reserved Secret, old release history and a real
independent competing-history creator. Setup/teardown writes belong only to
the explicitly opted-in disposable fixture. Its source compilation is not a
claim that it ran against Kubernetes; each configured cluster must run it in
CI before acceptance. Local results, remote publication, exact-head PR checks,
actual-main checks and recoverable retirement must be recorded separately.
