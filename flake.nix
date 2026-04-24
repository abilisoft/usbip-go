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
              pkgs.govulncheck
              pkgs.goreleaser
              pkgs.nfpm
              pkgs.syft
              pkgs.cosign
              pkgs.git-cliff
              pkgs.gh
              pkgs.git
              pkgs.coreutils
              pkgs.gnumake
            ];

            shellHook = ''
              export GOTOOLCHAIN=local
              export GOFLAGS="-trimpath"

              # Pin every Go cache under ./build/ unconditionally so inherited
              # env vars from the host (or a parent container) cannot redirect
              # writes outside the repo. Hermeticity requires ownership of
              # these paths, not merely a default.
              export GOCACHE="$PWD/build/cache/go-build"
              export GOMODCACHE="$PWD/build/cache/go-mod"
              export GOLANGCI_LINT_CACHE="$PWD/build/cache/golangci-lint"
              mkdir -p "$GOCACHE" "$GOMODCACHE" "$GOLANGCI_LINT_CACHE"

              # Assert that the go.mod `go` directive patch version matches
              # the toolchain shipped by nixpkgs. GOTOOLCHAIN=local turns any
              # mismatch into a cryptic build failure later; fail loud here.
              if [ -f "$PWD/go.mod" ]; then
                _gomod_go=$(awk '/^go [0-9]/ {print $2; exit}' "$PWD/go.mod")
                _shell_go=$(${go}/bin/go env GOVERSION | sed 's/^go//')
                if [ "$_gomod_go" != "$_shell_go" ]; then
                  echo "flake.nix: go.mod requires go $_gomod_go but devShell provides $_shell_go" >&2
                  echo "           bump the nixpkgs input or relax go.mod before using this shell." >&2
                  return 1 2>/dev/null || exit 1
                fi
                unset _gomod_go _shell_go
              fi

              echo "usbip-go devShell ready."
              echo "  go:      $(${go}/bin/go version | awk '{print $3}')"
              echo "  task:    $(${pkgs.go-task}/bin/task --version 2>/dev/null | head -1)"
            '';
          };
        });

      formatter = forAllSystems (pkgs: pkgs.nixpkgs-fmt);
    };
}
