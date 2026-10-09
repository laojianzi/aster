# Independent native workspaces

Use **New workspace** to open another native window. Aster permits up to four
live/closing workspace resources. Each starts disconnected with freshly owned
state: cluster connection and effective credentials, trust decision, resource
subscription, detail/editor, prepared operations, command, log stream, port
forward, readiness observation and Metrics sampler.

Loading kubeconfig now only lists contexts. It does not connect, run exec
credential programs or contact an API server. Select the context and Namespace,
then explicitly press **Connect**. Trust confirmations remain per window and
per configuration fingerprint; opening another window does not copy them.

Each window displays its workspace number and active cluster/Namespace. Window
placement uses a distinct per-slot state key. This does not persist credentials,
resource contents or draft text, and is not workspace-session restoration.

## Lifecycle and budget

Closing a window cancels that window's workers; it does not quit the application
while another window remains open. The manager joins work off the UI thread and
retains the slot until joining finishes, so rapidly opening/closing windows does
not create an unbounded queue of retained clients. When all windows have closed,
the application quits and joins every accepted workspace resource. Application
shutdown cancels all workspaces before joining any of them.

The manager and widget state have UI-thread ownership. Background resource
cleanup never mutates widgets. Late window callbacks cannot close a reused
slot; queued results for a closed Workbench are ignored. This is in-process
state/lifecycle isolation, **not a sandbox or an enterprise tenant boundary**.

An outstanding Kubernetes request may already have reached the API server when
a window is closed. Cancellation is not rollback, and disconnecting a Pod command
is not proof the remote process terminated. No connection or mutation is replayed
automatically when creating/reopening a workspace.

Budgets currently apply per window (up to four), not as one global budget for
all windows connected to the same physical cluster. Shared global request/cache
budgets, tabs within one window, persisted workspace definitions, cross-window
resource dragging and centralized session governance remain future work.

## Verification

Unit/race tests cover per-window cancellation, capacity including cleanup,
slot reuse, early closure, factory failure, shutdown, separate credentials and
same-name/UID data projections. Native controls verify explicit Connect,
new-window defaults and minimum-window accessibility bounds.

Every OS's actual-window smoke starts two real windows without reading kubeconfig,
requires both to paint, closes one, requires fresh frames from the survivor,
then closes the survivor and waits for clean application exit. This does not
simulate OS input, IME, accessibility technology or physical GPU drivers.

A separate E2E job creates two Kubernetes 1.37.0 kind control planes with distinct
verified loopback HTTPS configurations and distinct kube-system identities.
Both contain a ConfigMap with the same Namespace and name but different data.
Two native widget testers connect simultaneously; preview/commit occurs only
in the intended cluster, a plan from another issuer is rejected, and the second
workspace's real watch continues after the first workspace joins. This is native
component-to-real-cluster E2E, separate from OS window smoke, not the Cartesian
product of every OS and every Kubernetes version.
