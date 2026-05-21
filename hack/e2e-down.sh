#!/usr/bin/env bash
#
# hack/e2e-down.sh — tear down the e2e kind cluster created by
# hack/e2e-up.sh. Removes the cluster entirely; there is no partial
# teardown mode by design (this is a throwaway environment).
#
# Also explicitly removes the kind context from ~/.kube/config. `kind
# delete cluster` is supposed to do this on its own, but if the cluster
# crashed or the user nuked docker out from under it, the entry can be
# left behind — which then silently routes `kubectl` (and historically
# the e2e test) at a non-existent server. We belt-and-braces it.

set -euo pipefail

CLUSTER_NAME="${KIND_CLUSTER_NAME:-simple-apiserver-e2e}"
KIND_CONTEXT="kind-${CLUSTER_NAME}"

if kind get clusters | grep -qx "${CLUSTER_NAME}"; then
    printf '\033[1;34m==>\033[0m Deleting kind cluster %s\n' "${CLUSTER_NAME}" >&2
    kind delete cluster --name "${CLUSTER_NAME}"
else
    printf '\033[1;33m==>\033[0m No cluster named %s; nothing to delete\n' "${CLUSTER_NAME}" >&2
fi

# Defensive cleanup of any leftover kubeconfig entries. All three are
# safe to "fail" (kubectl exits non-zero when the entry doesn't exist)
# — that's the common case and not an error.
printf '\033[1;34m==>\033[0m Scrubbing leftover %s entries from kubeconfig\n' "${KIND_CONTEXT}" >&2
kubectl config delete-context "${KIND_CONTEXT}" >/dev/null 2>&1 || true
kubectl config delete-cluster "${KIND_CONTEXT}" >/dev/null 2>&1 || true
kubectl config delete-user "${KIND_CONTEXT}" >/dev/null 2>&1 || true
