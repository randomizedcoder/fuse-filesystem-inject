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
  sshPort = toString constants.ssh.hostPort;
  sshPass = constants.ssh.rootPassword;
  container = constants.demo.containerName;

  # A batch (non-interactive) ssh into the demo VM. Host keys are ignored and
  # never persisted because the VM regenerates them every boot. The throwaway
  # password is filled by sshpass so callers need no key setup.
  sshCmd = "sshpass -p ${sshPass} ssh -p ${sshPort} -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR root@127.0.0.1";

  # Expect asset driving the interactive "ssh in + docker exec into the
  # container" flow for `.#vm-enter` (sibling idiom to tests/scripts/*.exp).
  enterScript = ./scripts/vm-enter-container.exp;
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

  # SSH into the running demo VM (password-filled, host-key checks disabled —
  # this is a disposable loopback fixture). Any extra args are passed straight
  # to ssh, e.g. `nix run .#vm-ssh -- docker exec -it ${container} bash`.
  vmSsh = pkgs.writeShellApplication {
    name = "vm-ssh";
    runtimeInputs = [
      versions.openssh
      versions.sshpass
      versions.netcat-gnu
    ];
    text = ''
      if ! nc -z 127.0.0.1 ${sshPort} 2>/dev/null; then
        echo "ERROR: ssh port ${sshPort} not listening — is the VM up? (nix run .#demo)"
        exit 1
      fi
      exec ${sshCmd} "$@"
    '';
  };

  # SSH into the VM, docker-exec into the always-on container, and hand the
  # interactive shell to the user — the one-command "drop me inside the demo"
  # path. Driven by an expect script (same mechanism as the integration test).
  vmEnter = pkgs.writeShellApplication {
    name = "vm-enter";
    runtimeInputs = [
      versions.expect
      versions.openssh
      versions.sshpass
      versions.netcat-gnu
    ];
    text = ''
      if ! nc -z 127.0.0.1 ${sshPort} 2>/dev/null; then
        echo "ERROR: ssh port ${sshPort} not listening — is the VM up? (nix run .#demo)"
        exit 1
      fi
      exec expect ${enterScript} ${sshPort} ${sshPass} ${container}
    '';
  };

  # One-command demo: boot the VM if needed, wait until SSH + the always-on
  # PyTorch container are ready, then print exactly how to look inside. The
  # image is UNMODIFIED — geesefs-runc adds /dev/fuse + caps and geesefsd mounts
  # the S3 bucket at ${constants.mountPath} before the container's entrypoint.
  demo = pkgs.writeShellApplication {
    name = "demo";
    runtimeInputs = [
      versions.procps
      versions.coreutils
      versions.openssh
      versions.sshpass
      versions.netcat-gnu
    ];
    text = ''
      # -- 1. boot the VM if it is not already running -----------------------
      if pgrep -f "process=${proc}" >/dev/null 2>&1; then
        echo "VM already running (process=${proc})."
      else
        echo "Building VM runner..."
        VM_PATH=$(nix build .#microvm --print-out-paths --no-link)
        [ -n "$VM_PATH" ] || { echo "ERROR: build produced no output path"; exit 1; }
        echo "Starting VM: $VM_PATH/bin/microvm-run"
        "$VM_PATH/bin/microvm-run" &
        echo "VM started (pid $!)."
      fi

      # -- 2. wait for SSH to come up ----------------------------------------
      echo -n "Waiting for SSH on 127.0.0.1:${sshPort} "
      for _ in $(seq 1 120); do
        if nc -z 127.0.0.1 ${sshPort} 2>/dev/null; then break; fi
        echo -n "."; sleep 2
      done
      echo
      nc -z 127.0.0.1 ${sshPort} 2>/dev/null || { echo "ERROR: SSH never came up"; exit 1; }

      # -- 3. wait for the always-on PyTorch container -----------------------
      # First boot pulls a multi-GB image, so give it a generous window.
      echo -n "Waiting for the '${container}' container (first boot pulls PyTorch, up to ${toString (builtins.div constants.timeouts.demoReady 60)} min) "
      for _ in $(seq 1 ${toString (builtins.div constants.timeouts.demoReady 2)}); do
        state=$(${sshCmd} "docker inspect -f '{{.State.Running}}' ${container} 2>/dev/null" 2>/dev/null || true)
        if [ "$state" = "true" ]; then break; fi
        echo -n "."; sleep 2
      done
      echo
      state=$(${sshCmd} "docker inspect -f '{{.State.Running}}' ${container} 2>/dev/null" 2>/dev/null || true)
      [ "$state" = "true" ] || { echo "ERROR: '${container}' is not running yet — check 'journalctl -u pytorch-demo' in the VM"; exit 1; }

      cat <<EOF

      ============================================================
      Demo is ready. The '${container}' container is an UNMODIFIED
      ${constants.pytorchImage} image with the '${constants.minio.bucket}' S3
      bucket transparently mounted at ${constants.mountPath}.

      Drop straight into the container (easiest):
        nix run .#vm-enter
        # then, inside:  ls -la ${constants.mountPath} ; python ${constants.mountPath}/hello.py

      Or SSH into the VM yourself:
        ssh -p ${sshPort} root@localhost        # password: ${sshPass}   (or: nix run .#vm-ssh)
        docker exec -it ${container} bash        # then browse ${constants.mountPath}

      Or run one command from the host:
        nix run .#vm-ssh -- docker exec -it ${container} ls -la ${constants.mountPath}

      Stop the VM when done:  nix run .#vm-stop
      ============================================================
      EOF
    '';
  };
}
