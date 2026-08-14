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

## Status — it passes end-to-end

The injection is fully implemented and the test **passes end-to-end**:
`geesefs-runc` mutates `config.json` ([geesefs-runc.md](./geesefs-runc.md)) and
`geesefsd` performs the real `setns()` mount
([geesefsd-supervisor.md](./geesefsd-supervisor.md)), so the container gets a
live `fuse.geesefs` mount at `/models` and `python /models/hello.py` runs from
S3. The driver prints `GEESEFS-INJECTION-TEST: SUCCESS`.

It stays an **app** rather than a `nix flake check` gate because it needs KVM to
boot the VM and network access to pull the PyTorch image — neither is available
inside the `nix build` sandbox. The pure logic (policy validation, `config.json`
mutation, mount-argv / readiness parsing, the socket codec) *is* gated by
`nix flake check` via the Go unit checks, alongside `nixfmt --check` and the
static-payload / selftest smokes.
