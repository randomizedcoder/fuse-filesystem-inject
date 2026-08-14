# nix/microvm.nix
#
# The microVM definition: a NixOS system (astro/microvm.nix, QEMU) that runs
# Docker + MinIO and carries the injection tooling. Returns the runner package
# (`declaredRunner`), whose `bin/microvm-run` boots the VM.
#
# The host drives the guest over two TCP-exposed consoles (serial + virtio),
# exactly as the xdp2 test infrastructure does. Guest networking is QEMU
# user-mode (SLiRP): enough for `docker pull` and for the in-guest test to
# reach MinIO on 127.0.0.1, without any host bridge/TAP setup.
#
# See ../docs/microvm-host.md.
#
{
  pkgs,
  lib,
  microvm,
  nixpkgs,
  constants,
  versions,
  geesefs-runc,
  geesefsd,
  helloScript,
}:

let
  dockerModule = import ./modules/docker.nix { inherit constants versions geesefs-runc; };
  minioModule = import ./modules/minio.nix { inherit constants versions helloScript; };
  injectionModule = import ./modules/injection.nix { inherit constants geesefsd; };
  injectionTestModule = import ./modules/injection-test.nix { inherit constants versions; };
in
(nixpkgs.lib.nixosSystem {
  modules = [
    microvm.nixosModules.microvm

    # Pin the guest to the target-system pkgs (carries permittedInsecurePackages
    # for MinIO from the flake).
    { nixpkgs.pkgs = pkgs; }

    dockerModule
    minioModule
    injectionModule
    injectionTestModule

    # ------------------------------------------------------------------
    # Base guest configuration
    # ------------------------------------------------------------------
    (
      { config, pkgs, ... }:
      {
        system.stateVersion = "26.05";
        networking.hostName = constants.vm.hostname;

        # Trim the closure: none of this is needed for a headless test VM.
        documentation.enable = lib.mkDefault false;
        documentation.man.enable = lib.mkDefault false;
        documentation.doc.enable = lib.mkDefault false;
        documentation.info.enable = lib.mkDefault false;
        documentation.nixos.enable = lib.mkDefault false;

        microvm = {
          hypervisor = "qemu";
          mem = constants.vm.mem;
          vcpu = constants.vm.vcpu;
          # cpu = null => microvm.nix adds -enable-kvm -cpu host on Linux.

          # Read-only host /nix/store over 9p.
          shares = [
            {
              source = "/nix/store";
              mountPoint = "/nix/.ro-store";
              tag = "ro-store";
              proto = "9p";
            }
          ];

          # Writable data disk for docker + minio state.
          volumes = [
            {
              image = constants.vm.dataImage;
              mountPoint = "/var/lib";
              size = constants.vm.dataSizeMB;
            }
          ];

          # QEMU user networking (NAT to host): outbound for docker pull, and
          # 127.0.0.1 reachability for the in-guest MinIO.
          interfaces = [
            {
              type = "user";
              id = "eth0";
              mac = "52:54:00:12:34:56";
            }
          ];

          qemu = {
            serialConsole = false;
            extraArgs = [
              "-name"
              "${constants.vm.hostname},process=${constants.vm.processName}"

              # Serial console (boot messages) on TCP.
              "-serial"
              "tcp:127.0.0.1:${toString constants.console.serialPort},server,nowait"

              # Virtio console (hvc0) on TCP — the driver/interactive console.
              "-device"
              "virtio-serial-pci"
              "-chardev"
              "socket,id=virtcon,port=${toString constants.console.virtioPort},host=127.0.0.1,server=on,wait=off"
              "-device"
              "virtconsole,chardev=virtcon"
            ];
          };
        };

        # Console + boot params. ttyS0 targets x86_64; aarch64 hosts would use
        # ttyAMA0 (this experiment targets x86_64-linux).
        boot.kernelParams = [
          "console=${constants.console.device},115200"
          "console=hvc0"
        ];
        boot.initrd.availableKernelModules = [
          "9p"
          "9pnet"
          "9pnet_virtio"
          "virtio_pci"
          "virtio_console"
        ];

        # FUSE must be available in the guest so /dev/fuse exists for injection.
        boot.kernelModules = [ "fuse" ];

        # Autologin + fixed hostname so the console prompt regex is deterministic
        # for the expect-driven integration test.
        services.getty.autologinUser = "root";
        systemd.enableEmergencyMode = false;

        # A marker that lives ONLY on the VM host rootfs. The integration test
        # asserts it is unreachable from inside the container.
        environment.etc."vm-host-secret".text = constants.vmHostMarkerContents;

        environment.systemPackages = [
          versions.coreutils
          versions.util-linux
          versions.jq
        ];
      }
    )
  ];
}).config.microvm.declaredRunner
