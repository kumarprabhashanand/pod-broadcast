# Troubleshooting

`--dry-run` is the quickest check: it shows the pods that would be called, the port, scheme and path for each, and the exact request, without sending anything.

## The command stops (exit code 2)

**`no ready pods matched service/orders in namespace "shop"; 2 not ready`**
The target exists but none of its pods is ready. Check `kubectl get pods`; to call pods that aren't ready, add `--include-not-ready`. Without the not-ready count, nothing matched: check the target name, the label selector and the namespace (`-n`).

**`service "orders" not found in namespace "shop"`**
Check the name and the namespace. Targets are always in one namespace.

**`can't choose a port: use --port NAME|NUMBER, or annotate the pods with broadcast.prabhash.net/port`**
Nothing tells the plugin which port to call. Pass `--port`, or set the annotation in the pod template once. For a Service with several ports, the message lists their target ports.

**`missing permission: POST requests need the "create" verb on pods/proxy in namespace "shop"`**
You may read the pods but not call them with this method; no pod was called. See [Permissions](rbac.md). Errors such as `services "orders" is forbidden` mean you can't read the target itself.

**`method OPTIONS can't be sent in direct mode, which sends only GET, HEAD, POST, PUT, PATCH, DELETE`**
Other methods either don't pass the API server's pod proxy, or need permission for every verb on it. The controller sends any method (`--mode controller`).

**`the Authorization header can't reach pods`**
The API server removes the `Authorization` header from proxied requests, so the pod would never see it. If the application accepts another header, such as an API key header, use that one, ideally with `--header-from-env` or `--headers-from-secret`.

**`flags cannot be placed before plugin name`**
This comes from kubectl. Write `kubectl broadcast --context dev ...`, not `kubectl --context dev broadcast ...`.

## A pod fails (exit code 1 or 3)

The `ERROR` column, or `error` in JSON, says why:

| Error | Meaning and fix |
|---|---|
| `HTTP 404` | The pod answered, but not at this path. For presets, check the actuator base path (`--base-path` or the pod annotation) and that the endpoint is exposed. |
| `HTTP 401`, `HTTP 403` | The application refused the request. Pass the credentials it expects in a header. |
| `HTTP 500` and other codes | The application's own answer; the `BODY` column, or `body` in JSON, often says more. Use `--success-codes` if this code is expected. |
| `dial tcp 10.244.0.9:8081: connect: connection refused` | Nothing listens on that port. Check `--port` and the container's ports. |
| `timed out after 5s` | The pod didn't answer within `--timeout`. Raise it for slow operations. |
| `port not found: no TCP container port named "management"` | This pod has no container port with that name; maybe it runs an older version during a rollout. |
| `ambiguous port: 2 container ports are named "management"` | Two containers in the pod define the same port name. Use the port number instead. |
| `pod is gone` | The pod was deleted during the call. It isn't counted as a failure. |

Failed calls are retried after connection errors, timeouts and status 429, 500, 502, 503 and 504 (`--retries`, `--backoff`). Requests can reach a pod more than once, so operations should be safe to repeat.

## Controller mode

**`the Broadcast API isn't installed in this cluster`**
The CRDs and the controller aren't installed; see [Controller](controller.md#install).

**`warning: broadcast NAME has no status yet; is the pod-broadcast controller running?`**
Check `kubectl get pods -n pod-broadcast-system`, or the namespace of a single-namespace install, and the controller's logs.

**`broadcast NAME rejected: ...`**
A BroadcastPolicy doesn't allow it, and nothing was called. The message names what didn't match: no policy in the namespace, the method and path, the revert, a limit, or a Secret that isn't listed or lacks the label `broadcast.prabhash.net/use=true`.

**`port 8080 isn't allowed by a BroadcastPolicy`** (in a pod's row)
The policy names the port differently on this pod. Use the port the policy allows, by name or by the number it stands for on the pod.
