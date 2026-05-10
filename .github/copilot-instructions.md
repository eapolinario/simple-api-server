# Copilot instructions

> Status: **bootstrapping**. The repository is intentionally near-empty. Most of
> what follows describes architectural commitments that have already been
> decided through discussion — treat them as constraints, not suggestions.
> Anything not yet implemented is labeled _(planned)_.

## Plan and todo tracking

The durable plan lives at [`docs/plan.md`](../docs/plan.md). It contains the
problem statement, approach, todo list with dependencies, and a SQL seed
block that rehydrates the in-session `todos` / `todo_deps` tables.

**At the start of every session in this repo:**

1. Read `docs/plan.md` to load context.
2. If `SELECT COUNT(*) FROM todos` returns 0, paste the seed block from
   `docs/plan.md` into the `sql` tool to repopulate. The seed uses
   `INSERT OR IGNORE`, so re-running is safe.
3. Use the "ready work" query from the plan to find the next unblocked todo.
4. Set status to `in_progress` before starting; `done` when committed.
5. When a status change is meaningful (todo completed, scope changed, new
   todo added), update `docs/plan.md` and commit it alongside the code
   change. The markdown file is the source of truth across sessions; SQL is
   the live working view within a session.

## What this project is

An **aggregated Kubernetes API server** built using only official extension
points (no kube-apiserver fork, no patches). It registers an `APIService` with
an existing kube-apiserver and serves a custom API group from a separate
binary that links against `k8s.io/apiserver`.

Module path: `github.com/eapolinario/simple-api-server`.

## Architectural commitments (do not silently change these)

These decisions were made deliberately. If a request would violate one,
**stop and ask** rather than "fix" it.

1. **Aggregated APIServer, not CRDs, not webhooks.** The whole point is to
   exercise the aggregation layer. Do not propose replacing this with
   CRDs + a controller, even though it would be simpler — that defeats the
   experiment.
2. **In-memory storage backend.** A `map[types.NamespacedName]runtime.Object`
   guarded by `sync.RWMutex`. **No etcd. No SQL. No disk persistence.** Server
   restart wipes state, and that's intentional.
3. **Watch is deferred.** `?watch=true` requests must return HTTP 405
   (`MethodNotAllowed`) until watch is implemented properly. There must be a
   test asserting this so it cannot regress silently into a half-working watch.
   Implications: informers, `kubectl get -w`, and controllers that depend on
   our resources will not work yet — that is known.
4. **Single API: `tasks.example.com/v1alpha1`, Kind `Task`** with a status
   subresource. Spec/status split is non-trivial: spec is user intent, status
   carries `phase`, `conditions`, and `observedGeneration` and is only mutable
   via the `/status` subresource.
5. **Auth: delegating in prod, permissive in tests.** Production deployment
   uses `DelegatingAuthenticationOptions` / `DelegatingAuthorizationOptions`
   (delegate to the host kube-apiserver). Test harness uses
   `--authorization-mode=AlwaysAllow` and anonymous auth — never the other
   way around.
6. **Tests are first-class.** Every behavioral change ships with a test in the
   same PR. See "Test discipline" below.

## Project layout _(follows kubernetes/sample-apiserver)_

```
cmd/simple-apiserver/main.go         # binary entrypoint; wires options + server
pkg/apis/tasks/                      # internal API types
  v1alpha1/                          # external (versioned) API types
    types.go                         # Task, TaskSpec, TaskStatus, TaskList
    register.go                      # GroupVersion + AddToScheme
    zz_generated_deepcopy.go         # generated; do not edit
  install/install.go                 # registers all versions into a Scheme
pkg/registry/tasks/task/
  strategy.go                        # rest.RESTCreateUpdateStrategy + status strategy
  storage.go                         # REST + StatusREST wired to the in-memory store
pkg/storage/inmemory/                # in-memory backend (map + sync.RWMutex + monotonic RV)
pkg/apiserver/apiserver.go           # GenericAPIServer config + APIGroupInfo install
hack/update-codegen.sh               # invokes k8s.io/code-generator/kube_codegen.sh
hack/boilerplate.go.txt              # header prepended to generated files
test/integration/                    # in-process apiserver tests
test/e2e/                            # //go:build e2e, runs against kind
manifests/                           # APIService YAML and friends
flake.nix / flake.lock / .envrc      # Nix dev shell
Justfile                             # task runner
go.mod
```

The package directories are scaffolded with `doc.go` placeholders today;
real types and code arrive in subsequent todos.

When adding code, place it according to this layout. If a file doesn't have
an obvious home, ask.

## Dev environment

The repo is a **Nix flake** with `direnv` auto-activation. Outside Nix the
project may still build with whatever Go is on `$PATH`, but the canonical
toolchain is the dev shell:

```sh
nix develop          # or: direnv allow (one-time, then auto-loads on cd)
go version           # currently 1.26.x via nixpkgs-unstable
```

The dev shell provides: `go`, `gopls`, `golangci-lint`, `gotools`, `just`,
`kubectl`, `kind`. Do not add tool installations to a shell script — add
them to `flake.nix`.

`flake.lock` is committed; bump it deliberately with `nix flake update`.

## Build, test, run _(some targets are stubs until later todos)_

`Justfile` is the task runner (not Make). Run `just` with no args to list
recipes. Current surface:

```sh
just build              # go build ./cmd/simple-apiserver
just test               # default loop: go test ./...
just test-unit          # go test -short ./...
just test-integration   # go test ./test/integration/...
just test-e2e           # go test -tags=e2e ./test/e2e/...   (requires kind)
just run                # go run ./cmd/simple-apiserver
just fmt                # gofmt -w .
just lint               # golangci-lint run
just codegen            # TODO: wired up in codegen-setup todo
just verify-codegen     # TODO: wired up in codegen-setup todo
just clean              # go clean ./...
```

### Running a single test

```sh
go test ./pkg/registry/tasks/task/ -run TestStrategy_Validate_RejectsEmptyImage -v
go test -tags=e2e ./test/e2e/ -run TestE2E_CreateTaskViaAPIService -v
```

Always pass `-run` and `-v` for targeted runs; never grep test output to
"figure out what failed" when `-v` would just show it.

## Codegen workflow

1. Edit types in `pkg/apis/tasks/v1alpha1/types.go`.
2. Run `make codegen` (wraps `k8s.io/code-generator/kube_codegen.sh`).
3. Commit the regenerated `zz_generated_*.go` files in the same commit as the
   type change. **Never hand-edit generated files.**
4. CI runs `make verify-codegen`; a stale tree fails the build.

Generators in scope: `deepcopy`, `openapi`, `client`. Out of scope until
needed: `conversion` (only one version), `defaulter` (no defaults yet).

## Key conventions

- **Strategy pattern.** Validation, defaulting, and "what is mutable" live in
  `pkg/registry/tasks/task/strategy.go` as a `rest.RESTCreateUpdateStrategy`
  implementation. There is a separate `statusStrategy` for the status
  subresource that allows mutation of `status.*` only and bumps nothing in
  spec. Do not put validation in HTTP handlers or in the storage layer.
- **`observedGeneration` discipline.** Any controller-style update of status
  must set `status.observedGeneration = metadata.generation`. Tests should
  cover the case where generation advances but observedGeneration lags.
- **Conditions.** Use `k8s.io/apimachinery/pkg/apis/meta/v1.Condition` (the
  standard shape: Type, Status, Reason, Message, LastTransitionTime,
  ObservedGeneration). Don't invent a custom condition struct.
- **Errors.** Return `k8s.io/apimachinery/pkg/api/errors` typed errors
  (`NewNotFound`, `NewAlreadyExists`, `NewInvalid`) — never raw `fmt.Errorf`
  out of REST handlers. The aggregator turns these into correct HTTP status
  codes.
- **ResourceVersion.** Until watch lands, RV is a monotonic `uint64` from the
  in-memory store, stringified. It must increment on every successful write
  (including status-only writes). A test should assert monotonicity across
  mixed spec/status updates.

## Test discipline

Three layers, in order of how often they run:

1. **Unit** (`go test -short ./...`): pure functions — strategy, validation,
   the in-memory store. No HTTP, no apiserver. Should run in < 1 second total.
2. **In-process integration** (`test/integration/`): boots the full apiserver
   in-process via the sample-apiserver test harness pattern
   (`apiservertesting.StartTestServer`-style), exercises it through a real
   REST client. **This is the default test loop** — `make test` runs unit +
   this layer.
3. **E2E** (`test/e2e/`, gated by `//go:build e2e`): kind cluster, real
   APIService registration, real kube-apiserver out front. Slow; not in the
   default loop. Run before merging anything that touches serving config,
   auth, or APIService manifests.

Rules:

- Every new field on `TaskSpec` or `TaskStatus` ships with at least one
  validation test (positive and negative case) and one round-trip test
  through the integration layer.
- Every "we deferred this" decision (watch, multi-version, persistence) has
  an explicit failing-as-expected test that documents the deferral. If
  someone accidentally implements half of it, the test fails and forces a
  conversation.
- Prefer table-driven tests for strategy/validation. One `t.Run` per row.

## Non-goals (explicit)

- Implementing watch — deferred until list/get/create/update/delete are solid
  and well-tested.
- Multi-version support / conversion webhooks.
- Persistence across restarts.
- Production-grade auth (we delegate; we don't implement).
- A typed Go client for external consumers — `client-gen` output is for our
  own tests, not a published SDK.

## When asked to add something

- **A new field on Task:** types.go → codegen → strategy validation → unit
  test → integration round-trip test. In that order.
- **A new subresource:** new `*REST` type in `storage.go`, registered in
  `apiserver.go`'s `APIGroupInfo`, plus its own strategy. Don't piggyback on
  the main resource's strategy.
- **A new API resource (not Task):** new package under `pkg/apis/<group>/`
  and `pkg/registry/<group>/<resource>/`. Reuse the in-memory store
  generically — don't fork it per resource.
- **"Just add watch real quick":** No. Watch correctness is its own milestone
  (proper monotonic RV across the store, replay buffer, bookmarks, or
  wrapping in `cacher.Cacher`). Open an issue, don't sneak it in.

## Things to push back on

If a request would do any of the following, surface the trade-off before
acting:

- Adding etcd, SQLite, or any persistent backend.
- Replacing the aggregated apiserver with CRDs.
- Implementing a "minimal" watch that only handles the happy path.
- Skipping codegen and hand-writing `DeepCopy` methods.
- Putting validation logic anywhere other than the strategy.
- Adding a test that requires the network or a running cluster to the default
  `make test` loop.
