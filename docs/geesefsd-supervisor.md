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

## Where everything runs

A common misread is that `geesefsd` runs *inside* the container. It does not — it
is a **sibling of the container on the VM**, and only the *mount* crosses into the
container. The full nesting:

```
outer host (Nix + KVM)
└── microVM  (NixOS guest — the "host" for everything below)
    ├── dockerd
    │   └── pytorch-demo container ── mount namespace ──►  /models  (FUSE)
    └── geesefsd  (systemd service, runs on the VM as root)
            │  on a mount request from the createRuntime hook:
            └── nsenter -t <container-pid> -m -- <root>/.geesefs/bin/geesefs …
                    │
                    └── geesefs process  (a CHILD of geesefsd; a VM-host process)
                        • enters ONLY the container's MOUNT namespace ← mount lands here
                        • stays in the host NET namespace → reaches MinIO on the VM
                        • stays in the host PID namespace → invisible to the container's `ps`
                        • execs the static geesefs bind-mounted into the container rootfs
```

So:

- **`geesefsd` runs in the microVM**, alongside `dockerd`, as an ordinary systemd
  service (`nix/modules/injection.nix`). It is a *sibling* of the container, not a
  parent, and never runs inside it.
- **What crosses into the container is the mount, not the daemon.** `geesefsd`
  shells out to `nsenter -m` to attach to the container's *mount* namespace only,
  then launches `geesefs` there. The resulting `fuse.geesefs` mount is visible
  inside the container at `/models`, but the `geesefs` *process* remains a VM-host
  process (a child of `geesefsd`) — the container can't see it in its process
  list, cgroup, or network namespace.
- **This split is why the credential story works** (see [below](#s3-credentials)):
  the AWS keys live in `geesefsd`'s environment on the VM and are inherited by that
  host-side `geesefs` child. They are never handed to any process running inside
  the container.

Two consequences worth internalizing:

- **Network reachability is the VM's, not the container's.** `geesefs`
  deliberately does *not* enter the container's net namespace (`-m` only, no `-n`),
  so it talks to the S3 endpoint over the VM's own networking. Moving to a real S3
  endpoint is a property of the VM, not of any container.
- **The static binary** at `<root>/.geesefs/bin/geesefs` was bind-mounted into the
  container's rootfs by `geesefs-runc` purely so it is reachable at a known path
  after `pivot_root` — but it is executed by the host-side `geesefsd`, not by the
  container.

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
   GeeSFS via `nsenter -t <pid> -m -- <root>/.geesefs/bin/geesefs …` — entering
   the container's **mount** namespace only (so the host network namespace, and
   thus the S3 endpoint, stays reachable) and executing the static geesefs binary
   bind-mounted into the container, with `PATH=<root>/.geesefs/bin` so it finds
   the static `fusermount3` alongside it. S3 credentials come from the daemon's
   environment, never argv.
4. Poll the container's mount table via `/proc/<pid>/mountinfo` (read from the
   host — no helper binary needed inside the container) until the `fuse.geesefs`
   mount at `<root>/models` is live or a bounded timeout elapses (fail closed),
   record it keyed by container id, and reply `READY` / `FAILED`.

### Why `<root>` — the pre-`pivot_root` staging path

The `createRuntime` hook fires **after** runc has staged the container's rootfs
but **before** it `pivot_root`s into it, and `nsenter -m` enters the mount
namespace without `chroot`ing — so in-container absolute paths like `/models` or
`/.geesefs/bin/geesefs` resolve against the host root, where they do not yet
exist. geesefsd therefore resolves the staged rootfs from
`readlink(/proc/<pid>/cwd)` (runc has `chdir`'d the container init into it) and
prefixes it onto the payload path, the mount target, and `PATH`
(`internal/supervisor/supervisor.go:containerRoot`). Once the hook returns runc
`pivot_root`s `<root>` to `/`, so the mount created at `<root>/models` becomes
the container's `/models`.

On container exit the `poststop` hook sends an unmount request for the same id;
geesefsd `SIGTERM`s GeeSFS (which unmounts the FUSE filesystem itself) and drops
the state. Cleanup is idempotent, and each container's state is keyed by its own
id so one container can never reach another's mount.

The pure helpers (mount argv construction, the mount env, `mountinfo` readiness
parsing, the per-id registry, and the request-serving logic behind a `mounter`
interface) are table-tested; the live `setns()` + GeeSFS mount is exercised by
the [integration test](./integration-test.md).

## S3 credentials

### How it works today

Credentials never travel through the container, the image, the payload, the OCI
`config.json`, the annotations, or argv. They are supplied **host-side** to
geesefsd and reach GeeSFS by environment inheritance only:

- **Nix:** `nix/modules/injection.nix`, in the `systemd.services.geesefsd`
  attrset, sets `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `AWS_REGION` /
  `AWS_DEFAULT_REGION` in the unit's **`environment`** block. The values are read
  from `nix/constants.nix` (`minio.accessKey` / `minio.secretKey` / `minio.region`),
  so the demo's single source of truth is one file:

  ```nix
  # nix/modules/injection.nix — systemd.services.geesefsd
  environment = {
    AWS_ACCESS_KEY_ID     = constants.minio.accessKey;
    AWS_SECRET_ACCESS_KEY = constants.minio.secretKey;
    AWS_REGION            = constants.minio.region;
    AWS_DEFAULT_REGION    = constants.minio.region;
  };
  ```
- **Go:** `mountEnv()` (`internal/supervisor/supervisor.go`) inherits geesefsd's
  own environment (minus `PATH`, which it forces to the payload dir) and passes it
  as the `nsenter` child's `cmd.Env`; GeeSFS reads the standard AWS variables.

argv is avoided deliberately: a process's `/proc/<pid>/cmdline` is world-readable
(and shows up in `ps`), whereas the `environ` of a root-owned host process is
root-only. In this experiment that is a single static MinIO fixture key, shared
by every mount.

### Injecting per-container / per-bucket credentials

The single-key model is a demo simplification, not an architectural limit. The
seam is `mountEnv`: geesefsd already builds each child's environment per request,
so per-container credentials only require geesefsd to **resolve the right secret
for `req` and set the `AWS_*` vars for that child alone**. The hard rule is that
the secret is resolved from a **trusted host-side source**, never carried in the
(untrusted) annotations — an annotation may only ever name a *reference* that
geesefsd validates against an allowlist.

Three shapes, recommended first:

1. **Reference-by-annotation, resolved host-side (recommended).** Add a
   `geesefs.credential=<profile>` annotation carrying a *profile name*, not a
   secret. Thread it through `internal/policy` (validate the name against a strict
   charset + known-profile allowlist — fail closed otherwise) and
   `internal/protocol` (`Request.Credential string`, the name only). geesefsd
   resolves the profile to real keys from its own store — e.g. a root-only
   `/run/geesefs/creds/<profile>.env` populated by systemd `LoadCredential=` /
   `EnvironmentFile=`, or Vault — and `mountEnv` sets `AWS_*` from that instead of
   inheriting one global set. The secret stays out of `docker inspect`,
   `config.json`, argv, and the image; the untrusted input is only an allowlisted
   lookup key. **Nix-side**, this replaces the static `environment` block in
   `nix/modules/injection.nix` with a `serviceConfig.LoadCredential=` (or
   `EnvironmentFile=`) entry pointing geesefsd at the per-profile secret files;
   `nix/constants.nix` would hold the profile→bucket policy rather than raw keys.

2. **Host-side bucket→credential map.** No new annotation: geesefsd maps
   `req.Bucket` / `req.Endpoint` to a credential set via its own config. Simpler,
   but coarser (per-bucket, not per-container) and less explicit.

3. **Short-lived STS / workload identity.** geesefsd calls `AssumeRole` (role
   derived from a validated annotation or from the container's identity) and
   injects the temporary `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` /
   `AWS_SESSION_TOKEN`. Best for production — no long-lived secret at rest — at
   the cost of an STS endpoint and token-refresh handling.

All three preserve the existing invariants: credentials resolved host-side,
injected via env (never argv), and scoped per container id — the same per-id
keying that already isolates mounts (see [security.md](./security.md)) also keeps
one container from ever seeing another's credentials.

### Multi-tenant: a bucket + credentials per customer

A concrete target: one container per customer, each mounting a *different* S3
bucket with a *different* credential set. This is worth spelling out because it is
**half-done already**.

**What is already per-container today.** The bucket, mount path, and endpoint are
per-request, not global. Each container's `createRuntime` hook reads that
container's own annotations and sends geesefsd a `Request{id, pid, bucket, mount,
endpoint}`, and the supervisor keys all state by container id in its registry. So
two customers launched as:

```sh
# customer A
docker run --runtime=geesefs \
  --annotation geesefs.enabled=true \
  --annotation geesefs.bucket=cust-a-data \
  --annotation geesefs.mount=/models \
  --annotation geesefs.endpoint=https://s3.example.com \
  cust-a-image …

# customer B — different bucket
docker run --runtime=geesefs \
  --annotation geesefs.enabled=true \
  --annotation geesefs.bucket=cust-b-data \
  --annotation geesefs.mount=/models \
  --annotation geesefs.endpoint=https://s3.example.com \
  cust-b-image …
```

already get *different buckets* mounted at `/models`, each in its own mount
namespace, each tracked under its own container id. The **only** thing they share
today is the single global AWS key from geesefsd's unit environment.

**What is missing: per-customer credentials.** This is design #1 above, applied.
Roughly the work:

1. **Contract** (`internal/contract`): add `AnnCredential = "geesefs.credential"`.
2. **Policy** (`internal/policy`): parse the profile name; validate it against a
   strict charset **and** an allowlist of known profiles; fail closed on anything
   else.
3. **Protocol** (`internal/protocol`): add `Request.Credential string` (the
   profile *name* only — never the secret).
4. **Hook/runc** (`internal/runc`, `internal/hook`): carry the new annotation into
   the `Request` alongside bucket/mount/endpoint (the same path they already
   travel).
5. **Supervisor** (`internal/supervisor`): a `resolveCreds(profile)` that reads a
   root-only per-profile secret (e.g. `/run/geesefs/creds/<profile>.env`); change
   `mountEnv` to set `AWS_*` from the resolved set for that child alone, instead of
   inheriting one global set.
6. **Nix** (`nix/modules/injection.nix`): provision the per-profile secret files
   via `serviceConfig.LoadCredential=` / `EnvironmentFile=` (not the shared
   `environment` block); `nix/constants.nix` holds the profile→bucket policy.

**The security crux — a claim needs an authorization check.** The annotation is
attacker-controllable, so `geesefs.credential=cust-a` is only a *claim*. Customer
B must not be able to mount customer A's bucket by setting A's profile name.
geesefsd therefore has to bind a container to its permitted profile from a
**trusted** signal, not the annotation alone — e.g. the orchestrator that launches
the container, the image identity/digest, or a per-tenant runtime name — and
reject a request whose claimed profile does not match. (Binding the profile to the
allowed bucket in `nix/constants.nix` closes the complementary hole: a profile may
only mount its own bucket.) The S3 credentials themselves are the last line of
defense: A's keys grant access only to A's bucket, so even a mismatch fails at the
storage layer rather than leaking data.

Everything else already holds: per-id registry keying keeps each customer's mount,
`geesefs` process, and now credential set separate, and each `geesefs` child only
ever has its own customer's `AWS_*` in its environment — never on a command line,
never inside any container.
