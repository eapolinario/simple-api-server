#!/usr/bin/env bash
#
# hack/e2e-up.sh — stand up a kind cluster with the aggregated
# simple-apiserver registered and ready, then print the kubeconfig path.
#
# Idempotent: re-running on top of an existing cluster rebuilds the
# image, reloads it, restarts the deployment, and re-applies the
# manifests. Use hack/e2e-down.sh to tear everything down.
#
# Prerequisites (provided by the flake devshell except for the daemon):
#   * docker daemon running on the host
#   * kind, kubectl, docker CLI on PATH (devshell)
#
# This script is the only supported way to provision the e2e
# environment. The Go tests under test/e2e read KUBECONFIG and assume
# the cluster is already up.

set -euo pipefail

CLUSTER_NAME="${KIND_CLUSTER_NAME:-simple-apiserver-e2e}"
IMAGE="simple-apiserver:e2e"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFESTS_DIR="${REPO_ROOT}/manifests"

log() { printf '\033[1;34m==>\033[0m %s\n' "$*" >&2; }

# 1. Ensure the kind cluster exists. We don't recreate it if it's
#    already there — that's the whole point of idempotence.
if ! kind get clusters | grep -qx "${CLUSTER_NAME}"; then
    log "Creating kind cluster '${CLUSTER_NAME}'"
    kind create cluster --name "${CLUSTER_NAME}" --wait 60s
else
    log "Reusing existing kind cluster '${CLUSTER_NAME}'"
fi

# 2. Build the server image and load it into the cluster. Always rebuild
#    — the cost is dominated by the Go build cache, and rebuilding
#    guarantees the cluster sees the working tree.
log "Building ${IMAGE}"
docker build -t "${IMAGE}" "${REPO_ROOT}"

log "Loading ${IMAGE} into kind"
kind load docker-image "${IMAGE}" --name "${CLUSTER_NAME}"

# 3. Apply manifests. Order matters: namespace + RBAC + deployment +
#    service first, then the (cluster-scoped) APIService once the
#    Service exists, otherwise the aggregator marks the APIService
#    unavailable until the next reconcile.
KUBECONFIG_PATH="$(kind get kubeconfig-path --name "${CLUSTER_NAME}" 2>/dev/null || true)"
if [ -z "${KUBECONFIG_PATH}" ]; then
    # Newer kind versions don't expose `kubeconfig-path`; write one.
    KUBECONFIG_PATH="${REPO_ROOT}/.local-run/kind-${CLUSTER_NAME}.kubeconfig"
    mkdir -p "$(dirname "${KUBECONFIG_PATH}")"
    kind get kubeconfig --name "${CLUSTER_NAME}" > "${KUBECONFIG_PATH}"
fi
export KUBECONFIG="${KUBECONFIG_PATH}"

log "Applying in-cluster manifests"
kubectl apply -f "${MANIFESTS_DIR}/e2e/"

# Force a rollout so a re-run actually picks up the newly loaded image
# (the image tag is stable, so kubelet wouldn't otherwise restart pods).
log "Restarting deployment to pick up freshly loaded image"
kubectl -n simple-apiserver rollout restart deployment/simple-apiserver
kubectl -n simple-apiserver rollout status deployment/simple-apiserver --timeout=120s

log "Registering APIService"
kubectl apply -f "${MANIFESTS_DIR}/apiservice.yaml"

# 4. Wait for the aggregator to mark the APIService Available. This is
#    the real gate — `kubectl rollout status` only proves the pod is
#    serving /readyz, not that the kube-apiserver can reach it.
log "Waiting for APIService v1alpha1.tasks.example.com to be Available"
kubectl wait --for=condition=Available \
    apiservice/v1alpha1.tasks.example.com --timeout=120s

log "Ready. Export:"
echo "  export KUBECONFIG=${KUBECONFIG_PATH}"
