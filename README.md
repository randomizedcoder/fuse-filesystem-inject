# fuse-filesystem-inject

Experiments with injecting filesystems into containers.

This repo builds an isolated, repeatable experiment: a NixOS **microVM** running
**Docker** and **MinIO**, into which an S3-backed FUSE filesystem
([GeeSFS](https://github.com/randomizedcoder/geesefs-oci)) is **transparently
injected** — at the OCI runtime boundary — inside an *unmodified* third-party
(PyTorch) container. The S3 bucket appears as an ordinary directory (`/models`)
before the application starts, with no change to the application image.

## Design

Start with the overview, then the per-component docs:

- **[docs/design.md](./docs/design.md)** — overview, injection point, end-to-end flow, component map
- [docs/microvm-host.md](./docs/microvm-host.md) — the NixOS microVM (QEMU + Docker)
- [docs/minio-s3.md](./docs/minio-s3.md) — the MinIO S3 fixture
- [docs/geesefs-oci-image.md](./docs/geesefs-oci-image.md) — the GeeSFS OCI payload
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
nix run   .#vm-start         # boot the VM
nix run   .#vm-console       # attach to its console
nix run   .#integration-test # boot VM + prove injection end-to-end
nix flake check              # nixfmt --check + evaluation smoke
```

## Status

This is a **scaffold**. The docs describe the full design; the Nix modules stand
up the microVM + Docker + MinIO + the injection wiring; the integration test
encodes the target contract. `geesefs-runc` and `geesefsd` are currently faithful
**stubs** (pass-through + logged intentions) — the actual `config.json` mutation
and `setns()` mount are the next implementation pass. See
[docs/integration-test.md](./docs/integration-test.md) for exactly what passes
today, and [docs/implementation-plan.md](./docs/implementation-plan.md) /
[docs/implementation-status.md](./docs/implementation-status.md) for the phased
path forward and current progress.

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
  policy/            annotation validation (Phase 2)
  ocispec/           config.json mutation (Phase 3)
nix/
  constants.nix      single source of truth (VM/ports/minio/policy/timeouts)
  versions.nix       pinned package set (incl. go toolchain)
  default.nix        per-system aggregator (packages/devShells/apps/checks)
  lib/mkGoBinary.nix builds geesefs-inject + installs the per-role wrappers
  microvm.nix        the NixOS microVM (-> declaredRunner)
  devshell.nix       dev shell
  scripts.nix        vm-start / vm-stop / vm-console / demo apps
  modules/           docker, minio (+ hello.py seed), injection, injection-test
  tests/             integration-test host driver + expect script
assets/hello.py      PyTorch hello-world seeded into MinIO, run from the FUSE mount
docs/                the design suite + implementation plan/status
```
