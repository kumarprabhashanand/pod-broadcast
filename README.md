# pod-broadcast

`kubectl broadcast` sends one HTTP request to every ready pod of a Kubernetes Service, workload or label selector, and reports each pod's result.

**Status:** in development, not released yet.

## Build

Requires Go 1.26 or newer.

```sh
go build -o bin/kubectl-broadcast ./cmd/kubectl-broadcast
```

Put `bin/kubectl-broadcast` on your `PATH`; kubectl then runs it as `kubectl broadcast`.

## Test

```sh
go test ./...
```

## License

Apache License 2.0, see [LICENSE](LICENSE).
