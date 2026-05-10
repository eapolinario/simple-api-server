#!/usr/bin/env bash
# Copyright 2026 Eduardo Apolinario.
#
# Regenerates code under pkg/apis/... using k8s.io/code-generator.
#
# Currently wired:
#   * deepcopy-gen   -> pkg/apis/tasks/v1alpha1/zz_generated.deepcopy.go
#   * openapi-gen    -> pkg/generated/openapi/zz_generated.openapi.go
#
# client-gen is intentionally not wired in yet: its informers and listers
# depend on watch, which is deferred for this experiment, so generating
# them today would emit code that 405s against our own server.

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

# kube_codegen.sh's gen_* helpers call `go install` and then run binaries
# from ${GOBIN}. Pin GOBIN to a predictable location so successive runs
# reuse it.
export GOBIN="${SCRIPT_ROOT}/.tools/bin"
export PATH="${GOBIN}:${PATH}"
mkdir -p "${GOBIN}"

# shellcheck source=/dev/null
source "${CODEGEN_PKG}/kube_codegen.sh"

THIS_PKG="github.com/eapolinario/simple-api-server"

echo "Running deepcopy-gen against pkg/apis ..."
kube::codegen::gen_helpers \
    --boilerplate "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
    "${SCRIPT_ROOT}/pkg/apis"

echo "Running openapi-gen against pkg/apis ..."
kube::codegen::gen_openapi \
    --boilerplate "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
    --output-dir "${SCRIPT_ROOT}/pkg/generated/openapi" \
    --output-pkg "${THIS_PKG}/pkg/generated/openapi" \
    --report-filename "${SCRIPT_ROOT}/hack/api-violations.report" \
    --update-report \
    "${SCRIPT_ROOT}/pkg/apis"

