# nix/lib/mkGoBinary.nix
#
# Builds the single multi-call `geesefs-inject` Go binary and installs it under
# the three role names the rest of the system invokes it by: geesefs-runc,
# geesefsd, and geesefs-hook. Each role is a thin makeWrapper that
#
#   (a) sets argv[0] to the role name — the binary dispatches on argv[0]
#       basename, busybox-style (see cmd/geesefs-inject/main.go), and
#   (b) puts the runtime tools that role shells out to on PATH (the real runc
#       for geesefs-runc; geesefs + fuse3 + nsenter for the supervisor/hook).
#
# The Go module is deliberately stdlib-only, so `vendorHash = null` (no vendor
# tree, fully hermetic). That is also why there is no goModules.nix here, unlike
# the xtcp2 pattern this is modeled on — there are no dependencies to vendor.
#
# Returns the wrapped multi-call package. `passthru.raw` is the unwrapped
# binary, used by the Go checks (they need the module source, not the wrapper).
#
{
  pkgs,
  lib,
  versions,
}:

{ src }:

let
  buildGoModule = pkgs.buildGoModule.override { inherit (versions) go; };

  raw = buildGoModule {
    pname = "geesefs-inject";
    version = "0.0.0-nix";
    inherit src;

    # stdlib-only module → nothing to vendor.
    vendorHash = null;

    subPackages = [ "cmd/geesefs-inject" ];
    env.CGO_ENABLED = "0";
    ldflags = [
      "-s"
      "-w"
    ];

    # Unit tests run as their own Nix checks, not in the build (mirrors xtcp2).
    doCheck = false;

    meta = {
      description = "GeeSFS transparent-injection multi-call binary (geesefs-runc / geesefsd / geesefs-hook)";
      mainProgram = "geesefs-inject";
      platforms = lib.platforms.linux;
    };
  };

  # PATH closures per role. geesefs-runc only needs the real runc; the
  # supervisor and hook shell out to geesefs, fusermount3, and nsenter.
  runcPath = lib.makeBinPath [ versions.runc ];
  daemonPath = lib.makeBinPath [
    versions.geesefs
    versions.fuse3
    versions.util-linux
    versions.coreutils
  ];
in
pkgs.runCommand "geesefs-inject"
  {
    nativeBuildInputs = [ pkgs.makeWrapper ];
    passthru = { inherit raw; };
    inherit (raw) meta;
  }
  ''
    mkdir -p "$out/bin"

    makeWrapper ${raw}/bin/geesefs-inject "$out/bin/geesefs-runc" \
      --argv0 geesefs-runc \
      --set GEESEFS_HOOK_PATH "$out/bin/geesefs-hook" \
      --prefix PATH : ${runcPath}

    makeWrapper ${raw}/bin/geesefs-inject "$out/bin/geesefsd" \
      --argv0 geesefsd \
      --prefix PATH : ${daemonPath}

    makeWrapper ${raw}/bin/geesefs-inject "$out/bin/geesefs-hook" \
      --argv0 geesefs-hook \
      --prefix PATH : ${daemonPath}
  ''
