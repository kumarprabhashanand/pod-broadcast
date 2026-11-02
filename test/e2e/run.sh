#!/usr/bin/env bash
# Runs the end-to-end tests on a kind cluster, with the controller installed cluster-wide.
#
#   test/e2e/run.sh [NODE_IMAGE]
#
# CLUSTER       kind cluster name (default pod-broadcast-e2e)
# KEEP_CLUSTER  set to keep the cluster afterwards; an existing cluster is reused
# DUAL_STACK    set to create a dual-stack cluster
# SKIP_BUILD    set when the test images are already built
# E2E_SKIP_SCALE  set to skip the 100-pod test
set -euo pipefail

cd "$(dirname "$0")/../.."
NODE_IMAGE=${1:-kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5}
CLUSTER=${CLUSTER:-pod-broadcast-e2e}
CONFIG=test/e2e/kind.yaml
if [[ -n ${DUAL_STACK:-} ]]; then
  CONFIG=test/e2e/kind-dual-stack.yaml
  export E2E_DUAL_STACK=1
fi
IMAGES=(pod-broadcast-echo:e2e pod-broadcast-spring:e2e pod-broadcast-controller:dev)

if [[ -z ${SKIP_BUILD:-} ]]; then
  docker build -t pod-broadcast-echo:e2e test/apps/echo
  docker build -t pod-broadcast-spring:e2e test/apps/spring
  docker build -t pod-broadcast-controller:dev .
fi

if ! kind get clusters | grep -qx "$CLUSTER"; then
  kind create cluster --name "$CLUSTER" --image "$NODE_IMAGE" --config "$CONFIG" --wait 120s
fi
if [[ -z ${KEEP_CLUSTER:-} ]]; then
  trap 'kind delete cluster --name "$CLUSTER"' EXIT
fi
kind load docker-image --name "$CLUSTER" "${IMAGES[@]}"

KUBECTL=(kubectl --context "kind-$CLUSTER")
"${KUBECTL[@]}" apply -f deploy/crds/ -f deploy/controller.yaml -f deploy/user-roles.yaml
"${KUBECTL[@]}" -n pod-broadcast-system rollout restart deploy/pod-broadcast-controller
"${KUBECTL[@]}" -n pod-broadcast-system rollout status deploy/pod-broadcast-controller --timeout 120s

E2E_CONTEXT="kind-$CLUSTER" go test -tags e2e -count=1 -timeout 30m -parallel 3 -v ./test/e2e/
