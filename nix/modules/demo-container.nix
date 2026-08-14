# nix/modules/demo-container.nix
#
# The always-on demo container. Unlike the injection-test (a --rm oneshot that
# asserts and exits), this launches the UNMODIFIED upstream PyTorch image
# DETACHED with `sleep infinity` and the geesefs.* injection annotations, so the
# S3 FUSE mount at /models is live and the container stays up. A user who SSHes
# into the VM (see ../microvm.nix forwardPorts) can then:
#
#   docker exec -it <containerName> bash
#   ls -la /models && python /models/hello.py
#
# The image is never modified and carries no credentials — geesefs-runc injects
# /dev/fuse + caps and geesefsd (whose systemd env holds the S3 creds) performs
# the mount, exactly as in the automated test.
#
# See ../../docs/pytorch-workload.md and ../scripts.nix (the `demo` app).
#
{
  constants,
  versions,
}:

{
  config,
  lib,
  pkgs,
  ...
}:

let
  a = constants.annotations;
  m = constants.minio;
  name = constants.demo.containerName;
in
{
  systemd.services.pytorch-demo = {
    description = "Always-on demo: unmodified PyTorch container with GeeSFS injected at ${constants.mountPath}";
    # Start only once the image has been pulled and asserted by the test, so the
    # two never race on the (multi-GB) `docker pull`.
    after = [
      "docker.service"
      "minio-bucket-bootstrap.service"
      "geesefsd.service"
      "geesefs-injection-test.service"
    ];
    requires = [
      "docker.service"
      "minio-bucket-bootstrap.service"
    ];
    wantedBy = [ "multi-user.target" ];

    path = [
      versions.docker
      versions.coreutils
    ];

    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      StandardOutput = "journal+console";
      StandardError = "journal+console";
    };

    script = ''
      set -eu
      log() { echo "pytorch-demo: $*"; }

      log "waiting for docker daemon"
      for _ in $(seq 1 60); do docker info >/dev/null 2>&1 && break; sleep 1; done
      docker info >/dev/null 2>&1 || { echo "pytorch-demo: docker not ready" >&2; exit 1; }

      # Idempotent: replace any container left over from a previous boot.
      docker rm -f ${name} >/dev/null 2>&1 || true

      log "pulling ${constants.pytorchImage} (unmodified third-party image)"
      docker pull "${constants.pytorchImage}"

      log "launching detached container '${name}' with --runtime=${constants.runtimeName} + geesefs.* annotations"
      docker run -d --name ${name} \
        --runtime=${constants.runtimeName} \
        --annotation ${a.enabled}=true \
        --annotation ${a.bucket}=${m.bucket} \
        --annotation ${a.mount}=${constants.mountPath} \
        --annotation ${a.endpoint}=${m.endpoint} \
        "${constants.pytorchImage}" \
        sleep infinity

      log "ready — docker exec -it ${name} bash; then ls ${constants.mountPath}"
    '';
  };
}
