# Implementation Status

[← design overview](./design.md) · plan: [implementation-plan.md](./implementation-plan.md)

Living tracker for the [implementation plan](./implementation-plan.md). **Update this whenever a
phase's status or a definition-of-done item flips** — it is the single place to see how far the
`geesefs-runc` / `geesefsd` implementation has progressed past the scaffold.

**Legend:** ☐ todo · ◐ in-progress · ☑ done

## Overview

| Phase | Scope | Status | Tests | Notes |
|-------|-------|--------|-------|-------|
| 1 | Go module scaffold + Nix build wiring (behavior-preserving) | ☑ | `internal/cli` arg-parse table (+ `go-test`/`go-vet`/`gofmt`/`contract-parity` checks) | done — Go multi-call binary; no behavior change; microVM still builds |
| 2 | Policy parse + untrusted-annotation validation | ☑ | `internal/policy` table (25 cases) via `go-test` | done — fail-closed wired into geesefs-runc create |
| 3 | `geesefs-runc` `config.json` (OCI spec) mutation | ☑ | `internal/ocispec` table + idempotency via `go-test` | done — real mutation wired; hook switched to `createRuntime` |
| 4 | Hook + `geesefsd` mount (setns + mount-before-app) | ☑ | protocol codec, OCI-state parse, mount argv, `mountinfo` readiness, registry, `serve` tables via `go-test` | done — hook↔geesefsd unix-socket protocol + real `nsenter -m` mount wired (live mount exercised by the integration test) |
| 5 | Cleanup lifecycle + cross-container isolation | ☑ | serve unmount/idempotent/isolation tables via `go-test` | done — poststop hook → `geesefsd` reap+drop-state keyed by id; socket hardened root-only |
| 6 | Full E2E green + CI wiring + final review | ☑ | all unit + lint checks + static-payload smoke | done — self-contained static payload; `nix run .#integration-test` prints SUCCESS |

## Per-phase checklists

### Phase 1 — Go module scaffold + Nix build wiring  ✅
- [x] `go.mod` + `cmd/geesefs-inject/main.go` multi-call dispatch (`geesefs-runc` / `geesefsd` / `geesefs-hook`)
- [x] `internal/` skeleton (`contract`, `cli` + tests, `runc`, `supervisor`, `hook`, empty `policy` / `ocispec`)
- [x] `nix/lib/mkGoBinary.nix` (builds `geesefs-inject`, installs per-role argv[0] wrappers)
- [x] ~~`nix/lib/goModules.nix`~~ **N/A** — the module is stdlib-only, so `vendorHash = null` and there is no vendor tree to build (documented in `mkGoBinary.nix`)
- [x] `geesefs-runc` / `geesefsd` build from the Go module (both are the one `geesefs-inject` derivation; old `nix/packages/*.nix` stubs removed)
- [x] `nix build .#geesefs-runc .#geesefsd .#geesefs-inject` produce Go binaries; `--geesefs-selftest` still passes; microVM (`.#microvm`) still builds
- [x] integration test unchanged (still stub; `geesefsd mount` fails closed)
- [x] `internal/cli` arg-parse table test (11 cases incl. docker-style global value flags)
- [x] review gate: `nixfmt-check` + `gofmt` + `go-vet` + `go-test` + `contract-parity` all clean (`nix flake check` → all checks passed)

### Phase 2 — Policy parse + untrusted-annotation validation  ✅
- [x] `internal/policy`: annotations → typed `Policy{Bucket,Mount,Endpoint}` with `Parse` returning `(Policy, matched, err)`
- [x] validate mount path (absolute, `path.Clean(m)==m`, not `/`, no `..`/escape)
- [x] validate bucket charset (DNS-style regex, no shell/path metachars) + endpoint scheme allowlist (http/https), host required, no credentials/path/query
- [x] invalid input fails closed in `geesefs-runc` create (exit 1, no `exec runc`); unmatched passes through
- [x] table-driven tests incl. adversarial cases (`../`, relative, `/`, trailing slash, doubled sep, shell metachars, bad/missing scheme, embedded creds)
- [x] ~~Nix check `test-go-policy`~~ **covered by `go-test`** (`go test ./...` runs the policy table — a dedicated per-package check would be redundant)
- [x] review gate: `nix flake check` → all checks passed

### Phase 3 — `geesefs-runc` config.json mutation  ✅
- [x] `internal/ocispec`: add `/dev/fuse` device + cgroup rule `c 10:229 rwm`
- [x] add `CAP_SYS_ADMIN` (bounding/effective/permitted)
- [x] add mount-target dir (tmpfs) + `createRuntime` hook registration (argv0 + policy args)
- [x] idempotent (map[string]any round-trip, sorted-key output; re-apply adds nothing); unmatched never mutated
- [x] hook type is `createRuntime` (not `startContainer`); docs updated (design/injection-lifecycle/geesefs-runc)
- [x] matched+valid `create` writes the mutated spec (preserving file mode) then `exec runc`
- [x] table-driven tests: adds-all-fields, idempotency, no-duplicates, malformed/non-object spec → error
- [x] hook path resolved via `GEESEFS_HOOK_PATH` (set by the wrapper) with PATH fallback
- [x] ~~Nix check `test-go-ocispec`~~ **covered by `go-test`**
- [x] review gate: `nix flake check` → all checks passed

### Phase 4 — Hook + `geesefsd` mount  ✅
- [x] `hook` reads OCI state JSON on stdin; requests mount over unix socket (`internal/protocol`); blocks until READY/timeout
- [x] non-zero hook exit fails container startup (mount-before-app, fail-closed)
- [x] `geesefsd` daemon listens on the socket; creds come from its systemd environment (never argv); `nsenter -t <pid> -m` (mount ns only → retains host net ns) launches GeeSFS
- [x] poll `/proc/<pid>/mountinfo` for the `fuse.geesefs` mount until ready or bounded timeout; state keyed by container id (`registry`)
- [x] policy re-validated in `geesefsd` (`policy.Validate`) as defense in depth before any privileged action
- [x] integration test reaches "mount live + `python /models/hello.py` from S3" (steps 3–4) — *verified via a KVM run; not gated by `nix flake check`*
- [x] table tests: protocol codec, OCI-state parse, mount argv, mount env, `mountinfo` readiness, per-id registry, `serve` (valid/idempotent/fail-closed)
- [x] ~~Nix checks per package~~ **covered by `go-test`** (`go test ./...`)
- [x] review gate: `go vet` + `go test` clean locally; `nix flake check` (below)

### Phase 5 — Cleanup lifecycle + cross-container isolation  ✅
- [x] container-exit cleanup via a `poststop` hook (`geesefs-hook unmount`), keyed by id: reap GeeSFS (graceful SIGTERM → self-unmount) + drop state
- [x] idempotent cleanup: unmount of an unknown/already-cleaned container succeeds (repeated `poststop` never fails)
- [x] per-id isolation: an unmount touches only its own registry entry (table test with two containers)
- [x] socket hardened to root-only (`0600`) — a client can request a setns mount into an arbitrary pid, so only root (the hook's context) may reach it
- [x] cleanup-keying + idempotent-unmount + isolation table tests (`serve`)
- [x] integration test "no leftover mount on host" passes — *verified via a KVM run in Phase 6*
- [x] review gate: `go vet` + `go test` clean; `nix flake check` (below)

### Phase 6 — Full E2E green + CI wiring + final review  ✅
- [x] `nix run .#integration-test` prints `GEESEFS-INJECTION-TEST: SUCCESS` (verified on a KVM host)
- [x] `nix flake check` green for all Go unit checks + lint (11 checks) — incl. a `geesefs-static-smoke` asserting the payload is actually statically linked
- [x] documented why the integration test stays a `nix run` app (needs KVM + network) — see [integration-test.md](./integration-test.md)
- [x] `design.md` + README Status flipped from "scaffold/stubs" to "implemented"
- [x] final idiomatic / DRY sweep across Go + Nix
- [x] **payload rework:** the injected mount tooling is now a self-contained pair of static binaries (`geesefs` via `CGO_ENABLED=0`, `fusermount3` via `pkgsStatic.fuse3`) built locally under `nix/geesefs/` and RO bind-mounted into the container — the external `geesefs-oci` flake input was dropped, so the injection never reaches back to the host `/nix` closure ([geesefs-payload.md](./geesefs-payload.md))
