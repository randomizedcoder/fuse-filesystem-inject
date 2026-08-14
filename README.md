# fuse-filesystem-inject

Experiments with injecting filesystems into containers.

This repo builds an isolated, repeatable experiment: a NixOS **microVM** running
**Docker** and **MinIO**, into which an S3-backed FUSE filesystem
([GeeSFS](https://github.com/yandex-cloud/geesefs)) is **transparently
injected** — at the OCI runtime boundary — inside an *unmodified* third-party
(PyTorch) container. The S3 bucket appears as an ordinary directory (`/models`)
before the application starts, with no change to the application image.

## Design

Start with the overview, then the per-component docs:

- **[docs/design.md](./docs/design.md)** — overview, injection point, end-to-end flow, component map
- [docs/microvm-host.md](./docs/microvm-host.md) — the NixOS microVM (QEMU + Docker)
- [docs/minio-s3.md](./docs/minio-s3.md) — the MinIO S3 fixture
- [docs/geesefs-payload.md](./docs/geesefs-payload.md) — the static GeeSFS binaries injected into the container
- [docs/geesefs-runc.md](./docs/geesefs-runc.md) — the `geesefs-runc` OCI-config injector
- [docs/geesefsd-supervisor.md](./docs/geesefsd-supervisor.md) — the `geesefsd` host-side supervisor
- [docs/injection-lifecycle.md](./docs/injection-lifecycle.md) — when the mount is established, failure semantics
- [docs/pytorch-workload.md](./docs/pytorch-workload.md) — the unmodified example workload
- [docs/security.md](./docs/security.md) — privilege surface, untrusted input, isolation
- [docs/integration-test.md](./docs/integration-test.md) — the nix-driven end-to-end test
- [docs/implementation-plan.md](./docs/implementation-plan.md) — phased path from scaffold to working (Go), with per-phase definition-of-done
- [docs/implementation-status.md](./docs/implementation-status.md) — living progress tracker

## Quick start

```sh
nix develop                  # dev shell (docker/minio-client/skopeo/jq/...)
nix build .#microvm          # build the microVM runner
nix run   .#demo             # boot the VM + start the injected PyTorch container, then print how to look inside
nix run   .#integration-test # boot VM + prove injection end-to-end
nix flake check              # nixfmt --check + evaluation smoke
```

`nix run .#demo` is the easiest way to see it work: it boots the VM and waits for an
**unmodified** `pytorch/pytorch` container (started with the S3 bucket transparently
mounted at `/models`). Then drop straight inside it:

```sh
nix run .#vm-enter                  # ssh + docker exec into the container, hands you the shell
# inside the container:
ls -la /models                      # files served from S3 over FUSE
python /models/hello.py             # runs FROM the S3 mount
```

Or SSH in yourself: `ssh -p 2222 root@localhost` (password: `demo`, or `nix run .#vm-ssh`),
then `docker exec -it pytorch-demo bash`. Lower-level VM controls:
`nix run .#vm-start` / `.#vm-console` / `.#vm-stop`. SSH here is a loopback-only
throwaway convenience (see [docs/microvm-host.md](./docs/microvm-host.md)).

## Status

**Implemented and passing end-to-end.** `geesefs-runc` mutates the OCI
`config.json` (adds `/dev/fuse` + `CAP_SYS_ADMIN`, RO bind-mounts the static
GeeSFS payload, installs `createRuntime` + `poststop` hooks) and `geesefsd`
performs the real `setns()` mount and cleanup. `nix run .#integration-test`
boots the VM, injects into an unmodified PyTorch container, runs
`python /models/hello.py` from S3 over the FUSE mount, and asserts the isolation
/ escape properties — printing `GEESEFS-INJECTION-TEST: SUCCESS`. See
[docs/integration-test.md](./docs/integration-test.md) for exactly what is
asserted, and [docs/implementation-status.md](./docs/implementation-status.md)
for the per-phase record.

## Layout

```
flake.nix            thin orchestrator; logic lives in ./nix
cmd/geesefs-inject/  the single multi-call Go binary (dispatch on argv[0])
internal/
  contract/          shared contract (annotation keys, fuse device, mount path)
  cli/               runc CLI parsing (+ table tests)
  runc/              geesefs-runc role (OCI-config injector)
  supervisor/        geesefsd role (host-side mount supervisor)
  hook/              geesefs-hook role (createRuntime hook)
  policy/            annotation validation
  protocol/          hook <-> geesefsd unix-socket wire codec
  ocispec/           config.json mutation
nix/
  constants.nix      single source of truth (VM/ports/minio/policy/timeouts)
  versions.nix       pinned package set (incl. go toolchain)
  default.nix        per-system aggregator (packages/devShells/apps/checks)
  geesefs/           the static GeeSFS payload (geesefs + fusermount3)
  lib/mkGoBinary.nix builds geesefs-inject + installs the per-role wrappers
  microvm.nix        the NixOS microVM (-> declaredRunner)
  devshell.nix       dev shell
  scripts.nix        demo / vm-enter / vm-ssh / vm-start / vm-stop / vm-console apps
  scripts/           expect asset for vm-enter (ssh + docker exec into the container)
  modules/           docker, minio (+ hello.py seed), injection, injection-test, demo-container
  tests/             integration-test host driver + expect script
assets/hello.py      PyTorch hello-world seeded into MinIO, run from the FUSE mount
docs/                the design suite + implementation plan/status
```
