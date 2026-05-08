{
  description = "usbip-go hermetic development environment";

  inputs = {
    # Pinned to a specific nixpkgs revision rather than a branch so
    # `nix flake update` is always a deliberate act. Only bump this when
    # `go_1_26` on the target revision matches (or exceeds) the patch level
    # declared in go.mod. Revisit once nixos-unstable ships >= go 1.26.2.
    nixpkgs.url = "github:NixOS/nixpkgs/54807374c670e3e468f97f4f621951ebb83d1673";
  };

  outputs = { self, nixpkgs, ... }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];

      forAllSystems = f:
        nixpkgs.lib.genAttrs systems (system:
          f (import nixpkgs { inherit system; }));

      # Minor version of the Go toolchain to select from nixpkgs. Must match
      # the `go X.Y` directive in go.mod at least at the minor level; the
      # shellHook asserts patch-level parity at activation time.
      goMinor = "1.26";
    in {
      devShells = forAllSystems (pkgs:
        let
          goAttr = "go_${builtins.replaceStrings ["."] ["_"] goMinor}";
          go = pkgs.${goAttr} or (throw
            "flake.nix: nixpkgs does not expose ${goAttr}; bump goMinor or the nixpkgs input");
        in {
          default = pkgs.mkShell {
            name = "usbip-go-dev";

            packages = [
              go
              pkgs.go-task
              pkgs.golangci-lint
              pkgs.gofumpt
              pkgs.gotools          # goimports, stringer, guru, etc.
              pkgs.govulncheck
              pkgs.goreleaser
              pkgs.moq
              pkgs.nfpm
              pkgs.syft
              pkgs.cosign
              pkgs.git-cliff
              pkgs.gh
              pkgs.git
              pkgs.coreutils
              pkgs.gnumake
            ];

            # Environment-only setup. The hermetic-cache paths and the
            # go.mod/toolchain parity check were moved into Taskfile.yml
            # (see the `_check:tooling` precondition) so all imperative
            # shell logic lives in one place.
            GOTOOLCHAIN = "local";
            GOFLAGS = "-trimpath";
          };
        });

      formatter = forAllSystems (pkgs: pkgs.nixpkgs-fmt);
    };
}
