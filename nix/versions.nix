# nix/versions.nix
#
# Pinned tool set. Single source of truth for packages — the dev shell, the
# NixOS modules, the stub binaries, the scripts, and the checks all read their
# package attrs from here so they can never drift. Everything is a plain
# passthrough to nixpkgs (the nixpkgs input in flake.lock is the pin), except
# `geesefs` / `oci-geesefs`, which come from the geesefs-oci flake input.
#
{ pkgs, geesefsPkgs }:

{
  # Container runtime that runs inside the VM.
  docker = pkgs.docker;
  # The real OCI runtime the geesefs-runc wrapper delegates to.
  runc = pkgs.runc;

  # S3 fixture + client.
  minio = pkgs.minio;
  minio-client = pkgs.minio-client;

  # FUSE userspace: geesefs shells out to `fusermount3` at mount time.
  fuse3 = pkgs.fuse3;

  # Namespace entry for the host-side supervisor (nsenter/unshare live here).
  util-linux = pkgs.util-linux;

  # Basic userland.
  coreutils = pkgs.coreutils;
  procps = pkgs.procps; # pgrep/pkill for VM lifecycle
  getent = pkgs.getent; # mc shells out to getent
  curl = pkgs.curl;
  jq = pkgs.jq; # OCI config.json inspection/mutation

  # VM console driving.
  netcat-gnu = pkgs.netcat-gnu;
  socat = pkgs.socat;
  expect = pkgs.expect;

  # Image inspection / S3 exercising.
  skopeo = pkgs.skopeo;
  awscli2 = pkgs.awscli2;

  # From the geesefs-oci flake input: the FUSE-over-S3 binary and its OCI image.
  geesefs = geesefsPkgs.geesefs;
  oci-geesefs = geesefsPkgs.oci-geesefs;

  # Go toolchain for the geesefs-inject multi-call binary (geesefs-runc /
  # geesefsd / geesefs-hook) and its unit-test / lint checks. The go.mod `go`
  # directive is kept at/below this toolchain so the sandbox never tries to
  # download a toolchain.
  go = pkgs.go;

  # Nix + Go formatting/linting (checked by `nix flake check`).
  nixfmt = pkgs.nixfmt-rfc-style or pkgs.nixfmt;
}
