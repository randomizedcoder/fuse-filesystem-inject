# nix/tests/lib.nix
#
# Host-side driver for the integration test. All correctness logic lives in the
# guest (modules/injection-test.nix); this only:
#   Phase 0  build the VM runner
#   Phase 1  boot it in the background, poll for the QEMU process
#   Phase 2  wait for the serial + virtio consoles to listen
#   Phase 3  drive the virtio console with expect: stream the injection-test
#            service journal and match the success sentinel (or its failure)
#   Phase 4  poweroff and wait for the process to exit (trap cleanup on EXIT)
#
# Pattern lifted from xdp2/nix/microvms/lib.nix + constants.nix.
#
{
  pkgs,
  lib,
  versions,
  constants,
  scriptsDir,
}:

let
  proc = constants.vm.processName;
  hostname = constants.vm.hostname;
  serialPort = toString constants.console.serialPort;
  virtioPort = toString constants.console.virtioPort;
  t = constants.timeouts;
in
{
  integrationTest = pkgs.writeShellApplication {
    name = "integration-test";
    runtimeInputs = [
      versions.netcat-gnu
      versions.procps
      versions.coreutils
      versions.expect
    ];
    text = ''
      VM_PROCESS="process=${proc}"
      SERIAL_PORT=${serialPort}
      VIRTIO_PORT=${virtioPort}
      VM_HOSTNAME="${hostname}"
      POLL_INTERVAL=${toString constants.pollInterval}
      BUILD_TIMEOUT=${toString t.build}
      PROCESS_TIMEOUT=${toString t.processStart}
      SERIAL_TIMEOUT=${toString t.serialReady}
      VIRTIO_TIMEOUT=${toString t.virtioReady}
      SERVICE_TIMEOUT=${toString t.serviceReady}
      SHUTDOWN_TIMEOUT=${toString t.shutdown}
      EXPECT_SCRIPT="${scriptsDir}/vm-verify-service.exp"

      RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
      pass() { echo -e "  ''${GREEN}PASS: $1''${NC}"; }
      fail() { echo -e "  ''${RED}FAIL: $1''${NC}"; exit 1; }
      info() { echo -e "  ''${YELLOW}INFO: $1''${NC}"; }

      cleanup() {
        echo ""
        info "Cleaning up VM..."
        if [ -n "''${VM_PID:-}" ] && kill -0 "$VM_PID" 2>/dev/null; then
          kill "$VM_PID" 2>/dev/null || true
          wait "$VM_PID" 2>/dev/null || true
        fi
      }
      trap cleanup EXIT

      echo "========================================"
      echo "  fuse-filesystem-inject integration test"
      echo "========================================"

      # Phase 0: build
      echo "--- Phase 0: Build VM (timeout: $BUILD_TIMEOUT s) ---"
      if ! timeout "$BUILD_TIMEOUT" nix build .#microvm --print-out-paths --no-link; then
        fail "VM build failed or timed out"
      fi
      VM_PATH=$(nix build .#microvm --print-out-paths --no-link 2>/dev/null)
      [ -n "$VM_PATH" ] || fail "build succeeded but produced no output path"
      pass "VM built: $VM_PATH"

      if nc -z 127.0.0.1 "$SERIAL_PORT" 2>/dev/null; then
        fail "port $SERIAL_PORT already in use (another VM running?)"
      fi

      # Phase 1: start
      echo "--- Phase 1: Start VM (timeout: $PROCESS_TIMEOUT s) ---"
      "$VM_PATH/bin/microvm-run" &
      VM_PID=$!
      WAITED=0
      while ! pgrep -f "$VM_PROCESS" >/dev/null 2>&1; do
        sleep "$POLL_INTERVAL"; WAITED=$((WAITED + POLL_INTERVAL))
        [ "$WAITED" -ge "$PROCESS_TIMEOUT" ] && fail "VM process not found after $PROCESS_TIMEOUT s"
        kill -0 "$VM_PID" 2>/dev/null || fail "VM process died immediately"
      done
      pass "VM process running"

      # Phase 2: consoles
      echo "--- Phase 2: Wait for consoles ---"
      WAITED=0
      while ! nc -z 127.0.0.1 "$SERIAL_PORT" 2>/dev/null; do
        sleep "$POLL_INTERVAL"; WAITED=$((WAITED + POLL_INTERVAL))
        [ "$WAITED" -ge "$SERIAL_TIMEOUT" ] && fail "serial console not up after $SERIAL_TIMEOUT s"
        kill -0 "$VM_PID" 2>/dev/null || fail "VM died while waiting for serial"
      done
      pass "serial console up (port $SERIAL_PORT)"
      WAITED=0
      while ! nc -z 127.0.0.1 "$VIRTIO_PORT" 2>/dev/null; do
        sleep "$POLL_INTERVAL"; WAITED=$((WAITED + POLL_INTERVAL))
        [ "$WAITED" -ge "$VIRTIO_TIMEOUT" ] && fail "virtio console not up after $VIRTIO_TIMEOUT s"
      done
      pass "virtio console up (port $VIRTIO_PORT)"

      # Phase 3: verify the in-guest injection test service via the journal
      echo "--- Phase 3: Verify injection test (timeout: $SERVICE_TIMEOUT s) ---"
      if expect "$EXPECT_SCRIPT" "$VIRTIO_PORT" "$VM_HOSTNAME" "$SERVICE_TIMEOUT" "$POLL_INTERVAL"; then
        pass "geesefs-injection-test reported SUCCESS"
        RESULT=0
      else
        info "geesefs-injection-test did not report success"
        info "(expected in the scaffold pass until geesefs-runc/geesefsd are implemented)"
        RESULT=1
      fi

      # Phase 4: shutdown
      echo "--- Phase 4: Shutdown ---"
      echo "poweroff" | timeout "$SHUTDOWN_TIMEOUT" nc 127.0.0.1 "$VIRTIO_PORT" 2>/dev/null || true
      WAITED=0
      while kill -0 "$VM_PID" 2>/dev/null; do
        sleep "$POLL_INTERVAL"; WAITED=$((WAITED + POLL_INTERVAL))
        if [ "$WAITED" -ge "$SHUTDOWN_TIMEOUT" ]; then
          info "VM still up after $SHUTDOWN_TIMEOUT s, terminating"
          kill "$VM_PID" 2>/dev/null || true; break
        fi
      done
      pass "VM stopped"

      echo "========================================"
      if [ "$RESULT" -eq 0 ]; then
        echo -e "  ''${GREEN}Integration test PASSED''${NC}"
      else
        echo -e "  ''${YELLOW}Integration test did not pass (scaffold: injection not yet implemented)''${NC}"
      fi
      echo "========================================"
      exit "$RESULT"
    '';
  };
}
