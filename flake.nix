{
  description = "simple-api-server — aggregated Kubernetes APIServer experiment";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" "x86_64-darwin" ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
      pkgsFor = sys: import nixpkgs { system = sys; };
    in {
      devShells = forAllSystems (system:
        let pkgs = pkgsFor system;
        in {
          default = pkgs.mkShell {
            packages = [
              pkgs.go
              pkgs.gopls
              pkgs.golangci-lint
              pkgs.gotools
              pkgs.just
              pkgs.kubectl
              pkgs.kind
              # docker CLI only — the daemon is a host concern
              # (system service on Linux, Docker Desktop on macOS).
              # kind + the e2e image-build path both shell out to `docker`.
              pkgs.docker-client
            ];
          };
        });

      packages = forAllSystems (system:
        let pkgs = pkgsFor system;
        in {
          default = pkgs.buildGoModule {
            pname = "simple-api-server";
            version = "0.1.0";
            src = ./.;
            # TODO: replace with real hash after first `nix build` (Nix will
            # report the expected value on failure, then update this line).
            vendorHash = pkgs.lib.fakeHash;
            subPackages = [ "cmd/simple-apiserver" ];
          };
        });
    };
}
