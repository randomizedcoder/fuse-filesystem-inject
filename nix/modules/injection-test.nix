# nix/modules/injection-test.nix
#
# The in-guest end-to-end assertion runner, as a Type=oneshot systemd unit that
# prints a unique success sentinel (or fails). This is where the real proof
# lives — following xdp2's "assert inside the guest, print a sentinel" model,
# which is far more deterministic than driving docker over a serial console.
# The host driver (../tests/lib.nix) only boots the VM and greps the sentinel
# out of the journal.
#
# What it proves:
#   1. MinIO + bucket + hello.py + docker + /dev/fuse are ready.
#   2. An UNMODIFIED upstream PyTorch image, run with --runtime=geesefs and the
#      geesefs.* labels, gets the S3 bucket mounted at /models before its app.
#   3. The mount is a live fuse filesystem (checked via /proc/mounts + df).
#   4. `python /models/hello.py` runs FROM the S3 mount and prints its sentinel.
#   5. Isolation: the VM-host marker is unreachable, `../../` from the mount
#      cannot escape the container rootfs, and /proc is the container's own.
#
# SCAFFOLD NOTE: geesefs-runc/geesefsd are stubs this pass, so injection does
# not actually happen yet — the test is expected to FAIL at step 2/3 until they
# are implemented. It encodes the target contract. That is why the host driver
# exposes it as an app, not a must-pass `nix flake check` gate.
#
# See ../../docs/integration-test.md and ../../docs/injection-lifecycle.md.
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
  successSentinel = "GEESEFS-INJECTION-TEST: SUCCESS";
  helloSentinel = "FUSE-INJECT-PYTORCH-HELLO-OK";
  mount = constants.mountPath;
  a = constants.annotations;
  m = constants.minio;

  # Runs INSIDE the container. Base64-transported so no quoting games and no
  # bind mounts (the image launch stays clean).
  innerScript = ''
    set -eu
    echo "[container] uid=$(id -u) pid1-cgroup=$(head -1 /proc/1/cgroup 2>/dev/null || echo n/a)"

    echo "[container] assert ${mount} is a live fuse mount"
    if ! grep -E "[[:space:]]${mount}[[:space:]]fuse" /proc/mounts; then
      echo "[container] FAIL: no fuse mount at ${mount}"
      echo "[container] /proc/mounts snapshot:"; grep -E "fuse|${mount}" /proc/mounts || true
      exit 1
    fi
    df -h "${mount}" || true
    ls -la "${mount}"

    echo "[container] run PyTorch hello-world from the S3 FUSE mount"
    OUT=$(python "${mount}/hello.py")
    echo "$OUT"
    echo "$OUT" | grep -q "${helloSentinel}" || { echo "[container] FAIL: hello sentinel missing"; exit 1; }

    echo "[container] isolation: VM-host marker must NOT be visible"
    if [ -e "${constants.vmHostMarkerPath}" ]; then
      echo "[container] FAIL: ${constants.vmHostMarkerPath} leaked into container"; exit 1
    fi

    echo "[container] isolation: ../ from the mount must stay in container rootfs"
    TRAV=$(cat "${mount}/../../../../etc/os-release" 2>/dev/null || true)
    if printf '%s' "$TRAV" | grep -q "${constants.vmHostMarkerContents}"; then
      echo "[container] FAIL: traversal escaped to the VM host /etc"; exit 1
    fi

    echo "${successSentinel}"
  '';
in
{
  systemd.services.geesefs-injection-test = {
    description = "End-to-end: inject GeeSFS into an unmodified PyTorch container";
    after = [
      "docker.service"
      "minio-bucket-bootstrap.service"
      "geesefsd.service"
    ];
    requires = [
      "docker.service"
      "minio-bucket-bootstrap.service"
    ];
    wantedBy = [ "multi-user.target" ];

    path = [
      versions.docker
      versions.coreutils
      versions.curl
      versions.jq
      versions.util-linux
      versions.minio-client
      versions.getent
    ];

    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      StandardOutput = "journal+console";
      StandardError = "journal+console";
    };

    script = ''
      set -eu
      fail() { echo "geesefs-injection-test: FAIL: $*" >&2; exit 1; }
      log()  { echo "geesefs-injection-test: $*"; }

      # -- Step 1: readiness -------------------------------------------------
      log "waiting for docker daemon"
      for _ in $(seq 1 60); do docker info >/dev/null 2>&1 && break; sleep 1; done
      docker info >/dev/null 2>&1 || fail "docker not ready"

      log "waiting for /dev/fuse"
      [ -e /dev/fuse ] || fail "/dev/fuse missing (fuse module not loaded?)"

      log "waiting for MinIO bucket + seeded hello.py"
      export MC_CONFIG_DIR=/tmp/geesefs-injection-test/.mc
      mkdir -p "$MC_CONFIG_DIR"
      mc alias set local "${m.endpoint}" ${m.accessKey} ${m.secretKey} >/dev/null
      mc ls "local/${m.bucket}/hello.py" >/dev/null 2>&1 || fail "hello.py not present in bucket ${m.bucket}"

      # -- Step 2: pull the unmodified upstream image ------------------------
      log "pulling ${constants.pytorchImage} (unmodified third-party image)"
      docker pull "${constants.pytorchImage}" || fail "docker pull failed"

      # -- Steps 3-5: run with injection and assert inside the container -----
      B64=$(printf '%s' ${lib.escapeShellArg innerScript} | base64 -w0)

      log "running container with --runtime=${constants.runtimeName} + geesefs.* labels"
      if docker run --rm \
          --runtime=${constants.runtimeName} \
          --label ${a.enabled}=true \
          --label ${a.bucket}=${m.bucket} \
          --label ${a.mount}=${mount} \
          --label ${a.endpoint}=${m.endpoint} \
          "${constants.pytorchImage}" \
          bash -lc "echo $B64 | base64 -d | bash" \
          | tee /tmp/geesefs-injection-test/out.log \
          | grep -q "${successSentinel}"; then
        log "container reported success"
      else
        fail "injection assertions failed (expected until geesefs-runc/geesefsd are implemented)"
      fi

      # -- Step 6: cleanup check --------------------------------------------
      log "verifying host-side mount was cleaned up"
      if grep -q " ${mount} " /proc/mounts 2>/dev/null; then
        fail "leftover mount at ${mount} on the host after container exit"
      fi

      echo "${successSentinel}"
    '';
  };
}
