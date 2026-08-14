# `geesefs-runc` — the OCI-config Injector

[← design overview](./design.md)

`geesefs-runc` is a small `runc`-compatible wrapper. Docker/containerd invoke it
instead of `runc`. It does **not** implement a container runtime — it makes
narrowly-scoped changes to the OCI spec and then execs the real `runc`.

## Responsibilities

```
1. Receive the normal runc CLI invocation.
2. On `create`, locate the OCI bundle's config.json (--bundle / cwd).
3. Read the injection policy from OCI annotations.
4. Not matched?  -> exec real runc unchanged (pass-through).
   Matched but INVALID policy? -> fail closed (exit non-zero, do NOT exec runc).
5. Matched + valid -> mutate config.json to add:
                      - device /dev/fuse (+ device-cgroup rule)
                      - capability CAP_SYS_ADMIN (minimum for FUSE mount)
                      - the mount target directory in the rootfs
                      - RO bind mounts of the static geesefs + fusermount3
                        binaries into /.geesefs/bin (the self-contained payload)
                      - a createRuntime hook -> geesefs-hook mount   -> geesefsd
                      - a poststop     hook -> geesefs-hook unmount -> geesefsd
6. Write the modified spec and exec real runc with the original args.
```

Keeping it tiny minimizes security-sensitive code and maintenance. It must behave
**identically to `runc`** for any container that does not match policy, so it is
safe to register (or even set as `default-runtime`).

## Selecting containers (policy)

Injection is policy-driven, never blind. Where launch parameters are controllable,
**OCI annotations** are the interface, set with `docker run --annotation
<key>=<value>`. (Note: Docker's `--label` populates Docker's *own* container
metadata and does **not** appear in the OCI `config.json` annotations the runtime
reads, so it will not trigger injection — the wrapper only inspects annotations.)
Keys (from `nix/constants.nix`):

| Annotation | Meaning |
|------------|---------|
| `geesefs.enabled=true` | opt this container in |
| `geesefs.bucket=models` | S3 bucket to mount |
| `geesefs.mount=/models` | mount path inside the container |
| `geesefs.endpoint=http://127.0.0.1:9000` | S3 endpoint |

Where launch args cannot be changed, policy can instead live on the host and match
image digest, container name, platform labels, or an external policy DB. A
conservative default matters: **unmatched containers pass through unchanged.**

All annotation values are **untrusted input** — see [security.md](./security.md).

## Registration

`nix/modules/docker.nix` registers it as the `geesefs` runtime:

```nix
virtualisation.docker.daemon.settings.runtimes.geesefs.path =
  "${geesefs-runc}/bin/geesefs-runc";
```

Then `docker run --runtime=geesefs …`.

## Implementation status

`geesefs-runc` is the `geesefs-runc` role of the single Go multi-call binary
`geesefs-inject` (`cmd/geesefs-inject`, role logic in `internal/runc`; the Nix
build in `nix/lib/mkGoBinary.nix` installs an argv[0] wrapper per role). It
implements the full runtime path: it parses the runc CLI (`internal/cli`), finds
`config.json`, validates the policy annotations (`internal/policy`, failing
closed on invalid untrusted input), and for a matched-and-valid container
**mutates `config.json`** (`internal/ocispec`) to add the `/dev/fuse` device +
cgroup rule, `CAP_SYS_ADMIN`, the mount target, the two RO bind mounts of the
static [payload](./geesefs-payload.md), and the `createRuntime` + `poststop`
hooks — then `exec`s the real `runc`. The mutation is pure, idempotent, and
preserves every other spec field. The payload store paths come from the Nix
wrapper via `GEESEFS_STATIC_BIN` / `GEESEFS_FUSERMOUNT3_BIN`; if either is
missing the container fails closed rather than starting over an empty mount.

The `createRuntime` hook runs `geesefs-hook`, which reads the OCI container
state, asks `geesefsd` over its unix socket to establish the mount, and blocks
until it is ready (fail-closed); the symmetric `poststop` hook tears it down on
exit — see [geesefsd-supervisor.md](./geesefsd-supervisor.md). The end-to-end
path is exercised by [integration-test.md](./integration-test.md).
