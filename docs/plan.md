# Plan: bootstrap simple-api-server (aggregated k8s apiserver)

> **This file is the durable source of truth for the project plan.**
> The active in-session SQL working set (in `todos` / `todo_deps`) is rehydrated
> from the seed block at the bottom. If a Copilot session starts with no todos
> loaded, run that block via the `sql` tool to populate them. When status
> changes during a session, update both the SQL row and \(if the change is
> meaningful\) this file in the same commit.

## Problem

Empty repo. Goal: implement a Kubernetes API server using only official
extension points, with tests as a first-class citizen. Through discussion,
locked in:

- Aggregated APIServer (APIService + `k8s.io/apiserver`)
- In-memory storage backend (no etcd, no persistence)
- Single resource: `tasks.example.com/v1alpha1` Kind `Task` with status
  subresource (phase / conditions / observedGeneration)
- Watch deferred → returns 405, asserted by test
- Test layers: unit + in-process integration as default loop; kind E2E gated
  by `//go:build e2e`
- Auth: delegating in prod, AlwaysAllow + anonymous in tests
- Module path: `github.com/eapolinario/simple-api-server`
- Dev environment: Nix flake (`nixpkgs-unstable`) + direnv + Justfile;
  Go via `pkgs.go` (currently 1.26.x)

## Approach

Follow the kubernetes/sample-apiserver layout. Bring up the simplest
end-to-end slice first (Task with create/get/list/delete, no validation
beyond required fields), then layer on status subresource, conditions,
observedGeneration, and finally the deferred-watch test.

## Todos

| id | title | depends on |
|---|---|---|
| `init-module` | Initialize Go module + skeleton | — |
| `types-v1alpha1` | Define Task v1alpha1 types | `init-module` |
| `codegen-setup` | Wire k8s code-generator (deepcopy) | `types-v1alpha1` |
| `codegen-openapi` | Wire openapi-gen alongside deepcopy | `codegen-setup` |
| `inmemory-store` | Implement in-memory store | `init-module` |
| `strategy` | Implement Task strategy + status strategy | `types-v1alpha1` |
| `registry-storage` | REST + StatusREST wiring | `inmemory-store`, `strategy` |
| `apiserver-wiring` | GenericAPIServer config | `registry-storage`, `codegen-openapi` |
| `cmd-main` | Binary entrypoint | `apiserver-wiring` |
| `integration-harness` | In-process integration tests | `cmd-main`, `codegen-setup` |
| `watch-405-test` | Assert watch returns 405 | `integration-harness` |
| `apiservice-manifest` | APIService YAML | `apiserver-wiring` |
| `e2e-skeleton` | Kind-based E2E | `apiservice-manifest`, `integration-harness` |
| `makefile` | Justfile surface | `init-module` |
| `ci` | GitHub Actions CI | `makefile` |

Descriptions for each todo are in the seed block below — keep them in sync
with this table when editing.

## Notes / decisions

- `Job` was rejected as a Kind name to avoid mental-model collision with
  `batch/v1.Job`. Settled on `Task` (short name `tk`).
- Watch is the most important deferred item. Doing it right means either
  wrapping a minimal storage in `k8s.io/apiserver/pkg/storage/cacher.Cacher`
  or implementing monotonic RV + replay buffer + bookmarks ourselves. Either
  is a milestone of its own — not a "real quick" addition.
- Codegen entrypoint: `k8s.io/code-generator/kube_codegen.sh` (modern), not
  the older `generate-internal-groups.sh`.
- `codegen-setup` ships **deepcopy only** in its first PR. `openapi-gen`
  follows as the separate `codegen-openapi` todo (now merged) — it lands
  before `apiserver-wiring` because `genericapiserver.RecommendedConfig`
  consumes the generated `GetOpenAPIDefinitions`. `client-gen` is deferred
  to no earlier than watch — its informers/listers depend on watch and
  would otherwise emit code that 405s against our server.
  The `+k8s:deepcopy-gen=package` and `+k8s:openapi-gen=true` markers live
  in `pkg/apis/tasks/v1alpha1/doc.go` in **standalone** comment blocks
  (not the package doc comment) — gengo only picks them up that way.
- `genericregistry.Store` is **not** used — its etcd assumptions leak. We
  implement REST handlers against our own store directly.
- **PR / merge convention:** A todo is only marked `done` once its PR has
  actually merged into `main`. While a PR is open, the todo stays
  `in_progress`. Local commits don't count.
- `cmd-main` ships a `--authorization-mode` flag with values `""` (default,
  delegating) and `AlwaysAllow` (local-dev only, paired with a loopback
  bind). The path-based `--authorization-always-allow-paths` upstream flag
  is not sufficient because its authorizer ignores resource requests
  (`k8s.io/apiserver/pkg/authorization/path` returns `NoOpinion` whenever
  `IsResourceRequest()` is true). The `just dev` / `just dev-kubeconfig` /
  `just dev-clean` recipes are the supported way to drive the binary
  locally with kubectl.

## SQL seed block (rehydrate in-session todos)

Copilot CLI sessions have a per-session SQLite database (`todos` and
`todo_deps` tables already exist). At the start of a new session working in
this repo, paste the block below into the `sql` tool to populate it. Uses
`INSERT OR IGNORE` so it is safe to run repeatedly.

```sql
INSERT OR IGNORE INTO todos (id, title, description, status) VALUES
  ('init-module',         'Initialize Go module + skeleton',         'go mod init github.com/eapolinario/simple-api-server, create empty package directories matching the planned layout, add hack/boilerplate.go.txt', 'pending'),
  ('types-v1alpha1',      'Define Task v1alpha1 types',              'pkg/apis/tasks/v1alpha1/types.go: Task, TaskSpec (image, command), TaskStatus (phase, conditions, observedGeneration), TaskList; register.go with GroupVersion + AddToScheme', 'pending'),
  ('codegen-setup',       'Wire k8s code-generator (deepcopy)',      'hack/update-codegen.sh invoking kube_codegen.sh::gen_helpers; flesh out Justfile codegen + verify-codegen recipes; generate deepcopy', 'pending'),
  ('codegen-openapi',     'Wire openapi-gen alongside deepcopy',     'extend hack/update-codegen.sh with kube_codegen.sh::gen_openapi; add openapi-gen to go.mod tool directive; add +k8s:openapi-gen=true marker to doc.go; commit hack/api-violations.report baseline', 'pending'),
  ('inmemory-store',      'Implement in-memory store',               'pkg/storage/inmemory: map keyed by namespaced name, sync.RWMutex, monotonic uint64 RV, generic over runtime.Object; unit tests for CRUD + RV monotonicity', 'pending'),
  ('strategy',            'Implement Task strategy + status strategy','pkg/registry/tasks/task/strategy.go: validation, mutability rules, observedGeneration discipline; table-driven unit tests', 'pending'),
  ('registry-storage',    'REST + StatusREST wiring',                'pkg/registry/tasks/task/storage.go: REST and StatusREST backed by in-memory store; typed errors via k8s.io/apimachinery/pkg/api/errors', 'pending'),
  ('apiserver-wiring',    'GenericAPIServer config',                 'pkg/apiserver/apiserver.go: APIGroupInfo install, scheme registration, options', 'done'),
  ('cmd-main',            'Binary entrypoint',                       'cmd/simple-apiserver/main.go: option parsing, delegating auth in prod, server start', 'done'),
  ('integration-harness', 'In-process integration tests',            'test/integration: boot server in-process, REST client round-trips for create/get/list/update/delete and status subresource', 'pending'),
  ('watch-405-test',      'Assert watch returns 405',                'test asserting ?watch=true returns MethodNotAllowed; lives in test/integration; documents the deferral', 'pending'),
  ('apiservice-manifest', 'APIService YAML',                         'manifests/apiservice.yaml registering v1alpha1.tasks.example.com', 'pending'),
  ('e2e-skeleton',        'Kind-based E2E',                          'test/e2e with //go:build e2e: apply APIService against kind, create Task through aggregator, assert reachable', 'pending'),
  ('makefile',            'Justfile surface',                        'build, test, test-unit, test-integration, test-e2e, codegen, verify-codegen, lint, fmt, clean recipes (the `makefile` id is retained for stability — the file itself is a Justfile)', 'pending'),
  ('ci',                  'GitHub Actions CI',                       'workflow running unit + integration + verify-codegen on PRs', 'pending');

INSERT OR IGNORE INTO todo_deps (todo_id, depends_on) VALUES
  ('types-v1alpha1',      'init-module'),
  ('codegen-setup',       'types-v1alpha1'),
  ('codegen-openapi',     'codegen-setup'),
  ('inmemory-store',      'init-module'),
  ('strategy',            'types-v1alpha1'),
  ('registry-storage',    'strategy'),
  ('registry-storage',    'inmemory-store'),
  ('apiserver-wiring',    'registry-storage'),
  ('apiserver-wiring',    'codegen-openapi'),
  ('cmd-main',            'apiserver-wiring'),
  ('integration-harness', 'cmd-main'),
  ('integration-harness', 'codegen-setup'),
  ('watch-405-test',      'integration-harness'),
  ('apiservice-manifest', 'apiserver-wiring'),
  ('e2e-skeleton',        'apiservice-manifest'),
  ('e2e-skeleton',        'integration-harness'),
  ('makefile',            'init-module'),
  ('ci',                  'makefile');
```

### Querying ready work

```sql
-- Todos with all dependencies satisfied:
SELECT t.id, t.title
FROM todos t
WHERE t.status = 'pending'
  AND NOT EXISTS (
    SELECT 1 FROM todo_deps d
    JOIN todos dep ON d.depends_on = dep.id
    WHERE d.todo_id = t.id AND dep.status != 'done'
  )
ORDER BY t.id;
```
