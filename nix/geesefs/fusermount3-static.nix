# nix/geesefs/fusermount3-static.nix
#
# A fully static `fusermount3`, the second half of the injected payload.
#
# GeeSFS does not call mount(2) directly; like its goofys/jacobsa-fuse lineage
# it execs `fusermount3` by name to open /dev/fuse and perform the FUSE mount.
# That helper therefore has to be present *inside* the container alongside
# geesefs. nixpkgs' `pkgsStatic.fuse3` builds it against musl as a static ELF
# with no shared-library dependencies, so it can be bind-mounted into a
# container whose mount namespace lacks the host closure.
#
# We copy just the single binary into a minimal derivation so the bind-mount
# source is unambiguous and its closure is only the (dependency-free) file.
#
{ pkgs }:

pkgs.runCommand "fusermount3-static"
  {
    meta = {
      description = "Statically linked fusermount3 for GeeSFS injection";
      platforms = pkgs.lib.platforms.linux;
    };
  }
  ''
    mkdir -p "$out/bin"
    cp ${pkgs.pkgsStatic.fuse3.bin}/bin/fusermount3 "$out/bin/fusermount3"
  ''
