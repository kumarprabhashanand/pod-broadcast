# CLI reference

```
kubectl broadcast TARGET [flags] -X METHOD PATH
kubectl broadcast TARGET [flags] GROUP PRESET [ARGS]
kubectl broadcast -l SELECTOR [flags] (-X METHOD PATH | GROUP PRESET [ARGS])
kubectl broadcast presets
kubectl broadcast version
```

Put every flag, including kubectl's own such as `--context`, after `broadcast`.

## Targets

| Form | Pods called |
|---|---|
| `svc/NAME`, `service/NAME` | The pods behind the Service, taken from its EndpointSlices |
| `deploy/NAME`, `deployment/NAME` | The pods matching the Deployment's selector |
| `sts/NAME`, `statefulset/NAME` | The pods matching the StatefulSet's selector |
| `ds/NAME`, `daemonset/NAME` | The pods matching the DaemonSet's selector |
| `rs/NAME`, `replicaset/NAME` | The pods matching the ReplicaSet's selector |
| `-l`, `--selector` SELECTOR | The pods matching a label selector, such as `app=orders` |

All targets are in one namespace: the one from `-n`, or your kubeconfig context's default.

**Which pods are called.** A pod is called when its `Ready` condition is true, it has an IP, it isn't being deleted and it hasn't finished. Readiness always comes from the pod itself, also for Services that publish not-ready addresses. `--include-not-ready` also calls pods that aren't ready. Each pod is called once, even when a dual-stack Service lists it twice.

## Requests

| Flag | Default | Meaning |
|---|---|---|
| `-X`, `--request` | | HTTP method: `GET`, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE` or `OPTIONS`. Needs a PATH argument, which may include a query string. |
| `-H`, `--header` | | Header as `'Name: value'`; repeatable. A later header replaces an earlier one with the same name. |
| `--header-from-env` | | Header as `Name=VAR`, with the value of environment variable `VAR`; repeatable. The value is never shown. |
| `--headers-from-secret` | | A Secret whose keys become headers; repeatable. Values are never shown. Needs `get` on `secrets`. |
| `-d`, `--data` | | Request body, `@FILE` to read a file, or `@-` to read standard input |

Values of headers from the environment or a Secret are shown as `<redacted>`, as are headers named `Authorization`, `Cookie` or containing `token`, `secret`, `password` or `api-key`.

The `Authorization` header is refused, because the API server removes it before passing the request to the pod.

## Port and scheme

The port is chosen for each pod; the first rule that applies wins:

1. `--port NAME|NUMBER`
2. The pod annotation `broadcast.kumarprabhashanand.github.io/port`, a port name or number
3. For presets, the container port named `management`
4. For Service targets, the Service's only port, translated to its target port

A port name is looked up among the pod's TCP container ports, including sidecar containers. If no rule applies to any pod, the command stops and lists the options. A pod whose named port is missing, or defined twice, fails on its own while the others go ahead.

| Flag | Default | Meaning |
|---|---|---|
| `--port` | see above | Container port name or number |
| `--scheme` | the pod annotation `broadcast.kumarprabhashanand.github.io/scheme`, then `http` | `http` or `https`. Certificates of pods aren't verified by the API server's pod proxy. |

Annotations go in the pod template, for example:

```yaml
template:
  metadata:
    annotations:
      broadcast.kumarprabhashanand.github.io/port: management
```

## Execution

| Flag | Default | Meaning |
|---|---|---|
| `--concurrency` | `10` | Pods called at the same time |
| `--timeout` | `5s` | Timeout for each attempt |
| `--retries` | `2` | Retries after a connection error, a timeout or status 429, 500, 502, 503 or 504; at most 10 |
| `--backoff` | `1s` | Base delay between retries; it doubles after each attempt, with random jitter |
| `--success-codes` | `2xx` | Status codes that count as success: classes such as `2xx` or codes such as `404`, comma-separated |
| `--include-not-ready` | `false` | Also call pods that aren't ready |
| `--dry-run` | `false` | Print the pods and the exact request, and send nothing |

Requests may reach a pod more than once (retries, or running the command again), so operations should be safe to repeat.

## Presets

Presets are built-in requests. `kubectl broadcast presets` lists them with the request each one sends, and `--dry-run` shows it for your pods.

| Preset | Request |
|---|---|
| `spring evict-caches [--cache NAME]` | `DELETE {base}/caches`, or `{base}/caches/NAME` |
| `spring set-log-level LOGGER LEVEL` | `POST {base}/loggers/LOGGER` with `{"configuredLevel":"LEVEL"}` |
| `spring reset-log-level LOGGER` | `POST {base}/loggers/LOGGER` with `{}`, which clears the configured level |
| `spring refresh` | `POST {base}/refresh` |
| `spring health` | `GET {base}/health` |
| `spring info` | `GET {base}/info` |

`LEVEL` is one of `TRACE`, `DEBUG`, `INFO`, `WARN`, `ERROR`, `FATAL` or `OFF`. Presets with a body send `Content-Type: application/json`; `-H` can replace it.

| Flag | Default | Meaning |
|---|---|---|
| `--cache` | | Cache to evict, for `spring evict-caches` |
| `--base-path` | the pod annotation `broadcast.kumarprabhashanand.github.io/actuator-base-path`, then `/actuator` | Actuator base path `{base}`. The annotation is read per pod; the flag applies to all pods. |

The endpoints must be exposed by the application, for example with `management.endpoints.web.exposure.include=health,info,caches,loggers,refresh`. `refresh` needs Spring Cloud.

## Output

| Flag | Default | Meaning |
|---|---|---|
| `-o`, `--output` | `table` | `table`, `json` or `yaml` |

The table has one row per pod: its state (`Succeeded`, `Failed` or `Gone`, for a pod deleted during the call), status code, time across all attempts, attempts and error. A `BODY` column with the start of each response appears when any pod returned a body.

JSON and YAML follow [schema v1](../schemas/output-v1.json):

```json
{
  "schemaVersion": "v1",
  "mode": "direct",
  "dryRun": false,
  "target": { "namespace": "shop", "kind": "Service", "name": "orders" },
  "request": { "method": "DELETE", "path": "/actuator/caches" },
  "outcome": "PartiallyFailed",
  "summary": { "targeted": 2, "succeeded": 1, "failed": 1, "gone": 0 },
  "pods": [
    { "name": "orders-7d9f-abc", "uid": "6f1c…", "ip": "10.244.0.12", "port": 8081, "scheme": "http",
      "state": "Succeeded", "code": 204, "durationMs": 41, "attempts": 1, "error": "", "body": "" }
  ]
}
```

`body` holds up to 1 MiB of the response. A pod entry has a `path` field when its request path differs from `request.path`, because of its actuator base path annotation. With `--dry-run`, `outcome` is `DryRun` and pods that would be called have the state `Pending`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Every pod succeeded (pods deleted during the call don't count as failures), or a dry run |
| 1 | Some pods failed |
| 2 | Usage error, no pods matched, missing permission, or a request the pod proxy can't pass |
| 3 | Every pod failed |

## Kubernetes flags

The standard kubectl flags work: `-n`, `--context`, `--kubeconfig`, `--cluster`, `--user`, `--as`, `--as-group`, `--as-uid`, `--token`, `--server`, `--request-timeout` and the TLS and certificate flags. `kubectl broadcast --help` lists them all.
