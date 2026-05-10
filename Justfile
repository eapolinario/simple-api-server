set shell := ["bash", "-c"]

# Local-dev workdir. Holds the self-signed serving cert and a generated
# kubeconfig pointing at the running server. Gitignored.
LOCAL_RUN_DIR := "./.local-run"
DEV_PORT := "18443"
DEV_SERVER := "https://127.0.0.1:" + DEV_PORT

# List available recipes
default:
    @just --list

# Build the apiserver binary
build:
    go build ./cmd/simple-apiserver

# Run the default test loop (unit + in-process integration)
test:
    go test ./...

# Run unit tests with the race detector (requires CGO)
test-race:
    CGO_ENABLED=1 go test -race ./...

# Run unit tests only
test-unit:
    go test -short ./...

# Run in-process integration tests
test-integration:
    go test ./test/integration/...

# Run kind-based E2E tests (slow; requires a kind cluster)
test-e2e:
    go test -tags=e2e ./test/e2e/...

# Run the apiserver locally with strict auth (delegating-style). Useful
# for inspecting flags / smoke-checking startup; kubectl will get 403
# without --authorization-kubeconfig wired at a real cluster. For a
# kubectl-friendly loopback session, prefer `just dev`.
run:
    go run ./cmd/simple-apiserver

# Run the apiserver locally with kubectl-friendly defaults: loopback
# bind, self-signed cert under {{LOCAL_RUN_DIR}}/certs, in-cluster authn
# lookup skipped, and authz set to AlwaysAllow.
#
# DO NOT use this on a non-loopback interface — anonymous users get full
# write access under this configuration. Permissive auth is strictly for
# local development per the project's auth posture.
#
# Run `just dev-kubeconfig` in another shell to get a usable kubeconfig.
dev:
    @mkdir -p {{LOCAL_RUN_DIR}}/certs
    go run ./cmd/simple-apiserver \
        --secure-port={{DEV_PORT}} \
        --bind-address=127.0.0.1 \
        --cert-dir={{LOCAL_RUN_DIR}}/certs \
        --authentication-skip-lookup=true \
        --authorization-mode=AlwaysAllow

# Generate a kubeconfig pointing at the local dev server. Requires that
# `just dev` has been started at least once (so the serving cert exists
# under {{LOCAL_RUN_DIR}}/certs).
dev-kubeconfig:
    #!/usr/bin/env bash
    set -euo pipefail
    cert="{{LOCAL_RUN_DIR}}/certs/apiserver.crt"
    if [ ! -f "$cert" ]; then
        echo "no serving cert at $cert; run 'just dev' in another shell first" >&2
        exit 1
    fi
    kc="{{LOCAL_RUN_DIR}}/kubeconfig"
    kubectl config --kubeconfig="$kc" set-cluster simple-apiserver-local \
        --server={{DEV_SERVER}} \
        --certificate-authority="$cert" --embed-certs=true >/dev/null
    # kubectl 1.36's BasicAuth provider prompts interactively when the
    # user has no credentials at all, even though the server allows
    # anonymous access. Stash a placeholder bearer token so kubectl
    # doesn't prompt; the server can't validate it (no token authn
    # backend wired) and falls through to anonymous, which the
    # AlwaysAllowPaths above permit.
    kubectl config --kubeconfig="$kc" set-credentials dev \
        --token=dev-placeholder-token >/dev/null
    kubectl config --kubeconfig="$kc" set-context simple-apiserver-local \
        --cluster=simple-apiserver-local --user=dev --namespace=default >/dev/null
    kubectl config --kubeconfig="$kc" use-context simple-apiserver-local >/dev/null
    echo
    echo "Kubeconfig written to $kc"
    echo "Use:  export KUBECONFIG=\"$(pwd)/$kc\""

# Remove everything `just dev` and `just dev-kubeconfig` produce.
dev-clean:
    rm -rf {{LOCAL_RUN_DIR}}

# Format source code
fmt:
    gofmt -w .

# Lint / static analysis
lint:
    golangci-lint run

# Regenerate deepcopy / openapi / client code
codegen:
    hack/update-codegen.sh

# Verify generated code is up to date (CI-friendly: regenerates and fails on diff)
verify-codegen:
    #!/usr/bin/env bash
    set -euo pipefail
    hack/update-codegen.sh
    if ! git diff --exit-code -- pkg/apis pkg/generated hack/api-violations.report; then
        echo "::error::Generated code is out of date. Run 'just codegen' and commit the result."
        exit 1
    fi

# Remove build artifacts
clean:
    go clean ./...
    rm -f simple-apiserver
