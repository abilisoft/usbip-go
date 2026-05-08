# SPDX-FileCopyrightText: 2026 AbiliSoft
# SPDX-License-Identifier: Apache-2.0
#
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

      # Modules needed to drive the integration suite's usbip gadget setup
      # and configfs tree. Declared once so the kernel config and the
      # runtime `modprobe` list cannot drift.
      microvmKernelModules = [
        "usbip_core"
        "vhci_hcd"
        "usbip_host"
        "usbip_vudc"
        "libcomposite"
        # SCSI + USB storage stack so the E2E data-transfer test can
        # attach a mass_storage gadget, have vhci_hcd import it, and
        # read the backing bytes back through a real /dev/sdN block
        # device. Pulling these in at boot time avoids racing `modprobe`
        # from inside the test.
        "scsi_mod"
        "sd_mod"
        "usb_storage"
      ];

      # NixOS config for the integration-test microVM. Kept minimal: tmpfs
      # root, no user accounts, /src and /nix/store bind-mounted from the
      # host via virtio-9p. The VM boots, auto-loads the USBIP modules,
      # runs whatever the runner script injects as $CMD, and powers off.
      microvmModule = { config, pkgs, lib, ... }: {
        system.stateVersion = "25.05";

        # We boot with `qemu -kernel` so there is no bootloader stage;
        # disabling grub skips the stage-1 assertions that would
        # otherwise demand boot.loader.grub.devices on an x86 target.
        boot.loader.grub.enable = false;
        boot.loader.systemd-boot.enable = false;

        # Emergency and rescue targets would normally drop the VM into an
        # interactive root shell on serial if stage-1 or fs-mount fails.
        # Under `-nographic` that hangs CI forever. Disabling emergency
        # mode wires emergency.service + rescue.service to poweroff via
        # NixOS's own poweroff-on-failure path so every crash class ends
        # the VM instead of waiting for a human on the other end.
        systemd.enableEmergencyMode = false;

        boot.kernelParams = [
          "console=ttyS0"
          "panic=-1"
          # Explicit systemd-emergency target drops straight into a
          # poweroff if anything in stage-1 fails, instead of sitting
          # on an interactive rescue shell that would hang CI.
          "systemd.crash_shell=no"
          "systemd.crash_reboot=no"
          "systemd.show_status=false"
        ];
        boot.kernelModules = microvmKernelModules;

        # modprobe.d entry so the kernel uses num=16 whether the module
        # is loaded by us explicitly or (as in early-boot init paths)
        # auto-loaded before our systemd unit fires. modprobe in the
        # systemd unit with options alone is racy: if initrd already
        # loaded usbip_vudc with the default num=1, a later
        # `modprobe usbip_vudc num=16` is a silent no-op and leaves
        # the integration suite starved for vudc instances.
        boot.extraModprobeConfig = ''
          options usbip_vudc num=16
        '';

        boot.initrd.availableKernelModules = [
          "virtio_pci" "virtio_blk" "virtio_net" "virtio_console"
          "9p" "9pnet_virtio"
        ];
        boot.supportedFilesystems = [ "9p" "configfs" ];

        # Root is a tmpfs so the VM is stateless between boots — every run
        # starts from the same derivation, which is the hermeticity point.
        fileSystems."/" = {
          device = "tmpfs";
          fsType = "tmpfs";
          options = [ "mode=0755" ];
        };
        fileSystems."/nix/store" = {
          device = "store";
          fsType = "9p";
          options = [ "trans=virtio" "version=9p2000.L" "msize=131072" "cache=loose" "ro" ];
          neededForBoot = true;
        };
        fileSystems."/src" = {
          device = "src";
          fsType = "9p";
          # /src is read-only so the VM cannot mutate host source,
          # cmd.sh, or build artefacts outside the cache carve-out.
          # A separate rw mount on /src/build/cache (below) gives Go
          # a writable space without widening the blast radius to
          # the whole tree.
          options = [ "trans=virtio" "version=9p2000.L" "msize=131072" "ro" ];
        };
        fileSystems."/src/build/cache" = {
          device = "srccache";
          fsType = "9p";
          options = [ "trans=virtio" "version=9p2000.L" "msize=131072" "rw" ];
        };
        fileSystems."/sys/kernel/config" = {
          device = "configfs";
          fsType = "configfs";
        };

        # No networking, no users: the integration suite reaches the
        # daemon via UDS and the vudc loopback. Anything more is future
        # surface for Phase 4 to harden.
        networking.useDHCP = false;
        services.getty.autologinUser = "root";
        users.users.root.hashedPassword = "";

        # Make the Go toolchain and a minimal shell surface available
        # to the test payload inside the VM. /nix/store is bind-mounted
        # from the host, so every path under systemPackages resolves
        # without the VM having to download or install anything.
        environment.systemPackages = with pkgs; [
          bash coreutils util-linux kmod iproute2 procps
          pkgs."go_${builtins.replaceStrings ["."] ["_"] goMinor}"
          git gotools
        ];

        # The test payload: copy /src/build/vm/cmd.sh into tmpfs,
        # then exec the tmpfs copy. That snapshot closes the TOCTOU
        # window even though /src is mounted rw for the sake of Go's
        # cache writes. The unit powers off on success AND failure so
        # every crash class ends the VM instead of holding serial.
        systemd.services.usbip-go-test = {
          wantedBy = [ "multi-user.target" ];
          after = [ "local-fs.target" ];
          serviceConfig = {
            Type = "oneshot";
            StandardOutput = "journal+console";
            StandardError = "journal+console";
          };
          path = with pkgs; [
            bash coreutils util-linux kmod iproute2 procps
            # Go + goimports + git visible to the oneshot so integration
            # payloads can run `go test ./...` against the bind-mounted
            # /src without needing to re-install anything inside the VM.
            pkgs."go_${builtins.replaceStrings ["."] ["_"] goMinor}"
            pkgs.git pkgs.gotools pkgs.gcc pkgs.binutils
          ];
          script = ''
            set -eu
            echo "[vm] loading USBIP kernel modules"
            for m in ${lib.concatStringsSep " " microvmKernelModules}; do
              # usbip_vudc's `num=16` is set via /etc/modprobe.d (see
              # boot.extraModprobeConfig); passing it here too would be
              # redundant and could conflict if the module is already
              # loaded.
              modprobe "$m"
            done
            echo "[vm] snapshotting /src/build/vm/cmd.sh -> /run/vm-cmd.sh"
            install -m 0755 /src/build/vm/cmd.sh /run/vm-cmd.sh
            echo "[vm] running /run/vm-cmd.sh"
            exec bash /run/vm-cmd.sh
          '';
          onSuccess = [ "poweroff.target" ];
          onFailure = [ "poweroff.target" ];
        };
      };

      mkMicrovmRun = pkgs: pkgs.writeShellApplication {
        name = "usbip-go-microvm-run";
        runtimeInputs = [ pkgs.qemu_kvm ];
        text = ''
          set -eu
          kernel="${self.nixosConfigurations.microvm.config.system.build.kernel}/bzImage"
          initrd="${self.nixosConfigurations.microvm.config.system.build.initialRamdisk}/initrd"
          toplevel="${self.nixosConfigurations.microvm.config.system.build.toplevel}"
          src="''${USBIP_GO_VM_SRC:-$PWD}"
          mem="''${USBIP_GO_VM_MEM:-2048}"
          cpus="''${USBIP_GO_VM_CPUS:-2}"
          allow_tcg="''${USBIP_GO_VM_ALLOW_TCG:-}"

          [ -f "$src/build/vm/cmd.sh" ] || {
            echo "microvm-run: missing $src/build/vm/cmd.sh" >&2
            exit 64
          }

          # Default to KVM only so a /dev/kvm misconfiguration (e.g.
          # missing group_add in docker-compose.yml) surfaces as a hard
          # error instead of silently falling back to TCG and wasting
          # tens of seconds per CI run. Opt into TCG explicitly with
          # USBIP_GO_VM_ALLOW_TCG=1.
          accel="kvm"
          if [ -n "$allow_tcg" ]; then
            accel="kvm:tcg"
          else
            [ -w /dev/kvm ] || {
              echo "microvm-run: /dev/kvm is not writable by UID $(id -u); set USBIP_GO_KVM_GID in the environment or export USBIP_GO_VM_ALLOW_TCG=1 to accept the slow TCG fallback." >&2
              exit 69
            }
          fi

          # -cpu max keeps a rich feature set on KVM and TCG alike; -net
          # none drops qemu's default user-mode NIC so the VM matches the
          # "no networking" invariant declared in the NixOS module.
          exec qemu-system-x86_64 \
            -machine "type=q35,accel=$accel" \
            -cpu max \
            -m "$mem" -smp "$cpus" \
            -nographic \
            -net none \
            -kernel "$kernel" \
            -initrd "$initrd" \
            -append "init=$toplevel/init $(cat $toplevel/kernel-params) console=ttyS0 panic=-1 loglevel=4" \
            -virtfs "local,id=store,path=/nix/store,mount_tag=store,security_model=none,readonly=on" \
            -virtfs "local,id=src,path=$src,mount_tag=src,security_model=none,readonly=on" \
            -virtfs "local,id=srccache,path=$src/build/cache,mount_tag=srccache,security_model=none" \
            -device virtio-rng-pci \
            -no-reboot
        '';
      };
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
              pkgs.act           # run GitHub Actions locally (task ci:local)
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

      # integration-test microVM: produced with nixpkgs's standard
      # nixosSystem evaluator so the kernel, initrd, and systemd unit
      # closure are all bit-reproducible and share the host Nix store.
      nixosConfigurations.microvm = nixpkgs.lib.nixosSystem {
        system = "x86_64-linux";
        modules = [ microvmModule ];
      };

      packages.x86_64-linux = {
        microvm-kernel = self.nixosConfigurations.microvm.config.system.build.kernel;
        microvm-initrd = self.nixosConfigurations.microvm.config.system.build.initialRamdisk;
        microvm-toplevel = self.nixosConfigurations.microvm.config.system.build.toplevel;
        microvm-run = mkMicrovmRun (import nixpkgs { system = "x86_64-linux"; });
      };

      formatter = forAllSystems (pkgs: pkgs.nixpkgs-fmt);
    };
}
