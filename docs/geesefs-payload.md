# The GeeSFS Payload — Static Binaries Injected Into the Container

[← design overview](./design.md)

The mount tooling has to run *inside* the target container's mount namespace,
but the container is an unmodified third-party image (e.g. `pytorch/pytorch`)
that knows nothing about GeeSFS. The whole point of the exercise is that the
injection is self-contained: the tooling must never reach back out to the host's
`/nix` store at runtime. So we build the two binaries GeeSFS needs as **fully
static ELFs** and bind-mount them into the container.

## What GeeSFS needs

GeeSFS does not call `mount(2)` directly. Like its goofys / jacobsa-fuse
lineage, it opens `/dev/fuse` and then execs **`fusermount3`** by name to
perform the FUSE mount. So the payload is two binaries:

| Binary | Built from | Why static |
|--------|-----------|------------|
| `geesefs` | `pkgs.geesefs` rebuilt with `CGO_ENABLED=0` | pure-Go, but nixpkgs links it against glibc by default; CGO-off makes it a self-contained static ELF |
| `fusermount3` | `pkgs.pkgsStatic.fuse3` (musl) | the helper geesefs execs; the dynamic build pulls in a glibc/libfuse `/nix` closure the container lacks |

Both are built locally under [`nix/geesefs/`](../nix/geesefs) — this repo no
longer depends on any external GeeSFS OCI image. `nix build .#geesefs-static`
and `.#fusermount3-static` produce them; the `geesefs-static-smoke` flake check
asserts both actually report as `statically linked` (a dynamic binary would fail
to exec in a container without the host closure).

## How they reach the container

`geesefs-runc` (see [geesefs-runc.md](./geesefs-runc.md)) adds two **read-only
bind mounts** to the OCI spec, sourced from the static store paths and hardened
with `nosuid,nodev`:

```
/nix/store/…-geesefs/bin/geesefs            -> /.geesefs/bin/geesefs      (ro,bind)
/nix/store/…-fusermount3-static/bin/…       -> /.geesefs/bin/fusermount3  (ro,bind)
```

The Nix wrapper passes the two store paths to `geesefs-runc` via
`GEESEFS_STATIC_BIN` / `GEESEFS_FUSERMOUNT3_BIN` (see
[`nix/lib/mkGoBinary.nix`](../nix/lib/mkGoBinary.nix)). Passing them with
`makeWrapper --set` also pins them into the wrapper's runtime closure, so the
binaries are guaranteed present in the VM store even though only their string
paths appear in the injector.

## How they run

`geesefsd` launches GeeSFS with
`nsenter -t <pid> -m -- /.geesefs/bin/geesefs …` — entering only the mount
namespace — and sets `PATH=/.geesefs/bin` in the child environment so geesefs
finds the bind-mounted `fusermount3` (never a host copy). Because both binaries
are static, they have no `/nix` closure to satisfy inside the container. See
[geesefsd-supervisor.md](./geesefsd-supervisor.md) and
[injection-lifecycle.md](./injection-lifecycle.md).
