# Nonnullable integer set-list assistance

Issue #61 extends the existing string set-list checker, after completed integer
map-list #59/#60. Use one branch, `feat/native-schema-integer-set`, and reuse the
existing schema requests, source navigation, native history and CRD lifecycle.

## Accepted identity

Only explicit `x-kubernetes-list-type: set` arrays with nonnullable
`type: integer` items and decoded `int64` values gain this identity check.
Empty arrays are supported. Signed 64-bit endpoints and adjacent values above
2^53 remain exact integers; no numeric normalization or float conversion occurs.
The existing string contract and messages are retained.

Complete metadata and every item are preflighted before a duplicate is reported.
One fixed `list-set` hint at the original parent-array location says
"List entries repeat the advertised integer value." Reports advertise the
string/int64 scope but do not expose values, defaults or examples. Checks do not
change source, snapshots, selection, mounted Undo/Redo or cluster objects.

## Incomplete and server boundaries

Integral or fractional float64, json.Number, host/unsigned numbers, number,
boolean or compound item schemas, nullable items and malformed/uninspected
metadata are outside this increment. A late invalid item or exhausted budget
suppresses an earlier apparent duplicate. Independent type hints may remain.

The existing 256-item limit, shared 20,000 work units, 8 MiB operand budget and
context cancellation remain in force. Each integer inspection reserves eight
bytes; lookup/insertion/equality reserves 24 bytes, even after an early match.
String limits and accounting are unchanged.

Kubernetes scalar set validation compares runtime values, not mathematical enum
equality: see [the pinned implementation](https://github.com/kubernetes/kubernetes/blob/v1.37.0/staging/src/k8s.io/apiextensions-apiserver/pkg/apiserver/schema/listtype/validation.go).
JSON transport can turn an integral float into an integer token. Defaults, null
pruning, legacy-invalid-object update exemptions, SSA merging, CEL and admission
remain server boundaries. Current-identity strict server dry-run and explicit
reviewed mutation are still mandatory.

## Qualification

Acceptance requires an independent sorted-integer oracle, strict decoding and
budget/privacy regressions, native navigation/correction/history and minimum
window cases, and actual published-CRD/create/live dry-run checks on all three
existing Kubernetes versions. Old required tests must remain mandatory; new
native tests must run in all six OS/build-mode migration combinations.

Final PR and fresh main evidence, actual guarded retirement/recovery and Issue
completion are recorded separately in #61 and its sole PR after they occur.
This scope document is not a claim that those steps have passed. It does not
include an editor-engine migration, physical IME or cross-unmount history,
Helm/GitOps lifecycle, production release or deferred P3 enterprise authority.
