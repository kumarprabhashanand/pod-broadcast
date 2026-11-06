# Changelog

## Unreleased

## 0.1.1

- `stop` and `wait` say so when the Broadcast API isn't installed.
- A failed Broadcast's Event has the type `Warning`, and its no-port message names `--port`.

## 0.1.0

First public release.

- `kubectl broadcast`: one HTTP request to every ready pod of a Service, Deployment, StatefulSet, DaemonSet, ReplicaSet or label selector, through the API server's pod proxy with your own credentials. Table, JSON and YAML output (schema v1), exit codes for scripts, `--dry-run`, retries, and Spring Boot Actuator presets.
- An optional controller that runs `Broadcast` objects (API `broadcast.prabhash.net/v1alpha1`): from CI through the Kubernetes API, `Continuous` mode that also reaches pods started later and reverts at the end, and per-namespace `BroadcastPolicy` rules.
- Plugin binaries for Linux, macOS and Windows on amd64 and arm64; a controller image for linux/amd64 and linux/arm64, with an SBOM; build attestations for both.
