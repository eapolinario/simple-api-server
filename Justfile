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

# Regenerate deepcopy / openapi / client code (TODO: wired up in codegen-setup)
codegen:
    @echo "TODO: codegen-setup todo will wire up hack/update-codegen.sh (kube_codegen.sh)"
    @exit 1

# Verify generated code is up to date (TODO: wired up in codegen-setup)
verify-codegen:
    @echo "TODO: codegen-setup todo will implement verify-codegen"
    @exit 1

# Remove build artifacts
clean:
    go clean ./...
    rm -f simple-apiserver
