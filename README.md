# Aster

构建与测试入口：`python scripts/dev.py build` / `python scripts/dev.py test`。
原生终端依赖需先校验并准备，运行时不下载；详见 [Terminal](docs/terminal.md) 和
[分支接续台账](docs/BRANCHES.zh-CN.md)。

A native Kubernetes workbench built with Go and [MyGo Native UI](https://github.com/egoist/mygo).
No browser UI or embedded web dashboard is used.

**Development preview, not a production release.** The repository contains working desktop
workflows and executable regression suites. It does not yet implement the enterprise control
plane or satisfy all release qualifications. See [project status](docs/PROGRESS.zh-CN.md),
[testing](docs/testing.md), and [security boundaries](docs/security.md).

## Implemented workflows

- Explicit kubeconfig context selection, namespace and label scope, trust review for risky
  configuration, API discovery and generic custom-resource browsing.
- Bounded resource synchronization, watch-expiry reconciliation, virtual native resource lists,
  filtering and sorting, and identity-aware selection.
- Native YAML view/edit, bounded field diff, Events, container log follow/tail and loopback-only
  Pod port forwarding. Secret values are redacted and editing Secrets is disabled.
- Server dry-run and typed confirmation for create-only, edit, scale, restart and delete.
  Reviewed plans are session-bound, immutable, expiring and single-use. Conditional writes
  protect the reviewed UID/resourceVersion; ambiguous outcomes are not retried automatically.
- Native non-interactive Pod commands with explicit argv, typed confirmation, separate stdout/stderr,
  bounded output, remote exit status and cancellation. No automatic retry or transport fallback.
  See [command behavior and security limits](docs/commands.md); this is not a full terminal.
- Interactive native Pod TTY with explicit argv/container/name confirmation, ordered input/output,
  resize, protocol exit status, bounded input/scrollback and cancellation. Remote clipboard writes,
  links and runtime library downloads are blocked. See [terminal contract](docs/terminal.md).
- Native workload health and read-only rollout tracking for built-in workload types.
  API acceptance is separate from readiness. Tracking pins the original UID and generation,
  stops for replacement/newer changes, supports cancellation and has an overall deadline.

Independent multi-cluster workspace windows are implemented; shared tabs and persisted workspace definitions are not.
The editor is a bounded text editor and field-diff reviewer, not a complete Kubernetes IDE.

## Development

The checked-in module and CI pin **Go 1.27.1**, **MyGo v0.2.15**, and Kubernetes Go libraries
**v0.37.0**. Install the toolchain in `go.mod`, Python 3 and Git, then:

```sh
python scripts/dev.py build
./dist/aster # Windows: .\dist\aster.exe
```

Use a disposable development cluster first. Connection discovery parses the usual `KUBECONFIG`
loading chain without running authentication. The Connect action uses the selected context;
risky configuration requires an explicit trust decision. A trust prompt is not an OS sandbox.

```sh
python scripts/dev.py test
# After preparing source, static analysis may also run directly:
go vet ./...
```

A real desktop session is needed to open the app. CI also exercises the actual window under
Xvfb on Linux and on macOS/Windows runners. Headless `ui.NewTester` tests are separately labelled.
Unsigned CI binaries are development artifacts, not signed installable releases.

## Real-cluster tests

Install Docker (or a supported kind provider), kind v0.33.0, kubectl, Python 3, curl, Bash and the Go toolchain:

```sh
bash scripts/e2e.sh
```

The script creates its own named kind cluster and temporary kubeconfig, runs native interaction
and real API-server tests, validates nonempty test evidence, and deletes only its test cluster.
Do not point the destructive suite at an existing cluster. Running `go test -tags=e2e` without
an explicit opt-in and dedicated test context fails before fixture creation.

The CI matrix pins image digests for Kubernetes **1.35.8, 1.36.4 and 1.37.0**. It produces test
JSON, summary files, screenshots, environment/commit records and separate OS window-smoke logs.
A green matrix is evidence for these tests and versions, not full production certification.

## Repository layout

| Path | Responsibility |
| --- | --- |
| `internal/ui` | MyGo widgets, UI-owned state and asynchronous dispatch |
| `internal/kube` | Kubernetes discovery, queries, watches, logs, forwarding and commands |
| `internal/ttysession`, `internal/nativeterm` | Ordered interactive sessions and hardened native emulator |
| `internal/execsession` | Bounded command arguments, output and explicit outcome model |
| `internal/operation` | Immutable preview/execute boundary |
| `internal/health`, `internal/rollout` | Readiness semantics and identity-pinned observation |
| `internal/cluster`, `internal/eventqueue` | Cancellation and bounded state transport |
| `internal/manifest`, `internal/logbuffer` | Bounded/redacted presentation models |
| `internal/testcluster`, `e2e` | Disposable real-cluster fixtures and executable scenarios |
| `scripts`, `.github/workflows` | Evidence validation, local reproduction and CI |

Start with [architecture invariants](docs/architecture.md). Changes should include regression
coverage at the appropriate boundary; mock coverage cannot replace real-cluster or OS testing.

## Native workspaces and metrics

**New workspace** opens an independent native window (up to four including
closing sessions). Loading contexts never connects automatically: choose your
scope and press **Connect** in each window. Closing one does not stop others.
See `docs/workspaces.md` for lifecycle, isolation and test boundaries.

Existing Pods and Nodes expose a **Metrics** panel with CPU cores, memory working
set and bounded local trends. Missing, partial, stale or unauthorized data is
explicitly labeled; Aster does not install a metrics service in your cluster.
See `docs/metrics.md`. The disposable `scripts/e2e.sh` test environment does install
the checksum-verified test add-on before running the complete E2E suite.

### External authentication

Trusted kubeconfig exec authentication runs once per explicit Connect with a 30-second invocation
limit and a maximum 15-minute connection lifetime (or earlier returned expiry). It has bounded
stdout/stderr and an explicit environment policy. Disconnect cancels authentication; expiration
clears private workbench state and streams. Reconnect explicitly; no silent renewal or legacy
auth-provider fallback. See [authentication contract](docs/authentication.md) before using a cloud CLI.
