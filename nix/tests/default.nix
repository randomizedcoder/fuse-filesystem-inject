# nix/tests/default.nix
#
# The nix-driven integration test. Assembles the host-side driver that boots
# the microVM and confirms the in-guest geesefs-injection-test.service printed
# its success sentinel. Modeled on xdp2/nix/microvms.
#
# See ../../docs/integration-test.md.
#
{
  pkgs,
  lib,
  versions,
  constants,
}:

let
  scriptsDir = ./scripts;
  driver = import ./lib.nix {
    inherit
      pkgs
      lib
      versions
      constants
      scriptsDir
      ;
  };
in
{
  inherit (driver) integrationTest;
}
