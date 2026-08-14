# `geesefsd` — the Host-Side Supervisor

[← design overview](./design.md)

There are two possible homes for the long-running GeeSFS process: inside the
target container, or under a host-side supervisor. This design **prefers the
host-side supervisor**.

## Why host-side

Starting GeeSFS inside the container is simple but injects an infrastructure
daemon into the application's process environment and complicates lifetime
management. Instead, `geesefsd` runs on the VM host and enters the container's
mount namespace only to create the mount:

```
HOST
 |
 +-- geesefsd (systemd service)
       +-- start GeeSFS
       +-- setns() into the target container's MOUNT namespace
       +-- mount S3 -> /models
       +-- verify readiness
       +-- monitor GeeSFS
       +-- unmount + reap when the container exits

                CONTAINER MOUNT NAMESPACE
                         +-- /models  ->  S3 via GeeSFS
```

The application sees an ordinary directory; lifecycle control stays outside the
container. Advantages: the app image and entrypoint are untouched, GeeSFS is not
managed by app PID 1, infrastructure can monitor/restart it, credentials can be
supplied out-of-band, and cleanup is tied to container lifetime.

## Responsibilities

- Locate/pull the requested `geesefs-oci` version (see
  [geesefs-oci-image.md](./geesefs-oci-image.md)).
- Obtain the container's namespace handles; `setns()` into its mount namespace.
- Start GeeSFS and establish the mount; **verify readiness** before returning.
- Supply S3 credentials securely (from a host secret store / workload identity —
  not from the app or GeeSFS image).
- Monitor the GeeSFS process; collect logs/metrics.
- On container termination: terminate GeeSFS, unmount, release handles, remove
  temporary state, retain enough logs to diagnose failures.

The container id is the key linking runtime setup (from
[geesefs-runc.md](./geesefs-runc.md)) with supervisor state.

## Scaffold status

`geesefsd` is the `geesefsd` role of the Go multi-call binary `geesefs-inject`
(role logic in `internal/supervisor`). It has two subcommands:

- `daemon` — runs as the systemd service (`nix/modules/injection.nix`); an idle
  supervisor loop (waits for `SIGTERM`/`SIGINT`) so the unit is `Up`.
- `mount <id> <bucket> <mount> <endpoint>` — invoked by the hook; currently
  **logs the intended `nsenter … geesefs …` + readiness poll and exits non-zero**
  ("not implemented"), so startup does not silently proceed over an empty mount.
  The real `setns()` + mount is Phase 4 — see the
  [implementation plan](./implementation-plan.md).
