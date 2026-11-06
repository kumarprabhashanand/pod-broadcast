# Permissions

`kubectl broadcast` uses your own Kubernetes credentials. In the target namespace you need:

| Resource | Verbs | Why |
|---|---|---|
| `services` | `get` | Service targets |
| `endpointslices` (`discovery.k8s.io`) | `list` | Members of a Service |
| `pods` | `get`, `list` | Readiness and ports |
| `deployments`, `statefulsets`, `daemonsets`, `replicasets` (`apps`) | `get` | Workload targets |
| `pods/proxy` | depends on the method, see below | The calls themselves |
| `secrets` | `get` | Only with `--headers-from-secret` |

The verb on `pods/proxy` follows the HTTP method:

| Method | Verb |
|---|---|
| `GET`, `HEAD` | `get` |
| `POST` | `create` |
| `PUT` | `update` |
| `PATCH` | `patch` |
| `DELETE` | `delete` |

Anyone allowed to use `pods/proxy` on a pod can already call that pod's ports directly, so grant it only to people trusted with the applications in the namespace.

## Example Role

[`examples/rbac/direct-user.yaml`](../examples/rbac/direct-user.yaml) is a Role with every permission above except Secrets. Apply it and bind it to a user or group:

```sh
kubectl apply -n shop -f examples/rbac/direct-user.yaml
kubectl create rolebinding broadcast-direct-user -n shop --role=broadcast-direct-user --user=jane@example.com
```

Narrow it as needed, for example by dropping the `pods/proxy` verbs you don't use.

## Checking your access

```sh
kubectl auth can-i create pods --subresource=proxy -n shop
kubectl auth can-i list endpointslices.discovery.k8s.io -n shop
```

Before calling any pod, `kubectl broadcast` asks the API server whether you may use `pods/proxy` with the method. When a permission is missing, it stops with exit code 2, names the resource, the verb and the namespace, and calls no pod.
