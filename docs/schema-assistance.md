# Native schema assistance

Issue #14 is the bounded field-help milestone under production tracker #13.
It is not a complete language server or an admission validator.

## Workflow

Connect, open an existing resource (or a new-resource draft), then choose **Edit**.
The field pointer uses JSON Pointer syntax, for example `/spec/containers/0/image`;
empty means the resource root. `~1` and `~0` escape a slash and tilde in a field name.
**Field help** displays its type, description and immediate child fields.
**Check draft** checks the current single-document manifest for missing required
fields, primitive type mismatches and fields not described by the schema.
**Clear schema** cancels a pending request and clears its result.

Both actions are explicit and read-only. They do not alter the editor, adopt
schema defaults, create an operation plan, copy examples, perform mutations or
bypass Preview/dry-run. Edit and Apply remain separate: checks do not claim to
validate SSA intent/ownership or field omission behavior. Secret editing stays
disabled. Diagnostics include field pointers but never draft values.

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
64 JSON nesting levels, 500,000 JSON value nodes, 8,192 definitions, 20,000 draft
inspection nodes, 100 diagnostics, 128 child-field entries, 1,024-byte pointer,
2,048-rune description and 64 KiB rendered help/report. JSON duplicate keys,
trailing documents and unsupported document versions are rejected. Only local
`#/components/schemas/...` references are resolved (maximum 32 consecutive refs);
cycles and unsupported branches remain visible as incomplete inspection. The
single-resource editor's existing 256 KiB / 5,000-line limit still applies.

Cancellation/epoch checks bind results to connection, detail, draft and pointer
revisions. Editing, changing pointer, leaving Edit, closing detail, disconnecting
or expiring credentials cancels/invalidates prior results. Work happens off the
UI thread; only the finished bounded text is dispatched to the UI.

## Deliberate limitations

This checks structural hints, not full OpenAPI/JSON Schema, CEL, webhook or
admission behavior. Compositions (`allOf`/`oneOf`/`anyOf`/`not`, except the special
integer-or-string structure), unresolved references and exhausted budgets make
results explicitly incomplete. Enums, patterns, format, numeric ranges, list
semantics and defaults are not validated. A report with zero hints does not
mean the manifest is valid. `x-kubernetes-preserve-unknown-fields`, typed maps,
nullable fields and integer-or-string are handled conservatively. An unknown
field hint is advisory: the API server decides rejection/pruning/preservation.
Field explanations are plain text, not executable markup or clickable links.

Full completion insertion, incremental editing, diagnostic source locations,
long-lived schema caches and advanced constraints remain in tracker #13.

## Verification

Unit tests cover parser/work/output limits, malicious URLs/references, permission
errors, gzip expansion, exact number classification and cancellation. Native
component tests drive buttons/editor changes, delayed callbacks, credential-expiry
teardown and minimum-window layout. Three real Kubernetes versions exercise
built-in schemas, an independently authorized discovery-only identity, structural
CRD publication and native editor checks without persisting the changed draft.
These classes are distinct from three-OS real-window smoke and physical desktop
qualification. Exact commits, counts and artifacts are recorded on the PR.

Primary references:
- https://kubernetes.io/docs/concepts/overview/kubernetes-api/#openapi-v3
- https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/
- https://spec.openapis.org/oas/v3.0.3.html
