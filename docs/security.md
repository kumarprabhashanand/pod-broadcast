# Security

What the plugin and the controller can access, and how to limit it. To report a vulnerability, see [SECURITY.md](../SECURITY.md).

## Direct mode

- The plugin acts with your own Kubernetes credentials and adds no access of its own. Calls go through the API server's pod proxy, which needs `pods/proxy` with the verb for each HTTP method; see [Permissions](rbac.md). Whoever may use `pods/proxy` on a pod can reach every port of that pod.
- The API server removes the `Authorization` header from proxied requests, so the plugin refuses it. Pass credentials in another header, ideally with `--header-from-env` or `--headers-from-secret`.
- Certificates of pods called over HTTPS aren't verified.
- Values of headers from the environment or a Secret, and of headers with sensitive names, are shown as `<redacted>` in every output format, including `--dry-run`.

## Controller

**Who can make it call what.** Anyone who can create a Broadcast in a namespace makes the controller call that namespace's pods, from the controller's pods. They don't need access to the pods themselves. So:

- Grant `create` on `broadcasts` only to people and pipelines trusted to call the applications in the namespace.
- The cluster-wide install rejects broadcasts in namespaces without a BroadcastPolicy. Write policies with the narrowest targets, ports, methods and paths that work.
- The single-namespace install allows any broadcast when there is no policy; there, the namespace's RBAC is the only control.

**Secrets.** The controller reads Secrets one at a time by name, only those a Broadcast names in `headersFromSecrets`, and never lists or watches them. It uses a Secret only when it carries the label `broadcast.kumarprabhashanand.github.io/use=true` and, where policies apply, is listed in one. Header values from Secrets aren't written to the Broadcast or its Events.

**What a Broadcast reveals.** Anyone who can read a Broadcast sees:

- its `request.headers` in plain text, so put sensitive values in a Secret instead. For the same reason, the plugin refuses `--header-from-env` in controller mode;
- the first 256 bytes of each pod's response, in the status.

**Network.**

- The controller calls pod IPs directly, never other hosts, and doesn't follow redirects. Certificates of pods called over HTTPS aren't verified.
- If a pod is deleted and its IP goes to another pod between resolving the target and calling it, the request can reach the other pod. NetworkPolicies that admit the controller only to the pods it should call prevent this. Direct mode addresses pods by name through the API server.
- Metrics are served over plain HTTP on port 8080, without authentication. They hold only counts of broadcasts and calls. A NetworkPolicy can limit who reaches them.

**The controller's pod** runs as a non-root user with a read-only root filesystem, no Linux capabilities and the `RuntimeDefault` seccomp profile. Its own permissions are in the manifests under `deploy/`.
