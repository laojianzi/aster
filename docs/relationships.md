# Read-only resource relationships

Open a resource and choose **Related**. Verified links open in the detail pane;
the main list, Namespace selector and active cluster remain unchanged. Refresh detail
uses the detail resource kind, not the list kind. Every navigation performs a new GET
and checks group/version/kind, namespace, name and UID. A same-name replacement is rejected.
Closing the detail or changing its context cancels the lookup and invalidates queued callbacks.

## Supported relationships

- Owner references: resolve an explicitly known GVK, preserve scope, then verify the owner UID.
  The discovery catalog adds custom kinds with exact resource plurals; unknown versions are
  shown as Unsupported rather than deriving a guessed URL. Cluster-scoped objects cannot
  resolve namespaced owners. Secret/ConfigMap owners are shown as declared references only.
- Direct dependents: Deployment → ReplicaSet; ReplicaSet, StatefulSet, DaemonSet,
  ReplicationController or Job → Pod; CronJob → Job. Match the full owner reference,
  not a workload label. These are one-hop queries, not a recursively materialized graph.
- Service → EndpointSlice uses the service-name label. Service → Pod uses a non-empty,
  valid selector. A selectorless Service never triggers an all-Pod query. Label membership
  does not mean ownership, endpoint readiness, routing policy or working traffic.
- Pod configuration, service-account, node, PVC, environment and image-pull references are
  names only. Opening Related does not GET Secret or ConfigMap targets or their contents.
  These unverified references are not clickable; explicit resource browsing remains separate.

## Bounds and incomplete results

Each lookup allows at most 12 logical API lookups, 1,000 collection objects, 200 displayed
links, 200 objects per page and a 15-second deadline. No implicit cluster-wide dependent
scan is performed. The limit counts logical read calls: client-go may retry a read at the
transport layer within the context deadline. It is not a wire-request or process-RSS bound.
Response object byte limits and metadata-only collection optimization remain separate
hardening work; these budgets limit application processing and retained link projection.

Pagination retains continuation and collection resourceVersion. An exhausted budget,
inconsistent version, repeated token, malformed/out-of-scope object, denied resource type
or missing owner produces an explicit warning and PARTIAL result rather than a false empty
success. Different resource types are read separately: there is no atomic multi-resource
snapshot. Refresh to reconcile changes; navigation always rechecks identity.

## Verification

Unit tests cover stale UIDs, cross-scope owners, custom plurals, non-ownership label matches,
permission denial, pagination bounds, selectorless Services and no secret/config fetches.
Native interaction tests cover correct detail routing, revalidation, queued-result isolation,
cancellation and minimum-window controls. Real-cluster tests exercise controller-created
ReplicaSets/Pods, a label-matching unrelated ReplicaSet, Service/EndpointSlice association,
restricted RBAC and the Native Deployment → ReplicaSet → Pod → loopback port-forward flow.
Final results must be read from the exact PR/CI commit, not inferred from test source files.
