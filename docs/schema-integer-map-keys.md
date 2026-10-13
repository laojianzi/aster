# Required integer-key map-list assistance

This addendum extends the string-only map-list identity scope in
[schema-assistance.md](schema-assistance.md). The existing string behavior and
all other assistance limits are retained. Issue #59 / PR #60 is the sole scope;
MyGo #58, string map-lists #54/#55 and string set-lists #56/#57 are already merged.

## Accepted identity

An explicitly advertised `x-kubernetes-list-type: map` array may now use
`type: integer` keys as well as `type: string` keys. Every key must be explicitly
required and nonnullable in the object item schema. The complete metadata and
all item identities are inspected before any duplicate hint is emitted.

Integer values must be decoded `int64`, the representation preserved for signed
integer tokens by the application's strict unstructured manifest decoder. All
signed 64-bit values, including endpoints and adjacent integers above 2^53, stay
integers throughout projection and comparison. Mixed tuples preserve component
positions; non-key payloads do not contribute to identity. No numeric/string
normalization, default inference or float conversion is performed.

One generic `list-map-keys` hint appears at the original parent-array location:
"List entries repeat the advertised key identity." Existing all-string tuples
retain their original string-key message. Hints do not expose key values,
non-key payloads, defaults or examples. Inspection is read-only and reuses the
existing source navigation, immutable snapshots and mounted native Undo/Redo.

## Deliberately incomplete cases

`type: number`, `float64` (even mathematically integral), `json.Number`, host
`int`/unsigned values, nullable or missing keys, optional/default-only keys,
malformed metadata and unresolved or semantically uninspected references remain
incomplete. Independent type/required diagnostics may still be shown. A late
invalid item or budget failure prevents an earlier apparent duplicate from
being reported as a completed identity inspection.

Limits remain eight keys, 256 items, 256 bytes per key name and 16 KiB per string
component, within the existing shared document work/operand budgets and context
cancellation. Integer components reserve eight bytes for inspection and their
full tuple lookup/insertion/comparison cost. Per-feature limits do not promise
that every combination also fits the shared document budget.

## Server boundary

Kubernetes validates a single map key with runtime scalar identity; multiple
keys are projected into objects and serialized before comparison. This checker
therefore does not borrow the exact mathematical enum comparator for numeric
list identities. See the pinned [Kubernetes list validation source](https://github.com/kubernetes/kubernetes/blob/v1.37.0/staging/src/k8s.io/apiextensions-apiserver/pkg/apiserver/schema/listtype/validation.go).

For example, an in-memory float64 8080 is incomplete locally even though JSON
transport may encode it as the integer token 8080 and the server may then reject
a duplicate. Defaults, pruning of nulls, older invalid-object update exemptions,
SSA merge behavior, CEL, webhooks and other admission policies are still server
boundaries. Current-identity server dry-run and explicit reviewed mutation remain
mandatory; this hint is not a full admission or SSA-equivalence certificate.

## Verification and lifecycle

Core tests include an independent 726-case integer/tuple comparison corpus,
strict manifest decoding, signed endpoints, runtime and metadata boundaries,
shared budgets, references and input/privacy preservation. Public native tests
exercise source navigation, correction, exact decoded values, original selection
and multi-step mounted Undo/Redo, including a minimum window and late invalidity.

The existing isolated CRD fixture is reused for integer single/mixed keys and
legacy invalid objects. Actual published-schema and create/live dry-run cases
run in the existing Kubernetes 1.35.8/1.36.4/1.37.0 matrix, with absence checks for
creates and unchanged live UID/resourceVersion/spec. The original string fixture
and all original assertions remain. New required test names supplement every
existing CI requirement in `scripts/test_evidence.py`; the two native cases also
run in all six OS/mode migration combinations. No old requirement or timeout is
removed. The retirement registration is limited to PR #60 and its exact branch.

This document specifies the scope and tests, not a claim that pending CI has
passed. Final PR and fresh main qualification, actual branch retirement/recovery
and Issue completion are recorded in #59/#60 after they occur. No editor-engine
migration, arbitrary giant-input guarantee, physical IME qualification, history
across unmount/remount, complete font/rendering qualification or signed release
is implied. Enterprise authority remains deferred P3 outside the first MVP.
