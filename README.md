# pod-broadcast

`kubectl broadcast` sends one HTTP request to every ready pod of a Kubernetes Service, workload or label selector, and shows each pod's result.

```console
$ kubectl broadcast svc/orders spring evict-caches
POD                    STATE      CODE  TIME  ATTEMPTS  ERROR
orders-9bf8f4bb-cncrj  Succeeded  204   16ms  1
orders-9bf8f4bb-ptg98  Succeeded  204   16ms  1
2 targeted: 2 succeeded
```

Requests go through the Kubernetes API server's pod proxy with your own credentials, so nothing needs to be installed in the cluster. An optional [controller](docs/controller.md) runs broadcasts as Kubernetes objects, which adds:

- broadcasts from CI or any program, through the Kubernetes API;
- changes that also reach pods started later, and are reverted at the end;
- per-namespace policies on what may be called.

**Status:** in development, not released yet. Tested on Kubernetes 1.35, 1.36 and 1.37.

## Install from source

Requires Go 1.26 or newer.

```sh
go build -o bin/kubectl-broadcast ./cmd/kubectl-broadcast
```

Put `bin/kubectl-broadcast` on your `PATH`; kubectl then runs it as `kubectl broadcast`.

## Quick start

```sh
# Clear the caches on every ready pod of a Spring Boot app
kubectl broadcast svc/orders spring evict-caches

# Any HTTP request: a port name or number, a method and a path
kubectl broadcast deploy/orders --port 8081 -X POST /admin/reload

# Raise a log level on every pod, then put it back
kubectl broadcast deploy/orders spring set-log-level com.shop.orders DEBUG
kubectl broadcast deploy/orders spring reset-log-level com.shop.orders

# Read health from every pod, as JSON
kubectl broadcast -l app=orders spring health -o json

# Show the pods and the exact request without sending anything
kubectl broadcast svc/orders spring refresh --dry-run

# With the controller: DEBUG logging for 30 minutes, also on pods that start meanwhile
kubectl broadcast deploy/orders spring set-log-level com.shop.orders DEBUG --for 30m --name debug
kubectl broadcast stop debug   # end it early; the level is reset on every pod
```

Put kubectl flags such as `--context` and `-n` after `broadcast`.

The exit code is 0 when no pod failed, 1 when some failed, 2 for a usage error or when no pods matched, and 3 when pods failed and none succeeded, so scripts and CI can react.

## Documentation

- [CLI reference](docs/cli.md): targets, flags, port selection, presets, output and exit codes
- [Controller](docs/controller.md): install, Broadcast and BroadcastPolicy objects, Continuous mode
- [Permissions](docs/rbac.md): the RBAC rules a direct-mode user needs, with an example Role
- [Troubleshooting](docs/troubleshooting.md): what each error means and how to fix it
- [Testing](docs/testing.md): what is tested, how, where it has run, and the results

## Development

```sh
make test       # unit tests
make lint       # golangci-lint
make generate   # deepcopy code and CRDs after changing api/
make e2e        # end-to-end tests on a kind cluster; needs Docker and kind
```

## License

Apache License 2.0, see [LICENSE](LICENSE).
