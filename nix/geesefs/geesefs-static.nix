# nix/geesefs/geesefs-static.nix
#
# A fully static GeeSFS binary, built for injection *into* an unmodified
# third-party container. The whole point of the experiment is that the mount
# tooling never reaches back out to the host's /nix store at runtime, so the
# binary must carry everything it needs.
#
# geesefs is pure Go, but nixpkgs builds it dynamically (linked against glibc)
# by default. Rebuilding with CGO disabled makes it a self-contained static ELF
# with no shared-library or /nix/store closure — safe to bind-mount into a
# container whose mount namespace has neither. `-s -w` strips it.
#
{ pkgs }:

pkgs.geesefs.overrideAttrs (old: {
  env = (old.env or { }) // {
    CGO_ENABLED = "0";
  };
  ldflags = (old.ldflags or [ ]) ++ [
    "-s"
    "-w"
  ];
})
