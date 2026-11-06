# Testing

Every change goes through unit tests, lint and an end-to-end suite that runs the real plugin and controller against real Kubernetes clusters.

## Layers

| Layer | What it checks | How to run |
|---|---|---|
| Unit tests | Target resolution and readiness, port and scheme rules, headers and redaction, retries, backoff, timeouts and concurrency, the pod proxy transport against a fake API server, table, JSON and YAML output against the [JSON Schema](../schemas/output-v1.json), every preset's exact request, BroadcastPolicy matching including paths that try to escape a pattern, the controller's phases, conditions, reverts and pod limits against a fake cluster, and the CLI's flags, errors and exit codes against a fake cluster. Tests also fail when a flag or preset is missing from the [CLI reference](cli.md), or when the two controller manifests grant different permissions. | `make test` |
| Static checks | golangci-lint; govulncheck; generated CRDs and deepcopy code are up to date; the plugin builds for Linux, macOS and Windows on amd64 and arm64, and the controller image for linux/amd64 and linux/arm64 | `make lint`, `make generate` |
| End-to-end | The plugin binary and the controller image against a real cluster, described below | `make e2e` |

## How the end-to-end suite checks results

The suite deploys two test apps:

- [`test/apps/echo`](../test/apps/echo) answers every request and records it: method, path and query, `Host`, headers and body. The record is read on a separate port, which isn't counted.
- [`test/apps/spring`](../test/apps/spring) is a Spring Boot 4.1 app with Actuator, a cache and Spring Cloud's refresh endpoint.

Tests run the plugin as a user would and check its output and exit code. They don't rely on the plugin's own report: they read what each echo pod received through the Kubernetes API and assert exact numbers, for example exactly one request per pod, three attempts on a pod that keeps failing, and none at all for a dry run or a missing permission. Spring tests check the effect inside the app on every pod, such as the cached value changing after an eviction. Controller tests also check the Broadcast's status, its Events and the controller's metrics.

## What the end-to-end suite covers

**Direct mode**

| Test | Checks |
|---|---|
| `TestEveryPodGetsExactlyOneRequest` | Every ready pod of a Service gets exactly one request; exit code 0 |
| `TestOneFailingPod` | A pod answering 500 is retried three times and reported `Failed`; the others succeed; exit code 1 |
| `TestEveryPodRefusesConnections` | Every pod refusing connections gives a connection error per pod; exit code 3 |
| `TestNotReadyPodsAreSkipped` | Not-ready pods are skipped, also when the Service publishes not-ready addresses; `--include-not-ready` calls them |
| `TestDryRunSendsNothing` | `--dry-run` lists the pods and the request, hides secret values, and sends nothing |
| `TestRBACVerbForEachMethod` | Each HTTP method needs exactly its documented RBAC verb on `pods/proxy`; without it the command names the verb, calls no pod and exits 2 |
| `TestExampleRoleIsEnough` | The [example Role](../examples/rbac/direct-user.yaml) allows every method and target kind |
| `TestJSONOutputMatchesTheSchema` | JSON output, including dry runs and selector targets, validates against schema v1 |
| `TestRequestReachesThePodUnchanged` | A PATCH arrives with its path, query, `Host`, body and headers from flags, the environment and a Secret, while the output hides the secret values |
| `TestHTTPSAndAnnotations` | HTTPS pods are reached using the port and scheme pod annotations |
| `TestLargeResponseIsCut` | A 3 MiB response succeeds and is cut to 1 MiB |
| `TestWorkloadTargets` | Deployment and ReplicaSet targets call only their own pods |
| `TestPodInSeveralSlicesIsCalledOnce` | A pod listed in IPv4 and IPv6 EndpointSlices is called once; on a dual-stack cluster, with a real dual-stack Service |
| `TestHundredPodsFinishQuickly` | 100 pods finish in under 10 seconds at the default concurrency |
| `TestRunsAsAKubectlPlugin` | kubectl finds and runs the plugin |

**Presets**

| Test | Checks |
|---|---|
| `TestSpringPresets` | On a real Spring Boot app: `health` and `info` answer, `evict-caches` (all caches and one cache) empties the cache on every pod, `set-log-level` and `reset-log-level` change and clear the level on every pod, `refresh` succeeds |

**Controller**

| Test | Checks |
|---|---|
| `TestControllerRunsOnce` | `--mode controller` calls every pod once, JSON validates, a `Succeeded` Event is recorded, metrics are served |
| `TestControllerExitCodesAndWait` | A partial failure exits 1, both from `--mode controller` and from `kubectl broadcast wait` on a Broadcast created from YAML |
| `TestControllerDeletesAfterTTL` | `ttlSecondsAfterFinished: 0` deletes the Broadcast after it ran |
| `TestControllerPolicy` | No policy, a path or a port the policy doesn't allow, and an unlabeled or unlisted Secret are all refused; a listed, labeled Secret's header reaches the pods |
| `TestContinuousReachesNewPodsAndStops` | `--for` on a Service reaches pods added by scaling up; `stop` reverts every pod exactly once and keeps the Broadcast `Completed` |
| `TestContinuousSurvivesRestartAndExpires` | After the controller restarts, new pods are still reached; at expiry every pod is reverted exactly once, with no duplicate calls |
| `TestNamespacedInstall` | The single-namespace manifest runs broadcasts in its namespace without a policy, and ignores other namespaces |
| `TestDefaultUserRolesCoverBroadcasts` | With `deploy/user-roles.yaml`, Kubernetes' `view`, `edit` and `admin` roles get the documented permissions, and `edit` is enough to run a broadcast |

## Where it has run

| Environment | Kubernetes | When | Result |
|---|---|---|---|
| kind on GitHub Actions | 1.35, 1.36, 1.37, and 1.37 dual-stack | Every pull request and every push to `main` | All tests pass |
| kind with Docker Desktop | 1.37 | Manually, 2026-11-06 | All tests pass |
| Amazon EKS | 1.37 | Manually, 2026-10-30 | All tests pass; 100 pods in 1.6 seconds |

## Not covered yet

- A policy change that ends an active `Continuous` broadcast
- The controller calling pods over HTTPS (direct mode's HTTPS is covered)
- A pod deleted while it is being called (covered by unit tests only)
- A revert that fails, and the resulting exit code
- StatefulSet and DaemonSet targets end to end (they share the Deployment code path, which is covered)
- The 1,000-pod limits end to end (covered by unit tests), failover between two controller replicas, and service meshes with strict mTLS

## Running the end-to-end suite

`make e2e` needs Docker and kind. It builds the test images and the controller image, creates a kind cluster, installs the controller, runs the suite, and deletes the cluster.

| Variable | Effect |
|---|---|
| `KEEP_CLUSTER=1` | Keep the cluster afterwards; an existing one is reused |
| `CLUSTER=NAME` | kind cluster name (default `pod-broadcast-e2e`) |
| `DUAL_STACK=1` | Create a dual-stack cluster |
| `SKIP_BUILD=1` | Use images that are already built |
| `E2E_SKIP_SCALE=1` | Skip the 100-pod test |

`test/e2e/run.sh NODE_IMAGE` runs it on another Kubernetes version, for example a `kindest/node` image from the [kind releases](https://github.com/kubernetes-sigs/kind/releases).
