// Package ocispec applies the GeeSFS injection to an OCI runtime spec
// (config.json): it adds the /dev/fuse device, the device-cgroup rule,
// CAP_SYS_ADMIN, the mount target, and the createRuntime hook. The
// transformation is pure and idempotent; an unmatched spec is returned
// byte-identical.
//
// Implemented in Phase 3 — see docs/implementation-plan.md. This file is the
// package placeholder for the scaffold.
package ocispec
