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

- Obtain the container's namespace handles; `setns()` into its mount namespace.
- Start GeeSFS from the **static payload bind-mounted into the container** (see
  [geesefs-payload.md](./geesefs-payload.md)) and establish the mount; **verify
  readiness** before returning.
- Supply S3 credentials securely (from a host secret store / workload identity —
  not from the app or GeeSFS image).
- Monitor the GeeSFS process; collect logs/metrics.
- On container termination: terminate GeeSFS, unmount, release handles, remove
  temporary state, retain enough logs to diagnose failures.

The container id is the key linking runtime setup (from
[geesefs-runc.md](./geesefs-runc.md)) with supervisor state.

## Implementation status

`geesefsd` is the `geesefsd` role of the Go multi-call binary `geesefs-inject`
(role logic in `internal/supervisor`). It runs as the systemd service
(`nix/modules/injection.nix`) via `geesefsd daemon`, which **listens on a unix
socket** (`/run/geesefsd.sock`, see `internal/contract`) and serves one mount
request per connection:

1. Read a mount `Request` (`internal/protocol`) — container id, pid, and the
   validated policy — from the `createRuntime` hook.
2. Re-validate the policy (`policy.Validate`) as defense in depth, since this is
   the privileged component doing the `setns()` + mount.
3. If that id is already mounted, reply `READY` (idempotent). Otherwise launch
   GeeSFS via `nsenter -t <pid> -m -- /.geesefs/bin/geesefs …` — entering the
   container's **mount** namespace only (so the host network namespace, and thus
   the S3 endpoint, stays reachable) and executing the static geesefs binary
   bind-mounted into the container, with `PATH=/.geesefs/bin` so it finds the
   static `fusermount3` alongside it. S3 credentials come from the daemon's
   environment, never argv.
4. Poll the container's mount table via `/proc/<pid>/mountinfo` (read from the
   host — no helper binary needed inside the container) until the `fuse.geesefs`
   mount is live or a bounded timeout elapses (fail closed), record it keyed by
   container id, and reply `READY` / `FAILED`.

On container exit the `poststop` hook sends an unmount request for the same id;
geesefsd `SIGTERM`s GeeSFS (which unmounts the FUSE filesystem itself) and drops
the state. Cleanup is idempotent, and each container's state is keyed by its own
id so one container can never reach another's mount.

The pure helpers (mount argv construction, the mount env, `mountinfo` readiness
parsing, the per-id registry, and the request-serving logic behind a `mounter`
interface) are table-tested; the live `setns()` + GeeSFS mount is exercised by
the [integration test](./integration-test.md).
