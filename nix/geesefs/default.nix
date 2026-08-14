# nix/geesefs/default.nix
#
# The GeeSFS injection payload, built locally from nixpkgs (this repo no longer
# depends on the external geesefs-oci flake — the point of the exercise is a
# self-contained container, so we build the static binaries here). Both are
# fully static ELFs with no /nix/store closure, so geesefs-runc can RO
# bind-mount them into an unmodified container and geesefsd can exec them there.
#
{ pkgs }:

{
  geesefs-static = import ./geesefs-static.nix { inherit pkgs; };
  fusermount3-static = import ./fusermount3-static.nix { inherit pkgs; };
}
