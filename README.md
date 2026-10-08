# Aster

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
- Native workload health and read-only rollout tracking for built-in workload types.
  API acceptance is separate from readiness. Tracking pins the original UID and generation,
  stops for replacement/newer changes, supports cancellation and has an overall deadline.

Context switching is implemented; simultaneous multi-cluster workspaces are not yet implemented.
The editor is a bounded text editor and field-diff reviewer, not a complete Kubernetes IDE.

## Development

The checked-in module and CI pin **Go 1.27.1**, **MyGo v0.2.15**, and Kubernetes Go libraries
**v0.37.0**. Install the toolchain in `go.mod`, then:

```sh
go mod download
go run ./cmd/aster
```

Use a disposable development cluster first. Connection discovery parses the usual `KUBECONFIG`
loading chain without running authentication. The Connect action uses the selected context;
risky configuration requires an explicit trust decision. A trust prompt is not an OS sandbox.

```sh
go vet ./...
go test -race -shuffle=on -count=1 ./...
go build -trimpath -o dist/aster ./cmd/aster
```

A real desktop session is needed to open the app. CI also exercises the actual window under
Xvfb on Linux and on macOS/Windows runners. Headless `ui.NewTester` tests are separately labelled.
Unsigned CI binaries are development artifacts, not signed installable releases.

## Real-cluster tests

Install Docker (or a supported kind provider), kind v0.33.0, kubectl, Python 3 and the Go toolchain:

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
| `internal/kube` | Kubernetes discovery, queries, watches, logs and forwarding |
| `internal/operation` | Immutable preview/execute boundary |
| `internal/health`, `internal/rollout` | Readiness semantics and identity-pinned observation |
| `internal/cluster`, `internal/eventqueue` | Cancellation and bounded state transport |
| `internal/manifest`, `internal/logbuffer` | Bounded/redacted presentation models |
| `internal/testcluster`, `e2e` | Disposable real-cluster fixtures and executable scenarios |
| `scripts`, `.github/workflows` | Evidence validation, local reproduction and CI |

Start with [architecture invariants](docs/architecture.md). Changes should include regression
coverage at the appropriate boundary; mock coverage cannot replace real-cluster or OS testing.
