#!/usr/bin/env bash
#
# hack/e2e-down.sh — tear down the e2e kind cluster created by
# hack/e2e-up.sh. Removes the cluster entirely; there is no partial
# teardown mode by design (this is a throwaway environment).

set -euo pipefail

CLUSTER_NAME="${KIND_CLUSTER_NAME:-simple-apiserver-e2e}"

if kind get clusters | grep -qx "${CLUSTER_NAME}"; then
    printf '\033[1;34m==>\033[0m Deleting kind cluster %s\n' "${CLUSTER_NAME}" >&2
    kind delete cluster --name "${CLUSTER_NAME}"
else
    printf '\033[1;33m==>\033[0m No cluster named %s; nothing to do\n' "${CLUSTER_NAME}" >&2
fi
