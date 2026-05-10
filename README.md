# simple-api-server

> Experimental aggregated Kubernetes APIServer built on `k8s.io/apiserver`,
> using only official extension points (APIService aggregation layer).
> In-memory storage, no persistence — this is a learning / verification
> experiment, not a production system.

See [`docs/plan.md`](docs/plan.md) for the project plan and
[`.github/copilot-instructions.md`](.github/copilot-instructions.md) for the
architectural commitments.

## Development

```sh
nix develop          # or: direnv allow
just                 # list recipes
just build
just test
```

The dev shell pins Go via `nixpkgs-unstable` (currently Go 1.26.x — run
`go version` inside the shell to confirm), plus `gopls`, `golangci-lint`,
`kubectl`, and `kind`.
