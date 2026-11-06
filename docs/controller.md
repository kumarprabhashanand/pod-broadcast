# Controller

The controller runs Broadcast objects inside the cluster. Use it to:

- trigger a broadcast from CI or another program through the Kubernetes API, and fail the pipeline when pods fail;
- keep a change applied to pods that start later, and revert it at the end (`Continuous` mode, `kubectl broadcast --for`);
- limit what broadcasts may call, with a BroadcastPolicy per namespace.

Every Broadcast is a Kubernetes object, so creating one is subject to RBAC and to API server auditing, and it shows in `kubectl get broadcasts`.

## Install

Install from a [release](https://github.com/kumarprabhashanand/pod-broadcast/releases). Its manifests run the image `ghcr.io/kumarprabhashanand/pod-broadcast-controller` for linux/amd64 and linux/arm64, pinned by digest.

```sh
VERSION=v0.1.0
URL=https://github.com/kumarprabhashanand/pod-broadcast/releases/download/$VERSION
```

**CRDs and user roles**, once per cluster, by a cluster admin. `user-roles.yaml` adds Broadcast permissions to Kubernetes' default roles: `view` reads Broadcasts and BroadcastPolicies, `edit` also runs and stops Broadcasts, and `admin` also writes BroadcastPolicies.

```sh
kubectl apply -f $URL/broadcast.prabhash.net_broadcasts.yaml -f $URL/broadcast.prabhash.net_broadcastpolicies.yaml
kubectl apply -f $URL/user-roles.yaml
```

**Then the controller, for all namespaces**, in `pod-broadcast-system`. Broadcasts in a namespace without a BroadcastPolicy are rejected.

```sh
kubectl apply -f $URL/controller.yaml
```

**Or for one namespace**, with permissions in that namespace only. Without a BroadcastPolicy, broadcasts are allowed, since the namespace's RBAC already decides who may create them. This is the simplest way to try the controller.

```sh
kubectl apply -n shop -f $URL/controller-namespaced.yaml
```

**From a clone of this repository**, the same files are in `deploy/`, and they run `pod-broadcast-controller:dev`. Build it with `docker build -t pod-broadcast-controller:dev .` and load it into your cluster, for example with `kind load docker-image pod-broadcast-controller:dev`.

The release image is signed with a build attestation: `gh attestation verify oci://ghcr.io/kumarprabhashanand/pod-broadcast-controller:$VERSION --repo kumarprabhashanand/pod-broadcast`.

**Memory** grows with the cluster: the controller keeps the Pods, EndpointSlices, Services and workloads it can see in memory. In a large cluster, prefer the single-namespace install, or add a memory limit to the manifest after watching its usage.

**Upgrade** by applying the same files again.

## Uninstall

Delete every Broadcast first, so that `Continuous` ones revert. `kubectl delete` waits until they are gone.

```sh
kubectl delete broadcasts --all -A
kubectl delete -f $URL/controller.yaml
kubectl delete -f $URL/user-roles.yaml -f $URL/broadcast.prabhash.net_broadcasts.yaml -f $URL/broadcast.prabhash.net_broadcastpolicies.yaml
```

For the single-namespace install, replace the second command with these, as the controller creates its leader election Lease at runtime:

```sh
kubectl delete -n shop -f $URL/controller-namespaced.yaml
kubectl delete lease -n shop pod-broadcast-controller
```

Events of past broadcasts remain until the API server expires them.

## Broadcast

```yaml
apiVersion: broadcast.prabhash.net/v1alpha1
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

The spec can't be changed after creation. The controller calls at most 1,000 pods per Broadcast, and keeps the first 256 bytes of each response in the status. `kubectl get bc` is short for `kubectl get broadcasts`, and `kubectl get bcp` for BroadcastPolicies.

| Phase | Meaning |
|---|---|
| `Rejected` | A policy doesn't allow the broadcast; nothing was called |
| `Running` | `Once`: calling the pods |
| `Succeeded`, `PartiallyFailed`, `Failed` | `Once`: finished |
| `Active` | `Continuous`: applied to the ready pods, and applying to each pod that becomes ready |
| `Reverting`, `Completed` | `Continuous`: sending the revert, and finished |

The `Complete` condition is `True` once the Broadcast has finished, with the phase as its reason, so `kubectl wait --for=condition=Complete broadcast/NAME` waits for any result. To also fail on failed pods, use `kubectl broadcast wait NAME`, which returns the [exit codes](cli.md#exit-codes).

### Continuous mode

A `Continuous` broadcast applies its request to every ready pod, then to each pod that becomes ready, until its `duration` ends. The duration defaults to the policy's `defaultDuration`, else 1h, and can't exceed the policy's `maxDuration`, else the controller's `--max-duration`, 24h.

At the end, the `revert` request goes to every pod the request reached that still exists. `kubectl broadcast stop NAME` ends it early and keeps the object; deleting it also reverts. A policy change that no longer allows the broadcast ends it too, and so does deleting its target.

A pod runs with the old setting from the moment it becomes ready until the controller reaches it.

A Continuous broadcast keeps track of at most 1,000 pods over its life. When a workload replaces its pods, the entries of pods that no longer exist are removed and counted as gone (`status.droppedPods`); if more than 1,000 pods that still exist were reached, the broadcast ends and reverts.

Requests may reach a pod more than once: after a controller restart, pods without a recorded result are called again. Operations should be safe to repeat.

## BroadcastPolicy

```yaml
apiVersion: broadcast.prabhash.net/v1alpha1
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
- `paths` are RE2 patterns that must match the whole path and query, and also the path without its query, so that a wildcard can't reach into the query: `/actuator/.*/refresh` doesn't allow `/actuator/shutdown?x=/refresh`. Allow an optional query with `(\?.*)?`. So that no server reads a path as another one than the pattern matched, broadcasts are rejected when their path has `.` or `..` segments, `;`, `#`, a backslash, or percent-encoded `.`, `/`, backslash or `%`: `/actuator/caches/../shutdown` would match `/actuator/caches(/.*)?`, and many servers resolve it to `/actuator/shutdown`.
- Secrets used in `headersFromSecrets` must be listed in `secrets` and carry the label `broadcast.prabhash.net/use=true`.
- All policies in the namespace count; for `limits`, the strictest value wins.
- Policies apply to Broadcasts only. Direct mode is limited by RBAC on `pods/proxy`; see [Security](security.md#controller).

## Permissions

People and pipelines that create broadcasts need `create`, `get`, `list` and `watch` on `broadcasts`, and `patch` for `kubectl broadcast stop`; with `user-roles.yaml` installed, the `edit` role has them. They don't need access to the target's pods or to the Secrets a policy lists, so policies should be written by namespace admins, which the `admin` role allows.

The controller's own permissions are in the manifests. It reads Secrets one at a time by name, and never lists or watches them.

## Networking

The controller calls pod IPs directly, never other hosts, and doesn't follow redirects. It ignores `HTTP_PROXY` and similar variables, so pod calls never go through a proxy. If NetworkPolicies restrict traffic into your pods, allow the controller's pods (label `app: pod-broadcast-controller`, in the controller's namespace) on the target ports.

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
| `--zap-log-level` | `info` | Log level: `debug`, `info`, `error`, or a number for more detail. `--zap-encoder`, `--zap-devel` and the other `--zap-` flags set the log format. |
