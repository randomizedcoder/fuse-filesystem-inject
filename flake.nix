#
# flake.nix — fuse-filesystem-inject
#
# Thin orchestrator. Every concern lives under ./nix/ and is wired up here.
# See ./nix/default.nix for the per-system aggregator, and ./docs/design.md
# for the design of the experiment this flake builds.
#
# The experiment: a NixOS microVM running Docker + MinIO, into which an
# S3-backed FUSE filesystem (GeeSFS) is transparently injected — at the OCI
# runtime boundary — inside an unmodified third-party (PyTorch) container.
#
# Targets (see ./nix/default.nix for the definitions):
#
#   Dev shell:
#     nix develop                       # docker/minio-client/skopeo/jq/awscli/...
#
#   Packages (nix build .#<name>):
#     microvm            # the NixOS microVM runner (Docker + MinIO + injection); default
#     geesefs-inject     # the multi-call Go binary (all three role wrappers)
#     geesefs-runc       # role wrapper: the OCI-config injector runtime
#     geesefsd           # role wrapper: the host-side mount supervisor
#     geesefs-static     # the static geesefs binary bind-mounted into containers
#     fusermount3-static # the static fusermount3 helper bind-mounted alongside
#
#   Apps (nix run .#<name>):
#     integration-test   # boot VM + prove injection end-to-end (needs KVM); default
#     demo               # boot VM + wait for the always-on injected PyTorch container, then print how to SSH in
#     vm-enter           # ssh in + docker exec straight into the container (interactive shell at /models)
#     vm-ssh             # ssh into the running demo VM (append args, e.g. `-- docker exec -it pytorch-demo bash`)
#     vm-start           # boot the microVM in the background
#     vm-console         # attach to the VM console (hvc0)
#     vm-stop            # stop the microVM
#
#   Checks (nix flake check — runs all of these):
#     nixfmt-check       # all *.nix formatted
#     gofmt / go-vet / go-test   # Go format, vet, and unit tests
#     contract-parity    # internal/contract vs nix/constants.nix agree
#     geesefs-static-smoke       # payload binaries run + are statically linked
#     geesefs-runc-smoke / geesefsd-smoke   # wrapper --geesefs-selftest
#
{
  description = "fuse-filesystem-inject — transparent GeeSFS (FUSE over S3) injection into Docker containers, in a NixOS microVM";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";

    microvm = {
      url = "github:astro/microvm.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      microvm,
    }:
    flake-utils.lib.eachSystem
      [
        "x86_64-linux"
        "aarch64-linux"
      ]
      (
        system:
        let
          pkgs = import nixpkgs {
            inherit system;
            # MinIO ships in nixpkgs marked insecure. It is only ever used here
            # as a local test fixture inside the microVM, never exposed beyond it.
            config.permittedInsecurePackages = [
              "minio-2025-10-15T17-29-55Z"
            ];
          };
          lib = nixpkgs.lib;

          aggregator = import ./nix {
            inherit
              pkgs
              lib
              microvm
              nixpkgs
              ;
            src = ./.;
          };
        in
        {
          inherit (aggregator)
            packages
            devShells
            apps
            checks
            ;
        }
      );
}
