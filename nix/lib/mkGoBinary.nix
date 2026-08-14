# nix/lib/mkGoBinary.nix
#
# Builds the single multi-call `geesefs-inject` Go binary and installs it under
# the three role names the rest of the system invokes it by: geesefs-runc,
# geesefsd, and geesefs-hook. Each role is a thin makeWrapper that
#
#   (a) sets argv[0] to the role name — the binary dispatches on argv[0]
#       basename, busybox-style (see cmd/geesefs-inject/main.go), and
#   (b) puts the runtime tools that role shells out to on PATH (the real runc
#       for geesefs-runc; nsenter for the supervisor — the hook only talks to
#       the socket), and
#   (c) for geesefs-runc, records the host store paths of the static GeeSFS
#       payload (geesefs + fusermount3) it RO bind-mounts into containers.
#       Passing them via --set both hands them to the injector and pins them
#       into the wrapper's runtime closure, so they reach the VM store.
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
  # supervisor shells out to nsenter (util-linux) to enter container namespaces.
  # The hook needs nothing external — it only speaks to geesefsd over the socket.
  # geesefs itself is no longer on any host PATH: geesefsd execs the static copy
  # bind-mounted inside the container.
  runcPath = lib.makeBinPath [ versions.runc ];
  supervisorPath = lib.makeBinPath [ versions.util-linux ];

  geesefsBin = "${versions.geesefsStatic}/bin/geesefs";
  fusermount3Bin = "${versions.fusermount3Static}/bin/fusermount3";
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
      --set GEESEFS_STATIC_BIN ${geesefsBin} \
      --set GEESEFS_FUSERMOUNT3_BIN ${fusermount3Bin} \
      --prefix PATH : ${runcPath}

    makeWrapper ${raw}/bin/geesefs-inject "$out/bin/geesefsd" \
      --argv0 geesefsd \
      --prefix PATH : ${supervisorPath}

    makeWrapper ${raw}/bin/geesefs-inject "$out/bin/geesefs-hook" \
      --argv0 geesefs-hook
  ''
