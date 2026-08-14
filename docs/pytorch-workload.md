# PyTorch Example Workload

[← design overview](./design.md)

The demo workload is a **stock, unmodified upstream image** (`pytorch/pytorch`).
This is deliberate: the entire value of the design is injecting into an image we
do **not** control and cannot rebuild. If we built our own image, we would be
proving nothing about third-party workloads.

## How it is launched

```sh
docker run --rm \
  --runtime=geesefs \
  --annotation geesefs.enabled=true \
  --annotation geesefs.bucket=models \
  --annotation geesefs.mount=/models \
  --annotation geesefs.endpoint=http://127.0.0.1:9000 \
  pytorch/pytorch:latest \
  bash -lc 'python /models/hello.py'
```

Nothing about the image, its `ENTRYPOINT`, or its Python environment is changed —
the only additions are the `--runtime` and the `geesefs.*` **OCI annotations**,
which the [wrapper](./geesefs-runc.md) reads as policy. (Use `--annotation`, not
`--label`: Docker's `--label` populates Docker's own metadata and never reaches
the OCI `config.json` annotations the runtime inspects.)

### The always-on demo container

`nix run .#demo` boots the VM and the `pytorch-demo` guest service
(`nix/modules/demo-container.nix`) launches this same image **detached** with
`sleep infinity` instead of `--rm ... hello.py`, so the container stays up with
`/models` mounted. A user can then SSH in (see
[microvm-host.md](./microvm-host.md)) and explore the live mount:

```sh
docker exec -it pytorch-demo bash
ls -la /models              # files served from S3 over FUSE
python /models/hello.py     # runs FROM the S3 mount
```

The detached container carries **no credentials** — exactly as in the automated
test, `geesefsd` (whose systemd environment holds the S3 creds) performs the
mount, so the image and its annotations never see them.

## What the application sees

An ordinary directory at `/models` backed by the S3 bucket. The seeded
`hello.py` (see [minio-s3.md](./minio-s3.md)) imports `torch`, computes a small
tensor sum, and prints a unique sentinel — proving the file was read **through
GeeSFS from S3** and executed by the container's own PyTorch. The application does
not know GeeSFS exists.

`assets/hello.py` is intentionally dependency-light beyond `torch`, and prints a
distinct error if `torch` is missing, so a mount failure is distinguishable from
an import failure in the logs.

## Note on image size / iteration

`pytorch/pytorch:latest` is multi-GB. Override `constants.pytorchImage` with a
smaller CPU-only tag when iterating on the harness; the injection mechanism is
image-agnostic.
