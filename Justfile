set shell := ["bash", "-c"]

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

# Run the apiserver locally
run:
    go run ./cmd/simple-apiserver

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
    if ! git diff --exit-code -- pkg/apis; then
        echo "::error::Generated code is out of date. Run 'just codegen' and commit the result."
        exit 1
    fi

# Remove build artifacts
clean:
    go clean ./...
    rm -f simple-apiserver
