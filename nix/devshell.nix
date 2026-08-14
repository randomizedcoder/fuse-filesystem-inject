# nix/devshell.nix
#
# Developer environment. `nix develop` lands here. Everything needed to build
# and drive the microVM, poke at MinIO, and inspect the GeeSFS OCI image is on
# PATH. A `fuse-inject-help` function prints the cheat-sheet on entry.
#
{
  pkgs,
  lib,
  versions,
  constants,
}:

pkgs.mkShell {
  name = "fuse-inject-dev";

  packages = [
    versions.minio-client
    versions.skopeo
    versions.jq
    versions.awscli2
    versions.util-linux
    versions.netcat-gnu
    versions.socat
    versions.expect
    versions.curl
    versions.geesefsStatic
  ];

  shellHook = ''
    fuse-inject-help() {
      cat <<'EOF'

    fuse-filesystem-inject dev shell
    ================================
    MicroVM:
      nix build .#microvm             Build the VM runner
      nix run   .#vm-start            Boot the VM (background)
      nix run   .#vm-console          Attach to the VM console (hvc0)
      nix run   .#vm-stop             Stop the VM

    Injection payload:
      nix build .#geesefs-static      Static geesefs binary (bind-mounted into containers)
      nix build .#fusermount3-static  Static fusermount3 helper (bind-mounted alongside)
      nix build .#geesefs-runc        The OCI-config injector
      nix build .#geesefsd            The host-side supervisor

    End-to-end proof:
      nix run   .#integration-test    Boot VM, inject into PyTorch, assert mount + isolation
      nix run   .#demo                Show the docker run recipe used inside the VM

    Nix:
      nix flake check                 nixfmt --check + evaluation smoke
      nixfmt **/*.nix                 Format the nix sources

    EOF
    }

    fuse-inject-help
  '';
}
