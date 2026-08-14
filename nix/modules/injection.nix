# nix/modules/injection.nix
#
# Runs the host-side GeeSFS supervisor (geesefsd) as a systemd service on the
# VM. geesefs-runc installs a createRuntime hook (geesefs-hook) into matched
# containers; the hook talks to this daemon over its unix socket, and the daemon
# enters the target container's mount namespace and establishes the S3 mount.
#
# S3 credentials are supplied here, out-of-band, via the daemon's environment —
# never baked into the application image or the GeeSFS OCI image. GeeSFS (and
# the nsenter'd child) inherit them from the unit; they never appear in argv.
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

    # S3 credentials for GeeSFS, supplied out-of-band (not in any image). GeeSFS
    # reads the standard AWS environment variables; the nsenter'd child inherits
    # them, so they never appear on a command line.
    environment = {
      AWS_ACCESS_KEY_ID = constants.minio.accessKey;
      AWS_SECRET_ACCESS_KEY = constants.minio.secretKey;
      AWS_REGION = constants.minio.region;
      AWS_DEFAULT_REGION = constants.minio.region;
    };

    serviceConfig = {
      ExecStart = "${geesefsd}/bin/geesefsd daemon";
      Restart = "on-failure";
      RestartSec = 2;
      StandardOutput = "journal+console";
      StandardError = "journal+console";
    };
  };
}
