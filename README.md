# pod-broadcast

[![CI](https://github.com/kumarprabhashanand/pod-broadcast/actions/workflows/ci.yml/badge.svg)](https://github.com/kumarprabhashanand/pod-broadcast/actions/workflows/ci.yml)
[![E2E](https://github.com/kumarprabhashanand/pod-broadcast/actions/workflows/e2e.yml/badge.svg)](https://github.com/kumarprabhashanand/pod-broadcast/actions/workflows/e2e.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

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

**Status:** pre-1.0; the API may still change. Tested on Kubernetes 1.35, 1.36 and 1.37. See the [changelog](CHANGELOG.md).

## Install

Download the archive for your platform from the [latest release](https://github.com/kumarprabhashanand/pod-broadcast/releases/latest), unpack it, and put `kubectl-broadcast` on your `PATH`; kubectl then runs it as `kubectl broadcast`.

To check a download, compare it with `checksums.txt`, and verify that it was built by this repository's release workflow with the [GitHub CLI](https://cli.github.com/):

```sh
sha256sum --check --ignore-missing checksums.txt
gh attestation verify kubectl-broadcast_v0.1.0_linux_amd64.tar.gz --repo kumarprabhashanand/pod-broadcast
```

On macOS, a downloaded binary is quarantined until you allow it: run `xattr -d com.apple.quarantine kubectl-broadcast`, or allow it in System Settings, Privacy & Security.

### With Go

```sh
go install github.com/kumarprabhashanand/pod-broadcast/cmd/kubectl-broadcast@latest
```

This needs Go 1.26 or newer; Go downloads the 1.27 toolchain the module asks for by itself.

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
- [Security](docs/security.md): what the plugin and the controller can access, and how to limit it
- [Testing](docs/testing.md): what is tested, how, where it has run, and the results

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development and pull requests, [SECURITY.md](SECURITY.md) to report a vulnerability, and the [code of conduct](CODE_OF_CONDUCT.md) for how we work together.

## License

Apache License 2.0, see [LICENSE](LICENSE).
