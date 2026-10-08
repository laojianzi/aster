# Testing and evidence

## Layers are not interchangeable

| Layer | Runner / command | What it establishes |
| --- | --- | --- |
| Unit and concurrency | `go vet ./...`; `go test -race -shuffle=on -count=1 ./...` | Pure models, cancellation, bounded queues, defensive protocol behavior |
| Headless native interaction | `ui.NewTester`, included in Go tests | Real MyGo widget tree, focus, typed text and commands without an OS window |
| Real-cluster API + native interaction | `bash scripts/e2e.sh` | Actual Kubernetes API server/controllers plus workbench actions; no fake client |
| Per-OS native interaction | `go test -json ./internal/ui/...` on all three OS runners | Native widget tests on each OS; not physical keyboard/IME certification |
| Per-OS real-window smoke | `scripts/window_smoke.py` | Built executable starts, renders an actual window and closes with the expected marker |
| Release qualification | Outstanding | Signed installer, upgrade/recovery, real IME, accessibility, mixed-DPI, soak and enterprise security |

`go test -tags=e2e ... -run '^$'` is compilation only. Never report it as passing E2E.
The window-smoke fixture does not connect to Kubernetes; native real-cluster tests are separate.

## Disposable-cluster contract

Every test fixture must call `testcluster.Config(t)` before accessing a cluster. It requires:

- `ASTER_E2E_ALLOW_DESTRUCTIVE=1`;
- a single absolute `KUBECONFIG` path, never a fallback to `~/.kube/config`;
- `ASTER_E2E_CONTEXT=kind-aster-e2e` or `kind-aster-e2e-<suffix>`, matching current context;
- no external exec or legacy authentication provider in the selected test identity.

This is an accidental-use guard, not cryptographic proof that a remote endpoint is disposable.
The local script creates and owns the cluster. CI creates the named cluster on an isolated runner.
Fixtures use unique namespaces. Test RBAC intentionally has both allowed and denied cases.
Never reuse a production kubeconfig simply by renaming its context.

## Workload rollout regressions

The health interpreter checks controller observation before ready counts, distinguishes
Deployment progress deadlines and pause, StatefulSet rolling partition/start ordinal and
minReadySeconds, OnDelete manual intervention, Pod readiness versus Running, and Job completion.
Unknown custom-resource kinds do not borrow built-in readiness semantics.

The observer pins the resource UID and desired generation, uses bounded polling with read and
overall deadlines, respects retry delay, and stops on authorization errors, deletion, replacement
or a newer generation. Cancellation does not undo a Kubernetes write.

Real-cluster tests create a restricted, non-root HTTP Deployment initially scaled to zero.
They prove dry-run does not persist, execute a reviewed scale, wait for actual controller
observation and availability, and verify the native UI's readiness result. Separate scenarios
replace the resource or advance generation and require tracking to stop rather than report success.

## Command regressions

The real-cluster command tests check separate stdout/stderr, exit code 7, literal shell-like
arguments, output overflow, explicit cancellation and the configured command deadline. A read-only
identity is denied exec; an independent authorized request verifies that the forbidden command
did not create its marker file. A native UI scenario runs a real command, observes its exit,
disconnects a second command and clears the selected resource. Unit protocol tests forbid replay
when the WebSocket handshake returns an unsupported or missing negotiated protocol.

These tests do not claim PTY/interactive terminal support or that disconnection kills the remote
process. Every required E2E scenario must pass without skip on each configured cluster image.

## Evidence rules

CI records `go version` or `kubectl version`, the actual checked-out commit, test JSONL,
`summary.json`, and screenshots. Pull-request CI normally tests GitHub's synthetic merge commit,
not the feature-branch SHA. Record both; do not silently equate them.

`scripts/test_evidence.py` rejects empty, malformed, failing, skipped, unfinished or truncated
package evidence and missing required named tests. A later retry cannot erase an earlier
failure/skip in the same evidence file. The shell uses `pipefail`; evidence validation never
replaces the original test exit code. Validator regressions run with Python's standard unittest.

GitHub Actions artifacts have finite retention. Download reviewed evidence before it expires.
Do not publish kubeconfig credentials, Secret bodies, or customer logs as artifacts. Current
fixtures contain synthetic data only. Development binaries are unsigned and must not be
redistributed as a qualified production release.

## Local Linux smoke

After a build, with the relevant display libraries installed:

```sh
xvfb-run -a -s '-screen 0 1600x1000x24' \
  python3 scripts/window_smoke.py dist/aster artifacts/window.log
```

An offline development-kit workflow can package the pinned toolchain and vendored dependencies
for restricted environments. Verify its SHA-256 manifest before use. It is a development aid,
not a substitute for source review or trusted release provenance.
