# nix/constants.nix
#
# Single source of truth for the experiment. Every other module reads its
# knobs from here so VM resources, ports, MinIO credentials, the S3 mount
# path, the injection policy annotation keys, and timeouts can never drift.
#
# This is plain data (a `rec {}` attrset) — no packages, no `pkgs` argument.
#
rec {
  # ==========================================================================
  # MicroVM
  # ==========================================================================

  vm = {
    hostname = "fuse-inject";
    # QEMU `-name ...,process=<processName>` so `pgrep -f` can find the VM.
    processName = "fuse-inject";

    mem = 4096; # MiB — room for docker + a real container image
    vcpu = 4;

    # Writable data volume mounted at /var/lib (docker + minio state).
    dataImage = "fuse-inject-data.img";
    dataSizeMB = 8192;
  };

  # Console sockets QEMU exposes on the host loopback (NOT guest networking).
  # The integration-test host driver drives the guest over these.
  console = {
    device = "ttyS0";
    serialPort = 24700; # boot messages
    virtioPort = 24701; # hvc0, interactive/driver console
  };

  # ==========================================================================
  # MinIO — local S3 fixture inside the VM
  # ==========================================================================

  minio = {
    accessKey = "fuseinjecttest";
    secretKey = "fuseinjecttestsecret";
    region = "us-east-1";
    apiPort = 9000;
    consolePort = 9001;
    bucket = "models";
    # ~10 MiB is plenty to prove functionality; keep the tmpfs tiny.
    dataSize = "64M";
    # In-VM endpoint used by both the bootstrap and the injected GeeSFS.
    endpoint = "http://127.0.0.1:9000";
  };

  # ==========================================================================
  # Injection policy / GeeSFS
  # ==========================================================================

  # Where the S3 bucket is mounted inside the target container.
  mountPath = "/models";

  # Docker runtime name the wrapper registers as. `docker run --runtime=geesefs`.
  runtimeName = "geesefs";

  # Docker labels surface to the OCI runtime as annotations. These are the keys
  # the geesefs-runc wrapper inspects. Treat all values as UNTRUSTED input.
  annotations = {
    enabled = "geesefs.enabled";
    bucket = "geesefs.bucket";
    mount = "geesefs.mount";
    endpoint = "geesefs.endpoint";
  };

  # ==========================================================================
  # Workload under test
  # ==========================================================================

  # An UNMODIFIED upstream third-party image — the whole point is that we do
  # not rebuild it. Override for a smaller CPU-only tag when iterating.
  pytorchImage = "pytorch/pytorch:latest";

  # ==========================================================================
  # Isolation test fixture
  # ==========================================================================

  # A marker file that exists ONLY on the VM host rootfs. The integration test
  # asserts it is NOT reachable from inside the container (proving the mount
  # and rootfs are confined to the container's mount namespace).
  vmHostMarkerPath = "/etc/vm-host-secret";
  vmHostMarkerContents = "this-file-lives-on-the-vm-host-not-in-any-container";

  # ==========================================================================
  # Timeouts (seconds)
  # ==========================================================================

  pollInterval = 1;

  timeouts = {
    build = 1800; # first build pulls kernel/systemd/docker/minio
    processStart = 5;
    serialReady = 45;
    virtioReady = 60;
    serviceReady = 600; # docker pull of a large image can be slow
    command = 15;
    shutdown = 45;
  };
}
