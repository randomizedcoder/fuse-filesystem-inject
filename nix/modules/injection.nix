# nix/modules/injection.nix
#
# Runs the host-side GeeSFS supervisor (geesefsd) as a systemd service on the
# VM. geesefs-runc's startContainer hook talks to this daemon, which enters the
# target container's mount namespace and establishes the S3 mount.
#
# (The wrapper runtime itself is installed via the docker daemon `runtimes`
# entry in modules/docker.nix, not here.)
#
# See ../../docs/geesefsd-supervisor.md.
#
{
  constants,
  geesefsd,
}:

{
  config,
  lib,
  pkgs,
  ...
}:

{
  environment.systemPackages = [ geesefsd ];

  systemd.services.geesefsd = {
    description = "GeeSFS host-side mount supervisor";
    after = [
      "docker.service"
      "minio-bucket-bootstrap.service"
    ];
    wantedBy = [ "multi-user.target" ];

    serviceConfig = {
      ExecStart = "${geesefsd}/bin/geesefsd daemon";
      Restart = "on-failure";
      RestartSec = 2;
      StandardOutput = "journal+console";
      StandardError = "journal+console";
    };
  };
}
