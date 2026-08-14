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
# Quick references:
#   nix develop                     # dev shell (docker/minio-client/skopeo/jq/...)
#   nix build .#microvm             # build the microVM runner
#   nix run   .#vm-start            # boot the microVM
#   nix run   .#integration-test    # boot VM + prove injection end-to-end
#   nix flake check                 # nixfmt --check + evaluation smoke
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

    # The GeeSFS OCI payload. Local path for iteration; swap for the GitHub URL
    # (github:randomizedcoder/geesefs-oci) to pin a released artifact.
    geesefs-oci = {
      url = "path:/home/das/Downloads/geesefs-oci";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      microvm,
      geesefs-oci,
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
              system
              microvm
              nixpkgs
              geesefs-oci
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
