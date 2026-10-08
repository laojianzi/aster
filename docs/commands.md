# Pod commands

The **Command** tab is a native, non-interactive command runner. It is not a shell emulator
or a full terminal. It does not run a local shell, use a PTY, attach stdin, interpret ANSI,
or interpret pipelines or environment expansion implicitly.

## Use

Open a running Pod, select **Command**, choose its running container, and enter an explicit
JSON argument array. For example, `["id"]` or `["printenv", "HOSTNAME"]`. An explicit
`["/bin/sh", "-c", "..."]` executes shell syntax remotely when that executable is present.
Changing the arguments or selected container clears the confirmation. Type the exact Pod
name and select **Run command**. The confirmation is consumed by each execution.

Commands can change workloads, access secrets available to the container, or affect services.
Kubernetes exec has no dry-run equivalent. Pod-name confirmation is an accidental-use guard,
not approval enforcement. The effective Kubernetes identity must be authorized to read the
Pod and execute its subresource. Ordinary read-only Pod access does not grant command access.

## Bounds and outcomes

The desktop sets a two-minute command deadline and a **combined** 128 KiB stdout/stderr budget.
The backend contract permits at most five minutes, 2 MiB output, 64 arguments and 16 KiB argv.
A full output budget disconnects the session and reports the limit; it does not silently claim
that a partially captured command succeeded. Stdout and stderr remain separate. Their relative
ordering across streams is not reconstructed.

An explicit remote exit status produces either success (zero) or failure (nonzero). Cancel,
deadline, transport loss and output-limit disconnection have **no confirmed remote exit**.
**Stop command does not guarantee that the remote process was killed.** Closing detail,
changing scope/context or closing Aster cancels the local session; remote side effects remain.

The exec request uses WebSocket protocol v5. It is sent once, with **no automatic transport
fallback or replay**. An upstream upgrade error can occur after an HTTP 101 response has already
been processed. Retrying such an error as SPDY could run a non-idempotent command twice. A proxy
that blocks this protocol therefore requires explicit remediation, not a hidden retry.

## Identity and sensitive data

A fresh preflight rejects a different Pod UID, a terminating Pod or a stopped container.
Kubernetes routes exec by Pod name, so the UID check cannot be atomic with the upgrade and cannot
pin a container restart. Stronger governance requires managed access outside this desktop.

Argument and output data are not added to Aster's operation history. Closing detail clears
Aster's references to that data; this is not cryptographic memory erasure in Go. Arbitrary
command output may contain secrets and is **not universally redacted**. Control and Unicode
format characters are replaced for display instead of triggering terminal or clipboard effects.
API-server/proxy auditing may record exec arguments: do not put secret values in argv. Aster's
in-memory history is not an enterprise audit log or session recording.

## Validation

Unit regressions cover argument parsing, shared output bounds, late writers, exit-state
classification, 403 rejection without fallback, redirect rejection, and malformed upgrade
protocols without replay. Native tests cover confirmation reset, minimum-window layout,
shutdown and data clearing. Disposable-cluster tests cover real stdout/stderr/exit 7, literal
arguments, output overflow, cancellation, deadline and read-only RBAC denial. A native UI E2E
runs these controls against an actual Pod. The current commit's CI results remain authoritative.
