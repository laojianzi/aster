# Aster architecture

Aster is a native Kubernetes workbench.

## Invariants

1. UI packages do not call client-go directly.
2. Every asynchronous operation is bound to an immutable cluster session.
3. Production mutations are planned, previewed and revalidated before execution.
4. Resource state queues are bounded. Logs and terminal byte streams use separate ordered backpressure paths.
5. Credentials are never persisted in ordinary application state.
6. Tests distinguish fake/unit, real API-server integration, kind E2E and real desktop-OS E2E.

## Initial package boundaries

- internal/app: application lifecycle
- internal/cluster: cluster session lifecycle
- internal/eventqueue: bounded coalescing resource state transport
- internal/kube: Kubernetes clients and subscriptions
- internal/ui: MyGo native presentation
- internal/operation: mutation planning and execution
- internal/terminal: exec/session adapters
- internal/security: credential and policy boundaries
