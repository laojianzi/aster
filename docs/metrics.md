# Native resource metrics

Select an existing Pod or Node and open **Metrics**. This panel reads the
Kubernetes resource Metrics API with the current cluster identity; it never
installs add-ons or connects to kubelet/Prometheus endpoints directly.

**Refresh metrics** reads once. **Start sampling** reads sequentially, waiting
15 seconds after each completed query and UI acknowledgement. Only one result
may be queued for the UI. **Pause metrics** freezes the captured snapshot.
Switching away from Metrics, closing a detail, changing scope or disconnecting
cancels the sampler and invalidates pending UI callbacks. The bounded history is
cleared on detail identity changes and never persisted.

## Meaning and integrity

CPU is average core usage over the server-provided window. Memory is the
reported working set in bytes, displayed in MiB. Neither is a percentage of
requests/limits. A Pod total is the sum of reported containers, not application
cost or workload-wide usage. Requests/limits, historical Prometheus queries and
workload aggregation are outside this feature.

The view distinguishes missing API, missing sample, insufficient permission,
unsupported API version, transient unavailability, malformed data, partial
container samples, stale samples and replaced objects. Missing values are never
filled with zero. Samples older than two minutes are labeled stale; a timestamp
more than 30 seconds ahead or before object creation is rejected. Windows must
be positive and at most ten minutes. Negative, missing, non-finite or excessive
quantities and unknown/duplicate container names are rejected.

The client supports explicitly advertised metrics.k8s.io/v1 and v1beta1, preferring
v1. It checks the live core/v1 resource name, Namespace, GVK and UID before and
after the named metrics GET. A metrics UID, when provided, must agree. Metrics
servers often omit UID: these checks and sample/creation timestamps reduce
misattribution but do not provide an atomic cross-API snapshot or distinguish
container restarts within the same Pod UID.

Charts retain at most 60 local observations, deduplicated by source timestamp;
the horizontal axis is observation time, not reconstructed server history.
Failed, partial or stale observations create gaps, not fabricated zero points.
A paused snapshot is not presented as a continuously updated health signal.

## Security and bounds

Core Pod/Node access does not grant metrics.k8s.io access. Independent API-server
RBAC applies. Requests reuse the authenticated transport, reject redirects, share
its request limiter, and have one ten-second query deadline. Metrics/discovery
response bodies are limited to 256 KiB. No remote response body, redirect location
or credential is exposed in errors. The live-resource identity GET uses the
existing core client; its complete-object byte limit is a separate hardening item.
The decoder retains at most 256 reported container entries.

## Verification

Unit/race tests exercise quantities, stale/partial/future/malformed samples,
identity rechecks, missing metrics, protocol version selection, response bounds,
redirect denial and cancellation. Native tests exercise actual controls,
minimum-window layout, queued-result isolation and sampler teardown.

CI installs the official metrics-server v0.9.0 manifest in a disposable kind
cluster after verifying its release SHA-256. The installation script requires
explicit destructive-test consent, the dedicated context and a loopback HTTPS
API server. The kind-only add-on uses `--kubelet-insecure-tls` because kind's
kubelet serving certificate is not trusted by that add-on; this flag is never
added to Aster or recommended for production. Real E2E requires actual Pod and
Node metrics, independently denies/grants the metrics API role, and exercises
native sampling and teardown. Missing prerequisites fail rather than skip.

Semantics: Kubernetes resource metrics pipeline documentation; metrics-server
v0.9.0 release manifest. Installer digest is recorded in `scripts/install-e2e-metrics.sh`.
