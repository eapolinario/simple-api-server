#!/usr/bin/env bash
# Copyright 2026 Eduardo Apolinario.
#
# Regenerates code under pkg/apis/... using k8s.io/code-generator.
#
# Today this only runs the deepcopy generator (which produces
# zz_generated.deepcopy.go alongside each version package). openapi-gen and
# client-gen are intentionally not wired in yet; they will be added in
# follow-up work when they are actually needed (apiserver wiring for
# openapi, integration / external consumers for client). Watch is deferred,
# so client-gen's informers/listers would emit code that cannot run anyway.

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"

# kube_codegen.sh ships in the code-generator module. Locate the version that
# matches our go.mod (tracked via the `tool` directive on
# k8s.io/code-generator/cmd/deepcopy-gen) so we don't drift.
CODEGEN_PKG="$(cd "${SCRIPT_ROOT}" && go list -m -f '{{.Dir}}' k8s.io/code-generator)"
if [ -z "${CODEGEN_PKG}" ] || [ ! -f "${CODEGEN_PKG}/kube_codegen.sh" ]; then
    echo "could not locate kube_codegen.sh; is k8s.io/code-generator in go.mod?" >&2
    exit 1
fi

# kube_codegen.sh's gen_helpers calls `go install` and then runs binaries from
# ${GOBIN}. Pin GOBIN to a predictable location so successive runs reuse it.
export GOBIN="${SCRIPT_ROOT}/.tools/bin"
export PATH="${GOBIN}:${PATH}"
mkdir -p "${GOBIN}"

# shellcheck source=/dev/null
source "${CODEGEN_PKG}/kube_codegen.sh"

echo "Running deepcopy-gen against pkg/apis ..."
kube::codegen::gen_helpers \
    --boilerplate "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
    "${SCRIPT_ROOT}/pkg/apis"
