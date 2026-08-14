# Nix-Driven Integration Test

[← design overview](./design.md)

The integration test is the end-to-end proof: it boots the microVM, injects
GeeSFS into an unmodified PyTorch container, and asserts the mount works and is
properly isolated. It is modeled on the `xdp2` microVM test infrastructure.

## Architecture

Correctness logic lives **in the guest**, as a `Type=oneshot` systemd unit that
prints a success sentinel — the deterministic pattern from `xdp2`. A thin host
driver boots the VM and greps the sentinel out of the journal over the console.

```
host: nix run .#integration-test          guest: geesefs-injection-test.service
  Phase 0  build .#microvm                   1. wait: docker, /dev/fuse, minio+hello.py
  Phase 1  boot microvm-run &                2. docker pull <unmodified pytorch>
  Phase 2  wait serial + virtio consoles     3. docker run --runtime=geesefs + labels
  Phase 3  expect: stream journal,           4. assert /models is a fuse mount (df/mounts)
           match sentinel  <----------.      5. python /models/hello.py  (from S3!)
  Phase 4  poweroff + wait exit        `----- 6. isolation asserts; print SUCCESS sentinel
```

## What the guest unit asserts

`nix/modules/injection-test.nix`:

1. **Readiness** — docker daemon up, `/dev/fuse` present, MinIO bucket contains
   the seeded `hello.py`.
2. **Injection** — runs the **unmodified** `pytorch/pytorch` image with
   `--runtime=geesefs` and the `geesefs.*` labels.
3. **Mount live** — `/models` appears in `/proc/mounts` as a `fuse` filesystem;
   `df -h /models` and `ls /models` succeed.
4. **Execute from S3** — `python /models/hello.py` runs from the FUSE mount and
   prints `FUSE-INJECT-PYTORCH-HELLO-OK`.
5. **Isolation** — the VM-host marker `/etc/vm-host-secret` is not visible;
   `../../` traversal from `/models` cannot reach the VM host `/etc`.
6. **Cleanup** — after the container exits, no `/models` mount is left on the host.

On success it prints `GEESEFS-INJECTION-TEST: SUCCESS`. The in-container script is
base64-transported into the container (no bind mounts, no quoting games) so the
image launch stays clean.

## Running it

```sh
nix run .#integration-test
```

## Scaffold status — what passes today

`geesefs-runc` and `geesefsd` are stubs this pass, so **injection does not
actually happen yet**: the container starts without the mount, and the test
**fails at step 3/4** with a clear message. This is expected and intended — the
test encodes the *target* contract. For that reason it is exposed as an app, not
wired into `nix flake check`'s must-pass gates.

What **does** pass now: `nix flake check` (evaluation + `nixfmt --check` + a
`geesefs --version` smoke + a `geesefs-runc --geesefs-selftest` smoke), building
the VM runner, and booting the VM through Phase 2. Implementing the
`config.json` mutation ([geesefs-runc.md](./geesefs-runc.md)) and the `setns()`
mount ([geesefsd-supervisor.md](./geesefsd-supervisor.md)) is what flips steps
3–6 green.
