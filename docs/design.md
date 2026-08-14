# Transparent GeeSFS Injection into Docker Containers — Design

## Overview

This document describes a mechanism for making an S3-backed filesystem available
inside an otherwise **unmodified** Docker container, immediately before the
container's normal application process starts.

The motivating use case is [GeeSFS](https://github.com/yandex-cloud/geesefs), a
FUSE-over-S3 filesystem packaged separately as a statically-oriented binary in an
OCI image ([geesefs-oci](https://github.com/randomizedcoder/geesefs-oci)). The
application container may be a third-party image (e.g. `pytorch/pytorch`) that is
difficult or undesirable to rebuild, wrap, or otherwise modify.

The desired behavior:

```
docker starts application container
        |
        v
container namespaces / rootfs are prepared
        |
        v
inject GeeSFS tooling and FUSE access
        |
        v
mount S3 bucket at configured path (e.g. /models)
        |
        v
start original application ENTRYPOINT/CMD
```

The important property is that **the application image remains unchanged** — no
rebuild, no entrypoint wrapper, no S3-specific init logic baked into the image.

## The challenge

Docker applications normally assume everything needed at runtime is already in
the image, supplied via bind mounts/volumes, passed through `docker run` options,
or started as a separate container/host service. For this use case all of those
are inconvenient: the image is not ours, and a conventional sidecar's FUSE mount
lives in the sidecar's mount namespace and is not visible to the app container.

What is needed is a host/runtime-level mechanism that can detect that a container
wants an S3 filesystem, make GeeSFS available without touching the image, grant
the minimum privileges for FUSE, create the mount **in the application's mount
namespace**, ensure it is ready before PID 1 starts, and manage the GeeSFS
process lifetime.

## The injection point: the OCI runtime boundary

Docker uses containerd and an OCI runtime (`runc`) underneath. At the OCI layer,
the OCI bundle (rootfs + `config.json`) already exists, which is the opportunity
to augment the runtime spec without modifying the source image. We interpose a
small `runc`-compatible wrapper and delegate to the real `runc`:

```
        Docker → containerd → geesefs-runc → runc → application
                                   |
                                   +-- inspect/modify config.json (policy-driven)
                                   +-- add /dev/fuse + minimal caps
                                   +-- install a createRuntime hook integration
```

For the long-running GeeSFS process, a **host-side supervisor** (`geesefsd`)
enters the container's mount namespace via `setns()` and creates the mount there,
keeping storage infrastructure and credentials outside arbitrary workloads.

## Component map

| # | Component | Doc |
|---|-----------|-----|
| 1 | MicroVM host environment (QEMU + Docker) | [microvm-host.md](./microvm-host.md) |
| 2 | MinIO S3 fixture | [minio-s3.md](./minio-s3.md) |
| 3 | GeeSFS OCI image (the injection payload) | [geesefs-oci-image.md](./geesefs-oci-image.md) |
| 4 | `geesefs-runc` wrapper runtime | [geesefs-runc.md](./geesefs-runc.md) |
| 5 | `geesefsd` host-side supervisor | [geesefsd-supervisor.md](./geesefsd-supervisor.md) |
| 6 | Injection lifecycle | [injection-lifecycle.md](./injection-lifecycle.md) |
| 7 | PyTorch example workload | [pytorch-workload.md](./pytorch-workload.md) |
| 8 | Security considerations | [security.md](./security.md) |
| 9 | Nix-driven integration test | [integration-test.md](./integration-test.md) |

## Roadmap

The path from the current scaffold to a working end-to-end injection is tracked in two docs:

| Doc | Purpose |
|-----|---------|
| [implementation-plan.md](./implementation-plan.md) | Phased steps (Go), per-phase definition-of-done, tests, review gates |
| [implementation-status.md](./implementation-status.md) | Living progress tracker (status table + per-phase checklists) |

## Why a microVM

The whole experiment runs inside a NixOS microVM (astro
[microvm.nix](https://github.com/astro/microvm.nix), QEMU) so it is isolated,
repeatable, and requires nothing of the host but Nix + KVM. Docker and MinIO run
inside the VM; the injection tooling and its host-side supervisor also run there.
The Nix flake is deliberately thin — logic lives in modular `./nix/*.nix` files,
and all shell logic is `writeShellApplication`.

## End-to-end flow

```
1.  Platform requests a Docker container start.
2.  containerd builds the OCI bundle.
3.  Docker invokes geesefs-runc instead of runc.
4.  geesefs-runc reads the injection policy from OCI annotations.
5.  Not requested?  -> exec real runc unchanged.
6.  Requested?      -> add /dev/fuse + caps + mount target + createRuntime hook,
                        then exec real runc.
7.  runc creates the container namespaces.
8.  Before the app starts, the hook contacts geesefsd.
9.  geesefsd starts GeeSFS and mounts S3 into the app's mount namespace.
10. geesefsd verifies the mount is READY.
11. The hook returns success.
12. runc starts the original ENTRYPOINT/CMD unchanged.
13. geesefsd monitors GeeSFS for the container's lifetime.
14. On container exit, geesefsd unmounts and reaps.
```

From the application's perspective it is simply `docker run third-party-image`,
and `/models` contains the configured S3 bucket.

## Status

This repository is a **scaffold**: the docs describe the full design, the Nix
modules stand up the microVM + Docker + MinIO + the injection wiring, and the
integration test encodes the target contract. `geesefs-runc` and `geesefsd` are
currently faithful **stubs** (pass-through + logged intentions); the actual
`config.json` mutation and `setns()` mount are the next implementation pass. See
[integration-test.md](./integration-test.md) for exactly what passes today.
