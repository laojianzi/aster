# Reviewed server-side apply (existing resources)

Open a resource, choose **Edit → Apply**, and provide a minimal intent document.
Aster deliberately starts from identity only, not the live resource. Add exactly
those fields that the `aster-apply` workflow should manage. Do not copy UID,
resourceVersion, managedFields, status, finalizers or ownerReferences into intent.
The current scope rejects them rather than silently dropping them.

Example for an existing ConfigMap:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: example
  namespace: team
data:
  managed-key: desired-value
```

Acknowledge the omission warning, then **Preview apply**. The server performs a
strict dry-run. Review both the field-value Diff and the before/after field sets.
An apply that changes no values can still change ownership. Confirm the exact
resource name before submitting. Changing the draft invalidates approval and
cancels pending preview work. Closing detail cancels the preview and invalidates
its queued completion; this does not roll back submitted writes.

## Ownership and conflicts

The stable manager `aster-apply` identifies this workflow across installations,
not a person, tenant or permission. It differs from ordinary `aster-desktop`
updates. Kubernetes RBAC and admission remain authoritative.

Omitting a field that this manager previously owned can remove it or restore a
server default unless another manager also owns it. The UI requires acknowledgement
and previews the resulting object. Identity-only apply is intentionally rejected;
complete ownership relinquishment is outside this feature's scope. Full intent
reconstruction from managedFields, force takeover, and multi-object transactions
are not implemented. Always retain every field this workflow should keep managing.

A competing owner produces the API's field-conflict error. Aster never silently
sets force=true and offers no force toggle. Resolve intent with the owning
workflow and generate a fresh preview. **Owners** shows actual FieldsV1 paths,
including Kubernetes list-key notation; it does not invent JSON pointers or
infer ownership from labels. Display is capped at 64 manager entries, 256 paths,
32 nested levels and 24 KiB, with explicit truncation. No resource payload is
read just to render ownership already present in detail.

## Concurrency and transport

Only an existing, non-terminating, versioned object is supported. At preview time
Aster pins the exact UID and resourceVersion into the immutable apply payload.
Execution first rechecks the baseline and retains both preconditions in PATCH,
including the read-to-write race. An expired/reused/cross-session/cross-service
plan cannot execute. New resource creation uses the separate POST/create-only
workflow, never an unguarded upsert. The real-cluster tests exercise deletion and
same-name replacement immediately between the final GET and PATCH.

The actual content type is `application/apply-patch+yaml`; JSON is sent as a YAML
subset. FieldValidation=Strict and Force=false are present at dry-run and commit.
The backend uses MaxRetries(0) and rejects redirects. Lost responses are Unknown,
with a consumed plan; re-read state, do not automatically replay. Real-cluster
fault injection verifies an apply really committed before its response is lost.
Dry-run does not reserve state, freeze external admission services, or guarantee
that a later commit will succeed or produce identical non-deterministic admission
results. UID/resourceVersion protect against stale stored-object mutations, not
malicious/intermediary replay or a non-conforming aggregated API implementation.

Secret apply is disabled like Secret editing. Generic custom-resource fields can
contain sensitive data and are not universally redacted. No managedFields metadata
is edited directly. Ordinary Edit remains an explicitly reviewed JSON update and
can alter managed fields differently; this workflow is not a security boundary
against users holding direct cluster credentials.

## Verification and upstream semantics

Unit tests verify exact bytes/options, identity/version preconditions, rejection
paths, session/issuer/expiry/single-use, display bounds, confirmation invalidation
and cancellation. Three-version kind E2E verifies actual ownership, omission,
conflicts, stale concurrency, deletion/replacement, response loss, read-only RBAC
and the native interaction workflow. OS tests cover controls at the minimum window;
window smoke is not physical IME, accessibility, DPI or driver qualification.

Kubernetes primary references:
- https://kubernetes.io/docs/reference/using-api/server-side-apply/
- https://kubernetes.io/docs/reference/using-api/api-concepts/#updates-to-existing-resources

The final head, tested checkout and evidence are recorded in the PR verification
comment. A completed scoped feature is not an enterprise production Release.
