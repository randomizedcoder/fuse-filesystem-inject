# GeeSFS OCI Image — the Injection Payload

[← design overview](./design.md)

GeeSFS is packaged independently as an OCI image by
[geesefs-oci](https://github.com/randomizedcoder/geesefs-oci), consumed here as a
flake input. This decouples the GeeSFS artifact's release lifecycle from any
application image, and provides a clean distribution format for the binary and
its supporting files.

## What the image contains

`geesefs-oci` builds via `dockerTools.streamLayeredImage` on a scratch base. Three
variants ship along one axis — how much userland they carry:

| Package attr | Contents |
|--------------|----------|
| `oci-geesefs-min` | `geesefs` + `fuse3` + CA certs |
| `oci-geesefs` | + `fuse`, `coreutils`, `util-linux` (ls, df, mount, umount) |
| `oci-geesefs-shell` | + `busybox`, `bash` (debugging) |

Binaries land on `/bin`, CA bundle at `/etc/ssl/certs/ca-certificates.crt`,
entrypoint `/bin/geesefs`. This repo consumes `oci-geesefs` (the standard
variant) as `versions.oci-geesefs`.

## Two gotchas that shape the design

1. **`result` is a script, not a tarball.** `streamLayeredImage` produces an
   executable that streams the image: `./result | docker load`, or
   `./result | skopeo inspect docker-archive:/dev/stdin`. Nothing large is kept
   in the store.

2. **`fusermount3` is dynamically linked.** `geesefs` itself is pure-Go and
   effectively self-contained, but the `fusermount3` helper (from `fuse3`) links
   against glibc/libfuse in `/nix/store`. So grafting GeeSFS into another image
   means carrying its **`/nix` closure**, not just the binaries — unless the
   target already provides its own `fusermount3`. This is the central constraint
   on "how do we supply the binary into the container".

## Supplying the binary into the target container

Options, in rough order of transparency:

- **Host-side extraction (preferred here):** a host service keeps the selected
  GeeSFS version unpacked in a host-managed directory; `geesefsd` executes it
  while entering the container's namespaces. Most transparent to Docker callers.
  See [geesefsd-supervisor.md](./geesefsd-supervisor.md).
- **OCI image mount:** mount the contents of `geesefs-oci` read-only into the
  container (modern Docker image mounts). The app image itself stays unchanged.

Either way the application image is never rebuilt. See
[injection-lifecycle.md](./injection-lifecycle.md) for where in the container
lifecycle this happens.
