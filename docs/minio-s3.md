# MinIO S3 Fixture

[← design overview](./design.md)

MinIO provides the S3 endpoint that GeeSFS mounts. It runs as a NixOS service
inside the microVM and exists only for the VM's lifetime — a test fixture, not a
production deployment. Modeled on `xtcp2`'s `minio-bucket-bootstrap.nix`.

## Configuration

All values live in `nix/constants.nix` (`minio`):

| Setting | Value |
|---------|-------|
| API port | `9000` |
| Console port | `9001` |
| Region | `us-east-1` |
| Access key | `fuseinjecttest` |
| Secret key | `fuseinjecttestsecret` |
| Bucket | `models` |
| Data store | tmpfs at `/var/lib/minio`, `64M` (≈10 MiB used) |
| In-VM endpoint | `http://127.0.0.1:9000` |

MinIO is marked insecure in nixpkgs, so the exact version is pinned via
`config.permittedInsecurePackages` in `flake.nix`. It binds `0.0.0.0` so a future
QEMU host-forward could reach it; inside the VM everything uses `127.0.0.1`.

## Bootstrap + seeding

`nix/modules/minio.nix` defines `services.minio` plus a `minio-bucket-bootstrap`
oneshot that, once `/minio/health/live` returns 200:

1. `mc alias set local http://127.0.0.1:9000 …`
2. `mc mb --ignore-existing local/models`
3. `mc cp <assets/hello.py> local/models/hello.py`

Step 3 seeds the PyTorch hello-world payload into S3 so the integration test can
later execute it **through the FUSE mount** from inside the container. See
[pytorch-workload.md](./pytorch-workload.md) and
[integration-test.md](./integration-test.md).

The bootstrap is its own oneshot (not an `ExecStartPre`) so it can retry while
MinIO warms up, and later units get a clean `After=`/`Requires=` edge. `mc` shells
out to `getent`, so `pkgs.getent` is added to the unit `path`.

## Credentials

For the experiment, credentials are static and committed to `/nix/store`. In a
real deployment they would be delivered to GeeSFS by the host-side supervisor
from a secret store / workload identity, never baked into the app or GeeSFS
image — see [geesefsd-supervisor.md](./geesefsd-supervisor.md) and
[security.md](./security.md).
