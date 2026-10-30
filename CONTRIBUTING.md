# Contributing

Issues and pull requests are welcome.

## Before you start

- **Bug:** open an issue with the command, its output, and the versions of the plugin and Kubernetes.
- **Larger change:** open an issue first, so the approach is agreed before the work.
- **Security issue:** follow [SECURITY.md](SECURITY.md) instead of opening an issue.
- **Conduct:** everyone taking part follows the [code of conduct](CODE_OF_CONDUCT.md).

## Development

You need the Go version from `go.mod`. The end-to-end tests also need Docker and kind.

```sh
make build      # bin/kubectl-broadcast
make test       # unit tests
make lint       # golangci-lint
make generate   # deepcopy code and CRDs, after changing api/
make e2e        # end-to-end tests on a kind cluster
```

[Testing](docs/testing.md) explains what each layer covers and the options of the end-to-end suite.

## Pull requests

- Keep one topic per pull request, with tests for changed behavior and docs for user-visible changes. [docs/cli.md](docs/cli.md) must list every flag and preset; a test checks it.
- `make test` and `make lint` pass, and `make generate` leaves no changes.
- Write commit messages as short imperative sentences, such as "Add the --cache flag".
- Contributions are accepted under the [Apache License 2.0](LICENSE), as its section 5 describes.
