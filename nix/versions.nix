# nix/versions.nix
#
# Pinned tool set. Single source of truth for packages — the dev shell, the
# NixOS modules, the stub binaries, the scripts, and the checks all read their
# package attrs from here so they can never drift. Everything is a plain
# passthrough to nixpkgs (the nixpkgs input in flake.lock is the pin), except
# the GeeSFS payload (`geesefsStatic` / `fusermount3Static`), which is built
# locally from nixpkgs in ./geesefs.
#
{ pkgs }:

let
  geesefsPayload = import ./geesefs { inherit pkgs; };
in
{
  # Container runtime that runs inside the VM.
  docker = pkgs.docker;
  # The real OCI runtime the geesefs-runc wrapper delegates to.
  runc = pkgs.runc;

  # S3 fixture + client.
  minio = pkgs.minio;
  minio-client = pkgs.minio-client;

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

  # SSH into the demo VM (host-side `.#demo` / `.#vm-ssh` helpers). sshpass
  # fills the throwaway root password so the demo needs no key setup.
  openssh = pkgs.openssh;
  sshpass = pkgs.sshpass;

  # Image inspection / S3 exercising.
  skopeo = pkgs.skopeo;
  awscli2 = pkgs.awscli2;

  # The GeeSFS injection payload (see ./geesefs): a fully static geesefs binary
  # and its static `fusermount3` helper. geesefs-runc RO bind-mounts both into
  # an opted-in container so the mount tooling is entirely self-contained and
  # never reaches back out to the host closure at runtime.
  geesefsStatic = geesefsPayload.geesefs-static;
  fusermount3Static = geesefsPayload.fusermount3-static;

  # Go toolchain for the geesefs-inject multi-call binary (geesefs-runc /
  # geesefsd / geesefs-hook) and its unit-test / lint checks. The go.mod `go`
  # directive is kept at/below this toolchain so the sandbox never tries to
  # download a toolchain.
  go = pkgs.go;

  # Nix + Go formatting/linting (checked by `nix flake check`).
  nixfmt = pkgs.nixfmt-rfc-style or pkgs.nixfmt;
}
