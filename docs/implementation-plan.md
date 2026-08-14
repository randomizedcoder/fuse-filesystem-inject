# Implementation Plan

[← design overview](./design.md) · live progress: [implementation-status.md](./implementation-status.md)

The [scaffold](./design.md#status) stands up the microVM + Docker + MinIO + the injection
wiring, and the [integration test](./integration-test.md) encodes the target contract.
`geesefs-runc` and `geesefsd` are faithful **stubs** (pass-through + logged intentions). This
document is the ordered, reviewable path from those stubs to a working end-to-end injection.

## Ground decisions

- **Language: Go.** The two stubbed binaries become **one small Go multi-call binary**
  (subcommands `geesefs-runc`, `hook`, `geesefsd`), following the pattern already used in the
  sibling `xtcp2` repo: `pkgs.buildGoModule` with `doCheck = false`, a shared vendored-source
  derivation, and `go test` surfaced as **standalone Nix checks** (not the binary's
  `checkPhase`). Table-driven `[]struct{…}` tests; `gofmt` / `go vet` / `golangci-lint` as
  review gates.
  - Reference files to model on: `xtcp2/nix/lib/mkGoBinary.nix`, `xtcp2/nix/lib/goModules.nix`,
    `xtcp2/nix/tests/go-test-race.nix`, `xtcp2/nix/checks/`, and the table-driven
    `xtcp2/pkg/xtcp/deserializers_table_test.go`.
- **Shell → Go, not shell test harness.** No `bats`. Pure logic moves into Go where
  table-driven tests are idiomatic; shell remains only in the Nix/integration layer.

## The review gate (applies to every phase)

A phase is not "done" until all of the following are clean, and each is wired as a Nix check
where noted:

- `nixfmt --check` over all `*.nix` (existing `nixfmt-check`).
- `gofmt -l` reports nothing (check `gofmt`).
- `go vet ./...` clean (check `go-vet`).
- `golangci-lint run` clean (check `golangci-lint`).
- **Self-review checklist:**
  - Is the pure logic separated from I/O and covered by table-driven unit tests?
  - Is shared logic factored — no copy-paste between `geesefs-runc` / `hook` / `geesefsd`?
  - Does it read like `xtcp2` (layout, naming, error handling)?

## Phases

Each phase is independently reviewable and leaves the tree green (`nix flake check` passes;
the integration test may still fail at a later, not-yet-implemented step).

### Phase 1 — Go module scaffold + Nix build wiring (behavior-preserving)

**Goal:** stand up the Go module and build both binaries from it with **zero behavior change**
— still pass-through + log `WOULD…`, still answer `--geesefs-selftest`.

**Files:**
- `go.mod` (module root).
- `cmd/geesefs-inject/main.go` — multi-call dispatch on `argv[0]` / first arg →
  `geesefs-runc`, `geesefsd`, `hook`.
- `internal/…` — package skeleton (`cli`, and empty `policy` / `ocispec` / `contract`).
- `nix/lib/mkGoBinary.nix` — builds `geesefs-inject` and installs one argv[0] wrapper per
  role. (No `goModules.nix`: the module is stdlib-only, so `vendorHash = null` and there is no
  vendor tree — a deliberate simplification of the `xtcp2` pattern.)
- `geesefs-runc` and `geesefsd` become the one `geesefs-inject` derivation in `default.nix`
  (both bin names live in the same store path), so `nix/modules/docker.nix` and
  `nix/modules/injection.nix` need no changes. The old `nix/packages/geesefs-runc.nix` /
  `geesefsd.nix` shell stubs are removed.

**Definition of done:**
- `nix build .#geesefs-runc .#geesefsd` produce Go binaries.
- `--geesefs-selftest` smoke checks still pass; the integration test behaves exactly as today.
- `nix flake check` green, now including new `gofmt` and `go-vet` checks.

**Tests:** arg-parse table test in `internal/cli` — the subcommand / `--bundle` / flag matrix
(mirrors the current shell parser).

**Review gate.**

### Phase 2 — Policy parse + untrusted-annotation validation (pure)

**Goal:** `internal/policy` turns OCI annotations into a typed
`Policy{Enabled, Bucket, Mount, Endpoint}` and **validates untrusted input** (per
[security.md](./security.md)): mount path absolute + `path.Clean` + no `..`/escape; bucket-name
charset; endpoint scheme/host allowlist; reject anything unsafe to interpolate.

**Definition of done:**
- Validation implemented and used by the `geesefs-runc` `create` path.
- Invalid input → clear error and the container **fails closed**; unmatched → pass-through.

**Tests (table-driven — prime target):** valid cases plus adversarial —
`../`, relative, `/`, `/proc`, empty, over-long, bogus endpoint schemes. Nix check
`test-go-policy`.

**Review gate.**

### Phase 3 — `geesefs-runc`: `config.json` (OCI spec) mutation (pure)

**Goal:** `internal/ocispec` takes the raw spec JSON + a validated `Policy` and returns a
mutated spec adding:
- the `/dev/fuse` device,
- the device-cgroup rule (`c 10:229 rwm`),
- `CAP_SYS_ADMIN` (bounding / effective / permitted),
- the mount-target directory, and
- the injection hook registration.

**Idempotent** (applying twice equals once); **unmatched → byte-identical** spec.

**Correctness fix made here:** register a **`createRuntime`** hook — it runs in the host
(runtime) namespace after the container namespaces exist, which is what a host-side `setns()`
supervisor needs — **not** `startContainer`, which runs *inside* the container namespace.
Update [injection-lifecycle.md](./injection-lifecycle.md) and [geesefs-runc.md](./geesefs-runc.md)
to match.

**Definition of done:**
- A matched `create` writes the mutated spec, then `exec runc`; the container receives the
  device + caps + hook (the mount itself still lands in Phase 4).

**Tests (table-driven + golden):** matched adds exactly the right fields; unmatched unchanged;
idempotency; malformed spec → error; before/after golden specs. Nix check `test-go-ocispec`.

**Review gate.**

### Phase 4 — Hook + `geesefsd` mount (setns + mount-before-app)

**Goal:**
- `hook` subcommand reads the OCI state JSON on **stdin** (id, pid, annotations, bundle),
  requests a mount from `geesefsd` over a unix socket, and **blocks until READY or timeout** —
  a non-zero exit fails container startup (the mandatory *mount-before-app* + *fail-closed*
  contract from [injection-lifecycle.md](./injection-lifecycle.md)).
- `geesefsd` serves requests: fetch credentials (env / file), `nsenter -t <pid> -m` into the
  container mount namespace (retaining the host **net** namespace so MinIO stays reachable),
  launch GeeSFS mounting the bucket at the mount path, poll `findmnt` for `fuse.geesefs` until
  ready or a bounded timeout, record state keyed by container id, and reply READY / FAIL.

**Definition of done:**
- The integration test reaches "mount live + `python /models/hello.py` runs from S3" (steps
  3–4).
- A mount failure fails startup rather than running the app over an empty directory.

**Tests (table-driven where sensible):** mount argv construction
`(policy, pid, creds) → []string`; readiness-poll state machine
(ready / pending / timeout / error); request/response codec; per-id state map. The real
`setns()` + live GeeSFS mount is covered by the integration test, not unit tests.

**Review gate.**

### Phase 5 — Cleanup lifecycle + cross-container isolation

**Goal:** on container exit (a `poststop` hook or a `geesefsd` exit monitor, keyed by container
id): unmount, terminate GeeSFS, and drop state. One container must not be able to reach
another's mount or credentials.

**Definition of done:**
- The integration test's "no leftover mount on the host" assertion passes.
- A second-container assertion shows per-id isolation.

**Tests:** cleanup keying + idempotent-unmount table test.

**Review gate.**

### Phase 6 — Full E2E green + CI wiring + final review

**Goal:** the whole integration test prints `GEESEFS-INJECTION-TEST: SUCCESS` — mount live,
`hello.py` runs from S3, all isolation/escape assertions hold, clean unmount on exit.

**Definition of done:**
- `nix run .#integration-test` → SUCCESS.
- `nix flake check` green for **all** Go unit checks + lint. (Document why the boot-the-VM
  integration test stays a `nix run` **app** rather than a pure `flake check` gate — it needs
  KVM and network access for the `docker pull`.)
- [design.md](./design.md) and the README Status flip from "scaffold / stubs" to
  "implemented".
- Final idiomatic / DRY sweep across Go + Nix.

## Testing strategy

- **Unit (Go, table-driven), run as Nix checks** — the pure logic: policy validation
  (`test-go-policy`), spec mutation (`test-go-ocispec`), and the mount-arg / readiness / codec
  helpers. Each check copies the module source into the sandbox and runs `go test` offline
  (`GOPROXY=off`; the module is stdlib-only so no vendor tree is needed — see the `goCheck`
  helper in `nix/default.nix`). Binaries build with `doCheck = false`.
- **Lint / format gates** — `nixfmt-check` (exists) plus new `gofmt`, `go-vet`,
  `golangci-lint`.
- **Integration (in-guest, sentinel) — mechanism unchanged** — the existing
  `geesefs-injection-test.service` + host `expect` driver
  ([integration-test.md](./integration-test.md)); stays an **app** (`nix run
  .#integration-test`) because it needs a booted VM.
- **Not unit-tested (integration-only by nature):** the actual `setns()`, the live FUSE mount,
  and the Docker interaction.

## Idiomatic / DRY conventions

- **One Go module, one multi-call binary** — `geesefs-runc`, `hook`, and `geesefsd` are
  subcommands that share `internal/policy`, `internal/ocispec`, and `internal/contract`. No
  logic is duplicated across them.
- **Pure core, thin shell** — I/O (argv, stdin spec, sockets, `exec`) lives at the edges;
  validation and mutation are pure functions that take and return values, so they are trivially
  table-testable.
- **Single source of truth for the contract** — annotation keys, the FUSE device major/minor,
  and the default mount path live in Go (`internal/contract`); `nix/constants.nix` keeps its
  copies for the Nix/test side. A small **`contract-parity`** check asserts the two agree, so
  they can never drift.
- **Match the neighbor** — mirror `xtcp2` layout and idioms so the code is familiar.
