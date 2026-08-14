# nix/scripts.nix
#
# Operator helper apps (writeShellApplication). These run on the HOST and drive
# the microVM over its process name + console sockets — the same mechanism the
# integration test uses, but for interactive use. See ../docs/microvm-host.md.
#
{
  pkgs,
  lib,
  versions,
  constants,
}:

let
  proc = constants.vm.processName;
  virtio = toString constants.console.virtioPort;
in
{
  # Build the VM runner and boot it in the background.
  vmStart = pkgs.writeShellApplication {
    name = "vm-start";
    runtimeInputs = [
      versions.procps
      versions.coreutils
    ];
    text = ''
      if pgrep -f "process=${proc}" >/dev/null 2>&1; then
        echo "VM already running (process=${proc})"
        exit 0
      fi
      echo "Building VM runner..."
      VM_PATH=$(nix build .#microvm --print-out-paths --no-link)
      [ -n "$VM_PATH" ] || { echo "ERROR: build produced no output path"; exit 1; }
      echo "Starting VM: $VM_PATH/bin/microvm-run"
      "$VM_PATH/bin/microvm-run" &
      echo "VM started (pid $!). Console: nix run .#vm-console"
    '';
  };

  # Stop the VM by its QEMU process name.
  vmStop = pkgs.writeShellApplication {
    name = "vm-stop";
    runtimeInputs = [
      versions.procps
      versions.coreutils
    ];
    text = ''
      if ! pgrep -f "process=${proc}" >/dev/null 2>&1; then
        echo "No VM running (process=${proc})"
        exit 0
      fi
      echo "Sending SIGTERM to VM (process=${proc})..."
      pkill -f "process=${proc}" || true
      sleep 2
      if pgrep -f "process=${proc}" >/dev/null 2>&1; then
        echo "Still running, sending SIGKILL..."
        pkill -9 -f "process=${proc}" || true
      fi
      echo "Stopped."
    '';
  };

  # Attach to the interactive virtio console.
  vmConsole = pkgs.writeShellApplication {
    name = "vm-console";
    runtimeInputs = [
      versions.socat
      versions.netcat-gnu
    ];
    text = ''
      PORT=${virtio}
      if ! nc -z 127.0.0.1 "$PORT" 2>/dev/null; then
        echo "ERROR: console port $PORT not listening — is the VM up? (nix run .#vm-start)"
        exit 1
      fi
      echo "Attaching to VM console on 127.0.0.1:$PORT (Ctrl+C to detach)"
      exec socat -,raw,echo=0 TCP:127.0.0.1:"$PORT"
    '';
  };

  # Print the exact docker recipe the in-guest test uses. Run these INSIDE the
  # VM (nix run .#vm-console) to reproduce the injection by hand.
  demo = pkgs.writeShellApplication {
    name = "demo";
    runtimeInputs = [ versions.coreutils ];
    text = ''
      cat <<'EOF'
      Transparent GeeSFS injection — run these INSIDE the VM console:

        docker run --rm \
          --runtime=${constants.runtimeName} \
          --label ${constants.annotations.enabled}=true \
          --label ${constants.annotations.bucket}=${constants.minio.bucket} \
          --label ${constants.annotations.mount}=${constants.mountPath} \
          --label ${constants.annotations.endpoint}=${constants.minio.endpoint} \
          ${constants.pytorchImage} \
          bash -lc 'grep " ${constants.mountPath} " /proc/mounts; df -h ${constants.mountPath}; python ${constants.mountPath}/hello.py'

      The image is UNMODIFIED. geesefs-runc adds /dev/fuse + caps and geesefsd
      mounts the '${constants.minio.bucket}' S3 bucket at ${constants.mountPath}
      before the container's entrypoint runs.
      EOF
    '';
  };
}
