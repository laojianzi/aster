# Native schema assistance

The field-help milestone (#14), source navigation (#29), and bounded constraint
assistance (#42) share production tracker #13. They reuse the same explicit,
current-identity requests and read-only draft checks. This is not a complete
language server or an admission validator.

## Workflow

Connect, open an existing resource (or a new-resource draft), then choose **Edit**.
The field pointer uses JSON Pointer syntax, for example `/spec/containers/0/image`;
empty means the resource root. `~1` and `~0` escape a slash and tilde in a field name.
**Field help** displays its type, description and immediate child fields.
**Check draft** checks the current single-document manifest for missing required
fields, primitive type mismatches, fields not described by the schema, and the
supported value/container constraints below.
**Clear schema** cancels a pending request and clears its result.

Both actions are explicit and read-only. They do not alter the editor, adopt
schema defaults, create an operation plan, copy examples, perform mutations or
bypass Preview/dry-run. Edit and Apply remain separate: checks do not claim to
validate SSA intent/ownership or field omission behavior. Secret editing stays
disabled. Diagnostics identify field pointers and the constraint category, never
draft values, enum alternatives, defaults or schema examples.

## Bounded constraint assistance (#42)

These checks apply to the decoded value at the field pointer. A result is
advisory; it does not replace the API server's dry-run or admission decision.

| Constraint | Local check |
| --- | --- |
| Scalar `enum` | Compare string, boolean, null and number alternatives. Numbers compare by numeric value; strings and booleans retain their own types. Object/array alternatives are outside this contract. |
| `minimum` / `maximum` | Compare decoded JSON numbers, including exact integers and `json.Number`, without routing them through `float64`. OpenAPI 3.0 boolean `exclusiveMinimum` / `exclusiveMaximum` choose strict bounds. |
| `minLength` / `maxLength` | Count Unicode code points in the decoded string, including astral characters and combining marks. These are not byte, UTF-16-unit or grapheme counts. |
| `minItems` / `maxItems` | Count array entries; this does not validate uniqueness or Kubernetes list/merge semantics. |
| `minProperties` / `maxProperties` | Count object properties; this does not infer server defaults or mutation/pruning behavior. |

Existing draft values already represented as `float64` are compared using their
JSON serialization. The constraint checker cannot recover original YAML number
spelling or decimal precision lost by the existing manifest decoder. For example,
an exact decoded integer and a rounded floating-point draft value remain distinct
inputs to this contract. Source locations still use the original draft; they do
not substitute a different numeric parser or change the object sent to Preview.

Constraints share the 20,000-unit traversal/work budget and cancellation checks.
An enum has at most 128 alternatives; a numeric representation has at most 512
bytes, with a decimal exponent in the signed 32-bit range. Numeric comparison
uses normalized coefficients and magnitude without expanding powers of ten.
Charged schema/input strings and numeric operands share an 8 MiB constraint-byte
budget. These are inspection limits, not a hard cap on total process memory.
Unsupported or malformed constraints, complex enum alternatives and exhausted
budgets mark inspection **incomplete** through the existing report banner; they
do not produce a guessed success or a fabricated field-specific violation.
Supported checks may still produce advisory hints on an incomplete report.
Nullable values and canonical Kubernetes integer-or-string unions retain their
explicit type rules; arbitrary compositions are not flattened into guessed
constraints.

Checking leaves the native buffer, draft revision, selection, Undo/Redo and IME
composition untouched. It does not install editor completions or use synthetic
input. The existing connection/detail/draft/pointer epochs and source digest
reject a delayed result or navigation request after its identity changes.

## Transport and resource limits

Every action obtains fresh OpenAPI v3 discovery and a group-version document
from the current connection's transport and shared rate limiter. There is no
cross-window/identity schema cache. Only a constructed `/openapi/v3` and the
exact selected `api/<version>` or `apis/<group>/<version>` document are requested.
An advertised `serverRelativeURL` can contain only the matching path and a
hexadecimal `hash` query; it is never itself used as a request destination.
Absolute/foreign URLs, path traversal/escaping, extra queries and redirects are
rejected. Expired immutable URLs require an explicit retry; no automatic replay.
Errors do not echo response bodies or credentialed URLs. Authentication expiry
uses the existing connection lease; transport copies cannot bypass it.

Budgets: 20-second UI request deadline, 1 MiB decoded index, 16 MiB decoded schema,
64 JSON nesting levels, 500,000 JSON value nodes, 8,192 definitions, 128 keyword
keys per schema node, 20,000 shared traversal/required-entry/constraint work units,
100 diagnostics, 128 child-field entries, 1,024-byte pointer,
2,048-rune description and 64 KiB rendered help/report. JSON duplicate keys,
trailing documents and unsupported document versions are rejected. Only local
`#/components/schemas/...` references are resolved (maximum 32 consecutive refs);
cycles and unsupported branches remain visible as incomplete inspection. Kubernetes
single-ref `allOf` wrappers with annotation-only siblings are resolved without
changing the referenced definition. Structural siblings are not flattened. The
single-resource editor's existing 256 KiB / 5,000-line limit still applies.

Cancellation/epoch checks bind results to connection, detail, draft and pointer
revisions. Editing, changing pointer, leaving Edit, closing detail, disconnecting
or expiring credentials cancels/invalidates prior results. Work happens off the
UI thread; only the finished bounded text is dispatched to the UI.

## Deliberate limitations

This checks structural and bounded constraint hints, not full OpenAPI/JSON
Schema, CEL, webhook or admission behavior. `pattern`, `format`, `multipleOf`,
complex enum equality, unique/list semantics and CEL remain unsupported.
Compositions (`allOf`/`oneOf`/`anyOf`/`not`, except canonical integer-or-string
structure and annotation-only single-ref wrappers), unresolved references and
exhausted budgets remain explicitly incomplete. A report with zero hints does
not mean the manifest is valid. `x-kubernetes-preserve-unknown-fields` and typed
maps retain their conservative handling. An unknown-field hint is advisory:
the API server decides rejection/pruning/preservation. Defaults are never
inserted or adopted. Field explanations are plain text, not executable markup
or clickable links.

The existing incremental TextBuffer, Find and source navigation are reused.
Completion insertion still needs a supported native editor transaction that
preserves history, selection, composition and document identity. MyGo v0.4.5
external `TextBuffer.Replace` resets native history on synchronization; this
milestone does not bypass that boundary. Full language-server assistance,
long-lived schema caches and the remaining constraints stay in tracker #13.
Current actions already fetch fresh schemas; no new refresh button or cache is
introduced by #42.

## Verification

Unit tests cover parser/work/output limits, malicious URLs/references, permission
errors, gzip expansion, exact number classification and cancellation. Native
component tests drive buttons/editor changes, delayed callbacks, credential-expiry
teardown and minimum-window layout. Three real Kubernetes versions exercise
built-in schemas, an independently authorized discovery-only identity, structural
CRD publication and native editor checks without persisting the changed draft.
These classes are distinct from three-OS real-window smoke and physical desktop
qualification. Exact commits, counts and artifacts are recorded on the PR.

The #42 constraint contract adds exact-number/enum, Unicode/container-length,
malformed/unsupported applicability, shared-budget/cancellation/privacy and
input-immutability regressions. The mandatory native cases are
`TestNativeSchemaConstraintsPreserveNumbersDraftAndHistory` and
`TestNativeSchemaConstraintsMinimumWindowAndNoValueDisclosure`, in both checked
and production migration builds on all three OSes. The real-cluster cases
`TestNativeSchemaConstraintsAgainstRealCluster` and
`TestRealSchemaConstraintsAgreeWithDryRun` compare representative CRD discovery
and server dry-run behavior in each supported Kubernetes version. These tests
do not establish full admission equivalence or physical IME qualification.

This document defines the change and its required evidence, not a future CI
result. Final PR/main commits, raw results and scope completion belong to #42,
its single PR and the live #13 checkpoint.

Primary references:
- https://kubernetes.io/docs/concepts/overview/kubernetes-api/#openapi-v3
- https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/
- https://spec.openapis.org/oas/v3.0.3.html

Compatibility reference: `kubernetes/kube-openapi/pkg/builder3/util/util.go` (`WrapRefs`)
wraps native references with sibling annotations in single-element `allOf`. The
regression retains the original failed real-cluster Pod path; it is not skipped.


## Source positions and native navigation (#29)

Field help and structural draft diagnostics now resolve against the **original
single-document draft**, not its re-encoded Kubernetes object. Select a result
in **Schema location**, then press **Go to location**. The editor focuses and
reveals its caret at the explicit key or sequence-item start. This never edits
the draft or creates an operation plan. Existing Undo is retained. Navigation
is consumed once, not repeated on repaint. Active text composition blocks it
until a fresh explicit click after composition finishes.

Coordinates are one-based Unicode code-point lines/columns; selection uses
zero-based rune offsets, not bytes, UTF-16 units, graphemes or display-cell widths.
Both LF and CRLF and inline JSON are tested. A missing required field points to
an explicitly labeled enclosing value; it does not invent a nonexistent key.
Aliases/merges and YAML 1.1-coerced keys are not expanded into guessed locations.
Unsupported BOM/line-break forms, duplicates, multiple documents or exhausted
budgets disable navigation but do not bypass or replace server dry-run.

The immutable index stores a digest, field paths and coordinates, not scalar
values, a draft copy or the parsed AST. It is invalidated with draft/pointer,
detail, connection, tab, expiry and window lifecycle. A deferred jump rechecks
all epochs plus the exact draft digest; queued changes are applied before a jump.
No build-scoped MyGo Context or Element is retained outside the current build.

Additional budgets: existing 256 KiB/5,000-newline input bound; 20,000 charged
AST nodes/entries, depth 64, pointer 1,024 bytes, 2 MiB cumulative index path/entry
accounting and 100 navigation results. Combined report/help and source notes stay
within 64 KiB, with an explicit truncation marker. The YAML AST parser is synchronous
within the input budget; cancellation is checked around parsing and throughout
traversal, not advertised as a preemptible parser or a hard process-memory cap.

This is bounded source assistance, not a new incremental text engine, an LSP,
completion insertion, full schema/admission validation or physical IME acceptance.
The original Kubernetes manifest decoder still decides local object semantics.
The pinned `go.yaml.in/yaml/v3` AST reader is only used for presentation locations.
