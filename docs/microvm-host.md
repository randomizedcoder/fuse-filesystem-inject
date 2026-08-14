# MicroVM Host Environment

[← design overview](./design.md)

The experiment runs entirely inside a NixOS **microVM** so it is isolated and
repeatable. The VM provides the Docker daemon, the MinIO S3 fixture, and the
injection tooling. Nothing is required of the outer host except Nix + KVM.

## Stack

- **Hypervisor:** QEMU via astro [microvm.nix](https://github.com/astro/microvm.nix).
- **Guest:** minimal NixOS, autologin root, fixed hostname (`fuse-inject`) so the
  test's console prompt regex is deterministic.
- **Rootfs / store:** host `/nix/store` shared read-only over 9p; a writable disk
  image mounted at `/var/lib` holds Docker + MinIO state.
- **Networking:** QEMU user-mode (SLiRP). Enough for `docker pull` (outbound NAT)
  and for the in-guest test to reach MinIO on `127.0.0.1`. No host bridge/TAP
  setup is required for a single VM. Host access to the MinIO console can be
  added later with QEMU `forwardPorts`.
- **Consoles:** serial (boot messages) and virtio (`hvc0`) are each exposed on a
  host-loopback TCP socket. These are QEMU character devices, **not** guest
  networking — the integration-test driver connects to them with `nc`/`expect`.

## FUSE

`boot.kernelModules = [ "fuse" ]` ensures `/dev/fuse` exists in the guest. The
`geesefs-runc` wrapper then exposes that device (and the device-cgroup rule) to a
matched container so GeeSFS can mount inside it. This nesting — FUSE inside
Docker inside a microVM — is a known requirement to validate; see
[security.md](./security.md).

## Files

| File | Role |
|------|------|
| `nix/microvm.nix` | The `nixosSystem` → `declaredRunner`; QEMU/console/9p/volume/network config. |
| `nix/modules/docker.nix` | Enables Docker; registers `geesefs-runc` as the `geesefs` runtime. |
| `nix/constants.nix` | Single source of truth: VM resources, console ports, etc. |
| `nix/scripts.nix` | `demo`, `vm-enter`, `vm-ssh`, `vm-start`, `vm-stop`, `vm-console` host apps. |
| `nix/scripts/vm-enter-container.exp` | expect script behind `vm-enter` (ssh → docker exec → `interact`). |

## Running it

```sh
nix run .#demo          # boot + start the injected PyTorch container, then print how to look inside
nix run .#vm-enter      # ssh in + docker exec into the container, then hand over the shell (expect-driven)
nix run .#vm-ssh        # ssh in (append args, e.g. `-- docker exec -it pytorch-demo bash`)
nix run .#vm-start      # build + boot in the background
nix run .#vm-console    # attach to hvc0 (Ctrl+C to detach)
nix run .#vm-stop       # SIGTERM/SIGKILL by QEMU process name
```

`vm-enter` reuses the same `expect` mechanism as the integration test's console
driver (`nix/tests/scripts/vm-verify-service.exp`): it spawns `ssh`, runs
`docker exec -it <container> bash -l` as the login command, then `interact`s so
the user lands inside the container (at `/models`) and Ctrl-D returns to the host.

### SSH access

The VM enables `sshd` and QEMU forwards host `127.0.0.1:2222` to the guest
(SLiRP `hostfwd` on the user-mode netdev — no host bridge/TAP), so any machine
that can run the flake can `ssh -p 2222 root@localhost` (password: `demo`). This
is a **loopback-only, disposable demo convenience** — the same posture as
autologin-root and the insecure test MinIO. The root password and host-key
checks are deliberately relaxed for `.#demo`/`.#vm-ssh`; never reuse the password
or expose the VM beyond loopback. The port and password live in
`nix/constants.nix` (`ssh = { hostPort; guestPort; rootPassword; }`).

Docker is registered with an extra runtime named `geesefs`
(`virtualisation.docker.daemon.settings.runtimes`). Containers opt in with
`docker run --runtime=geesefs`; setting it as `default-runtime` would make
injection apply to every container transparently. See
[geesefs-runc.md](./geesefs-runc.md).
