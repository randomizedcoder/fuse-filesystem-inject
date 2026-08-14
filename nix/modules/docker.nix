# nix/modules/docker.nix
#
# Enables Docker inside the microVM and registers geesefs-runc as an alternative
# OCI runtime named `geesefs`. Containers opt in with `docker run --runtime=geesefs`.
#
# The wrapper behaves identically to plain runc unless a container matches the
# GeeSFS injection policy, so registering it is safe. To make injection apply to
# every container without changing how they are launched, this could instead be
# set as `default-runtime` — left off here so pass-through is the default.
#
# See ../../docs/microvm-host.md and ../../docs/geesefs-runc.md.
#
{
  constants,
  versions,
  geesefs-runc,
}:

{
  config,
  lib,
  pkgs,
  ...
}:

{
  virtualisation.docker = {
    enable = true;
    daemon.settings = {
      runtimes.${constants.runtimeName} = {
        path = "${geesefs-runc}/bin/geesefs-runc";
      };
      # Uncomment to inject into EVERY container transparently:
      # default-runtime = constants.runtimeName;
    };
  };

  # docker CLI + the real runtime on PATH for interactive use in the guest.
  environment.systemPackages = [
    versions.docker
    versions.runc
  ];
}
