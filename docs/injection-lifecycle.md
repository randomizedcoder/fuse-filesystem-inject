# Injection Lifecycle

[← design overview](./design.md)

This describes exactly *when* the mount is established relative to the
application process, and what happens on failure.

## The createRuntime hook point

The OCI runtime lifecycle includes a `createRuntime` hook that fires **after**
the container namespaces are created but **before** the user-specified
application process starts — precisely the point we need. Crucially it runs in
the **runtime (host) namespace**, not the container's, which is exactly where a
host-side `setns()` supervisor must act. (An earlier draft named the
`startContainer` hook, but that one executes *inside* the container namespace
just before `execve`, so it cannot perform the host-side setns mount.)

```
container created
      |
      v
mount namespace ready
      |
      v
createRuntime hook (geesefs-runc installs it; it runs geesefs-hook,
                    which asks geesefsd to establish the mount)
      |
      +-- geesefsd starts GeeSFS
      |
      +-- geesefsd mounts S3 -> /models in the app's mount namespace
      |
      +-- geesefsd WAITS for the FUSE mount to become ready
      |
      +-- hook reports success
      |
      v
original ENTRYPOINT/CMD starts
```

## Mount-before-app is mandatory

The setup must **not** launch GeeSFS asynchronously and return immediately — the
application would race filesystem initialization. `geesefsd` verifies the mount is
operational (e.g. it appears in the mount table as `fuse.geesefs` and is
listable) before allowing startup to continue.

## Failure semantics

For a required S3 mount, failing container startup is safer than starting the app
over an empty directory:

```
GeeSFS mount succeeds?
        |
        +-- yes --> start application
        |
        +-- no  --> FAIL container startup
```

Starting PyTorch with an empty `/models` where an S3 dataset was expected can lead
to far more dangerous, silent failures than simply refusing to start. Timeouts are
bounded so the runtime never waits indefinitely on an unreachable endpoint.

## Cleanup

`geesefs-runc` also registers a **`poststop`** hook — the symmetric counterpart
to `createRuntime`, running in the host/runtime namespace after the container
process has exited. It invokes `geesefs-hook unmount`, which sends geesefsd an
unmount request keyed by the same container id; geesefsd terminates GeeSFS (a
graceful `SIGTERM` lets it unmount the FUSE filesystem itself) and drops the
container's state. Cleanup is idempotent — an unmount for an unknown or
already-cleaned container succeeds — so a repeated `poststop` never fails.

Because the mount only ever exists inside the container's mount namespace (the
supervisor enters it with `nsenter -m`), it is never visible on the host; the
integration test asserts no mount is left on the host afterward. See
[integration-test.md](./integration-test.md).
