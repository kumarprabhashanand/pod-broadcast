#!/usr/bin/env bash
# Prints the krew plugin manifest for a release, from the archives the release workflow builds.
#
#   hack/krew-manifest.sh VERSION DIST_DIR
set -euo pipefail

version=$1 dist=$2
repo=https://github.com/kumarprabhashanand/pod-broadcast

cat <<YAML
apiVersion: krew.googlecontainertools.github.com/v1alpha2
kind: Plugin
metadata:
  name: broadcast
spec:
  version: $version
  homepage: $repo
  shortDescription: Send an HTTP request to every ready pod
  description: |
    Sends one HTTP request to every ready pod of a Service, workload or label
    selector, and shows each pod's result with an exit code for scripts. A
    request through a Service reaches only one pod, so this is for changes
    that every replica has to get, such as clearing a cache or changing a log
    level. Presets cover common Spring Boot Actuator operations.

    Requests go through the API server's pod proxy with your own credentials,
    so nothing has to be installed in the cluster.
  caveats: |
    This plugin needs the pods/proxy permission in the target namespace, with
    the verb that matches each HTTP method. Details:
    $repo/blob/main/docs/rbac.md
  platforms:
YAML
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os=${target%/*} arch=${target#*/}
  ext=tar.gz bin=kubectl-broadcast
  if [[ $os == windows ]]; then
    ext=zip bin=kubectl-broadcast.exe
  fi
  archive=kubectl-broadcast_${version}_${os}_${arch}.$ext
  sha=$(sha256sum "$dist/$archive" | cut -d' ' -f1)
  cat <<YAML
  - selector:
      matchLabels:
        os: $os
        arch: $arch
    uri: $repo/releases/download/$version/$archive
    sha256: $sha
    bin: $bin
    files:
    - from: $bin
      to: .
    - from: LICENSE
      to: .
YAML
done
