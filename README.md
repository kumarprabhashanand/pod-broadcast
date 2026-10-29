# pod-broadcast

`kubectl broadcast` sends one HTTP request to every ready pod of a Kubernetes Service, workload or label selector, and shows each pod's result.

```console
$ kubectl broadcast svc/orders spring evict-caches
POD                    STATE      CODE  TIME  ATTEMPTS  ERROR
orders-9bf8f4bb-cncrj  Succeeded  204   16ms  1
orders-9bf8f4bb-ptg98  Succeeded  204   16ms  1
2 targeted: 2 succeeded
```

Requests go through the Kubernetes API server's pod proxy with your own credentials, so nothing needs to be installed in the cluster.

**Status:** in development, not released yet.

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
```

Put kubectl flags such as `--context` and `-n` after `broadcast`.

The exit code is 0 when every pod succeeded, 1 when some failed, 2 for a usage error or when no pods matched, and 3 when every pod failed, so scripts and CI can react.

## Documentation

- [CLI reference](docs/cli.md): targets, flags, port selection, presets, output and exit codes
- [Permissions](docs/rbac.md): the RBAC rules a user needs, with an example Role
- [Troubleshooting](docs/troubleshooting.md): what each error means and how to fix it

## Development

```sh
make test   # unit tests
make lint   # golangci-lint
make e2e    # end-to-end tests on a kind cluster; needs Docker and kind
```

## License

Apache License 2.0, see [LICENSE](LICENSE).
