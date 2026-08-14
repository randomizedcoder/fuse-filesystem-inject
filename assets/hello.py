#!/usr/bin/env python3
#
# assets/hello.py — a tiny PyTorch "hello world".
#
# This file is SEEDED INTO THE MINIO BUCKET by the minio bootstrap oneshot,
# then executed from the GeeSFS FUSE mount *inside the container*:
#
#     python /models/hello.py
#
# Running it successfully proves the full path end to end: the file was read
# through GeeSFS from MinIO/S3, and the container's own PyTorch executed it.
# The integration test greps stdout for the unique sentinel below.
#
# It is deliberately dependency-light beyond torch and prints the sentinel even
# if torch is missing, so a mount failure is distinguishable from an import
# failure in the logs.

SENTINEL = "FUSE-INJECT-PYTORCH-HELLO-OK"

try:
    import torch

    x = torch.arange(1, 5, dtype=torch.float32)
    total = float(x.sum().item())
    print(f"torch {torch.__version__}: sum(1..4) = {total}")
    assert total == 10.0, f"unexpected tensor sum: {total}"
    print(f"read from S3 FUSE mount and executed by the container: {SENTINEL}")
except ImportError as exc:  # pragma: no cover - diagnostic path
    print(f"ERROR: torch not importable in this container: {exc}")
    raise SystemExit(2)
