# nix/modules/minio.nix
#
# A single-node MinIO server inside the microVM as the local S3 endpoint, plus
# a oneshot that pre-creates the bucket AND seeds the PyTorch hello-world script
# into it. Modeled on xtcp2's minio-bucket-bootstrap.nix.
#
# Storage is a small tmpfs — it only needs to hold one tiny script; ~10 MiB is
# plenty. Credentials are static test fixtures committed to /nix/store; this is
# not a production deployment.
#
# The bootstrap is its own oneshot (rather than an ExecStartPre) so it can retry
# while MinIO warms up and later units get a clean `After=` edge on a ready unit.
#
# See ../../docs/minio-s3.md.
#
{
  constants,
  versions,
  helloScript,
}:

{
  config,
  lib,
  pkgs,
  ...
}:

let
  m = constants.minio;

  credentialsFile = pkgs.writeText "minio-credentials" ''
    MINIO_ROOT_USER=${m.accessKey}
    MINIO_ROOT_PASSWORD=${m.secretKey}
  '';

  apiUrl = "http://127.0.0.1:${toString m.apiPort}";

  bootstrapScript = pkgs.writeShellScript "minio-bucket-bootstrap" ''
    set -eu
    export MC_CONFIG_DIR=/var/lib/minio-bucket-bootstrap/.mc
    mkdir -p "$MC_CONFIG_DIR"

    MC=${pkgs.minio-client}/bin/mc
    CURL=${pkgs.curl}/bin/curl

    # /minio/health/live returns 200 only once the API socket is bound and the
    # disk pool is formatted — a better readiness gate than `systemctl is-active`.
    for _ in $(${pkgs.coreutils}/bin/seq 1 60); do
      if "$CURL" --silent --fail --max-time 2 "${apiUrl}/minio/health/live" >/dev/null 2>&1; then
        break
      fi
      sleep 1
    done

    if ! "$CURL" --silent --fail --max-time 2 "${apiUrl}/minio/health/live" >/dev/null 2>&1; then
      echo "minio-bucket-bootstrap: /health/live never returned 200 after 60s" >&2
      exit 1
    fi

    "$MC" alias set local "${apiUrl}" ${m.accessKey} ${m.secretKey} >/dev/null

    # Idempotent bucket create + seed the hello-world payload used by the test.
    "$MC" mb --ignore-existing "local/${m.bucket}"
    "$MC" cp "${helloScript}" "local/${m.bucket}/hello.py"

    echo "minio-bucket-bootstrap: bucket ${m.bucket} ready with hello.py"
  '';
in
{
  # tmpfs for MinIO data (services.minio dataDir defaults under /var/lib/minio).
  fileSystems."/var/lib/minio" = {
    device = "tmpfs";
    fsType = "tmpfs";
    options = [
      "size=${m.dataSize}"
      "mode=0755"
    ];
  };

  services.minio = {
    enable = true;
    rootCredentialsFile = "${credentialsFile}";
    region = m.region;
    browser = false;
    listenAddress = "0.0.0.0:${toString m.apiPort}";
    consoleAddress = "0.0.0.0:${toString m.consolePort}";
    dataDir = [ "/var/lib/minio/data" ];
  };

  systemd.services.minio-bucket-bootstrap = {
    description = "Create MinIO bucket and seed hello.py for the injection test";
    after = [ "minio.service" ];
    requires = [ "minio.service" ];
    wantedBy = [ "multi-user.target" ];

    # mc shells out to `getent`, which lives in its own package in nixpkgs.
    path = [
      pkgs.getent
      pkgs.coreutils
    ];

    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      ExecStart = "${bootstrapScript}";
      StateDirectory = "minio-bucket-bootstrap";
      StateDirectoryMode = "0700";
      StandardOutput = "journal+console";
      StandardError = "journal+console";
    };
  };
}
