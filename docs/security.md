# Security Considerations

[← design overview](./design.md)

This design crosses several sensitive boundaries and should be treated as
**privileged infrastructure**. The list below is the review surface.

## Privilege surface

- **`/dev/fuse` exposure.** The wrapper grants a matched container access to the
  FUSE device. Scope it to matched containers only.
- **`CAP_SYS_ADMIN`.** FUSE mounting typically needs it. Automatically adding it
  to arbitrary application containers materially changes their security boundary.
  Grant the **minimum** required for FUSE, never full `--privileged`.
- **`setns()` into container namespaces.** `geesefsd` enters the container's mount
  namespace from the host. This is powerful; validate the target and fail closed.
- **OCI spec mutation.** `geesefs-runc` edits `config.json`. Bugs here can weaken
  isolation for the app.

## Untrusted input

Configuration arriving via OCI annotations
(`geesefs.bucket`, `geesefs.mount`, `geesefs.endpoint`) is **untrusted**. It must
be validated before use:

- bucket names, endpoints, and mount paths must be well-formed;
- mount paths must be prevented from escaping the intended container filesystem;
- values must never be interpolated into a shell in a way that permits arbitrary
  host command execution.

## Isolation guarantees the test checks

Because the mount is created **inside the container's mount namespace**, it is
confined to that container. The [integration test](./integration-test.md)
asserts this concretely:

- a marker file that exists only on the VM host (`/etc/vm-host-secret`) is **not**
  visible inside the container;
- `../../` traversal from `/models` cannot reach the VM host `/etc` (it resolves
  within the container rootfs);
- `/proc` inside the container is the container's own (PID 1 is the app, not the
  VM's `systemd`).

## Credentials

S3 credentials must not live in the application image, the GeeSFS payload, the
OCI `config.json`, the annotations, or any command line. The host-side supervisor
is the natural place to resolve them (workload identity, host secret store,
short-lived STS, Docker secrets) and hand them to GeeSFS by **environment
inheritance** — argv is avoided because `/proc/<pid>/cmdline` is world-readable,
whereas a root process's `environ` is not. In this experiment the MinIO
credentials are a single static test fixture set in the geesefsd systemd unit;
the [supervisor doc](./geesefsd-supervisor.md#s3-credentials) covers both the
current flow and how per-container credential injection would look (a validated,
allowlisted *reference* in an annotation resolving to a host-side secret — never
the secret itself). Because untrusted annotations can never carry a secret, only
a reference, an attacker who controls a container's annotations cannot exfiltrate
or forge another tenant's credentials.

## Cross-container isolation

One container must not be able to access another container's GeeSFS instance or
credentials. Container id is the key that scopes supervisor state and mounts.

## Known nesting requirement

FUSE inside Docker inside a microVM requires `/dev/fuse` in the guest and in the
container. This is validated as part of the environment; see
[microvm-host.md](./microvm-host.md).
