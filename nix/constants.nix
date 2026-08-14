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

    # Writable data volume mounted at /var/lib (docker + minio state). Must hold
    # the extracted PyTorch image, which is large (CUDA + cuDNN unpack to ~9 GB),
    # so give docker's overlayfs generous headroom. The image is a sparse file on
    # the host, so it only consumes what is actually written.
    dataImage = "fuse-inject-data.img";
    dataSizeMB = 24576; # 24 GiB
  };

  # Console sockets QEMU exposes on the host loopback (NOT guest networking).
  # The integration-test host driver drives the guest over these.
  console = {
    device = "ttyS0";
    serialPort = 24700; # boot messages
    virtioPort = 24701; # hvc0, interactive/driver console
  };

  # SSH access to the demo VM. QEMU user-mode networking forwards
  # host 127.0.0.1:<hostPort> to the guest sshd on <guestPort> (no host
  # bridge/TAP needed). The root password is a throwaway convenience for this
  # loopback-only demo fixture — the same posture as autologin-root and the
  # insecure test MinIO. Never reuse it or expose the VM beyond loopback.
  ssh = {
    hostPort = 2222;
    guestPort = 22;
    rootPassword = "demo";
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

  # OCI annotation keys the geesefs-runc wrapper inspects. Set them per container
  # with `docker run --annotation <key>=<value>` (Docker's --label populates
  # Docker's own metadata, NOT the OCI config.json annotations the runtime sees,
  # so it does not work here). Treat all values as UNTRUSTED input.
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

  # The always-on demo container. `nix run .#demo` boots the VM and this guest
  # service launches the unmodified PyTorch image DETACHED (sleep infinity) with
  # the injection annotations, so `/models` (the live S3 FUSE mount) is already up
  # when a user SSHes in and `docker exec`s into it.
  demo = {
    containerName = "pytorch-demo";
  };

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
    # `.#demo` waits this long for the always-on container. On a first boot this
    # covers the injection-test's full PyTorch pull+extract AND the demo
    # container start (both serialized); later boots reuse the cached image.
    demoReady = 1500;
  };
}
