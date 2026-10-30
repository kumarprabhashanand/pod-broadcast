# Controller

The controller runs Broadcast objects inside the cluster. Use it to:

- trigger a broadcast from CI or another program through the Kubernetes API, and fail the pipeline when pods fail;
- keep a change applied to pods that start later, and revert it at the end (`Continuous` mode, `kubectl broadcast --for`);
- limit what broadcasts may call, with a BroadcastPolicy per namespace.

Every Broadcast is a Kubernetes object, so creating one is subject to RBAC and to API server auditing, and it shows in `kubectl get broadcasts`.

## Install

Each [release](https://github.com/kumarprabhashanand/pod-broadcast/releases) has the manifests with the matching image, `ghcr.io/kumarprabhashanand/pod-broadcast-controller:VERSION`, already set. The commands below use the files in this repository, which reference `pod-broadcast-controller:dev`: build that image and load it into your cluster (`kind load docker-image pod-broadcast-controller:dev` for kind), or apply the release's `controller.yaml` and `controller-namespaced.yaml` instead, together with the two `broadcast.*.yaml` CRD files.

```sh
docker build -t pod-broadcast-controller:dev .
```

**For all namespaces**, in `pod-broadcast-system`. Broadcasts in a namespace without a BroadcastPolicy are rejected.

```sh
kubectl apply -f deploy/crds/
kubectl apply -f deploy/controller.yaml
```

**For one namespace**, with permissions in that namespace only. Without a BroadcastPolicy, broadcasts are allowed, since the namespace's RBAC already decides who may create them. The CRDs are installed once per cluster, by a cluster admin.

```sh
kubectl apply -f deploy/crds/
kubectl apply -n shop -f deploy/controller-namespaced.yaml
```

**Memory** grows with the cluster: the controller keeps the Pods, EndpointSlices, Services and workloads it can see in memory. In a large cluster, prefer the single-namespace install, or add a memory limit to the manifest after watching its usage.

**Upgrade** by applying the same files again. **Uninstall** in this order: delete every Broadcast and wait until they are gone, so that `Continuous` ones revert; delete the controller manifest; delete the CRDs.

## Broadcast

```yaml
apiVersion: broadcast.kumarprabhashanand.github.io/v1alpha1
kind: Broadcast
metadata:
  generateName: clear-orders-cache-
  namespace: shop
spec:
  target: {service: orders}
  request: {port: management, method: DELETE, path: /actuator/caches}
```

```sh
NAME=$(kubectl create -f clear-cache.yaml -o name | cut -d/ -f2)
kubectl broadcast wait "$NAME" -n shop
```

Use `kubectl create`, not `kubectl apply`: each run is a new object. `kubectl explain broadcast.spec` describes every field.

| Field | Meaning |
|---|---|
| `target` | One of `service`, `workloadRef` (`kind` and `name` of a Deployment, StatefulSet, DaemonSet or ReplicaSet) or `selector`; plus `includeNotReady` |
| `request` | `method`, `path`, `port`, `defaultPort`, `scheme`, `headers`, `headersFromSecrets` and `body`; ports and schemes are chosen as in the [CLI](cli.md#port-and-scheme) |
| `mode` | `Once` (default) or `Continuous` |
| `duration`, `revert` | `Continuous` only: how long it runs, and the request that undoes it |
| `execution` | `concurrency`, `timeout`, `retries`, `backoff` and `successCodes`, with the CLI's defaults |
| `ttlSecondsAfterFinished` | How long a finished Broadcast is kept; `0` deletes it at once. Defaults to the controller's `--default-ttl`, 1h. |

The spec can't be changed after creation. The controller calls at most 1,000 pods per Broadcast, and keeps the first 256 bytes of each response in the status.

| Phase | Meaning |
|---|---|
| `Rejected` | A policy doesn't allow the broadcast; nothing was called |
| `Running` | `Once`: calling the pods |
| `Succeeded`, `PartiallyFailed`, `Failed` | `Once`: finished |
| `Active` | `Continuous`: applied to the ready pods, and applying to each pod that becomes ready |
| `Reverting`, `Completed` | `Continuous`: sending the revert, and finished |

### Continuous mode

A `Continuous` broadcast applies its request to every ready pod, then to each pod that becomes ready, until its `duration` ends. The duration defaults to the policy's `defaultDuration`, else 1h, and can't exceed the policy's `maxDuration`, else the controller's `--max-duration`, 24h.

At the end, the `revert` request goes to every pod the request reached that still exists. `kubectl broadcast stop NAME` ends it early and keeps the object; deleting it also reverts. A policy change that no longer allows the broadcast ends it too, and so does deleting its target.

A pod runs with the old setting from the moment it becomes ready until the controller reaches it.

Requests may reach a pod more than once: after a controller restart, pods without a recorded result are called again. Operations should be safe to repeat.

## BroadcastPolicy

```yaml
apiVersion: broadcast.kumarprabhashanand.github.io/v1alpha1
kind: BroadcastPolicy
metadata: {name: actuator, namespace: shop}
spec:
  allow:
    - targets: ["svc/orders", "deploy/orders"]
      ports: [management]
      methods: [GET, POST, DELETE]
      paths: ["/actuator/(caches|loggers|health|info|refresh)(/.*)?"]
  secrets: [orders-actuator-auth]
  limits: {maxConcurrency: 20, maxRetries: 3, defaultDuration: 30m, maxDuration: 4h}
```

- A broadcast passes when its request, and its revert, each match one rule on target, port, method and path together.
- `targets` are written like CLI targets; `"*"` matches any target, and is the only way to allow label selectors.
- `ports` are compared by number on each pod, so a number can't be used to get around a rule written with a name.
- `paths` are RE2 patterns that must match the whole path and query.
- Secrets used in `headersFromSecrets` must be listed in `secrets` and carry the label `broadcast.kumarprabhashanand.github.io/use=true`.
- All policies in the namespace count; for `limits`, the strictest value wins.

## Permissions

People and pipelines that create broadcasts need `create`, `get`, `list` and `watch` on `broadcasts`, and `patch` for `kubectl broadcast stop`. They don't need access to the target's pods or to the Secrets a policy lists, so policies should be written by namespace admins.

The controller's own permissions are in the manifests. It reads Secrets one at a time by name, and never lists or watches them.

## Networking

The controller calls pod IPs directly, never other hosts, and doesn't follow redirects. If NetworkPolicies restrict traffic into your pods, allow the controller's pods (label `app: pod-broadcast-controller`, in the controller's namespace) on the target ports.

## Events and metrics

The controller records an Event on the Broadcast at each phase change; `kubectl describe broadcast NAME` shows them. Prometheus metrics are served on port 8080 at `/metrics`:

| Metric | Meaning |
|---|---|
| `pod_broadcast_broadcasts_finished_total{phase}` | Broadcasts that finished, by phase |
| `pod_broadcast_pod_calls_total{state}` | Calls to pods, by the pod's resulting state |

## Controller flags

| Flag | Default | Meaning |
|---|---|---|
| `--namespace` | all namespaces | Run broadcasts in this namespace only |
| `--require-policy` | `true` | Reject broadcasts in namespaces without a BroadcastPolicy |
| `--default-ttl` | `1h` | How long finished broadcasts are kept |
| `--max-duration` | `24h` | Longest `Continuous` broadcast when no policy sets `maxDuration` |
| `--leader-elect` | `true` | Leader election, so extra replicas wait as standbys |
| `--metrics-bind-address` | `:8080` | Prometheus metrics |
| `--health-probe-bind-address` | `:8081` | `/healthz` and `/readyz` |
| `--version` | | Print the version and exit; the version is also logged at startup |
