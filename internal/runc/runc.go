// Package runc implements the geesefs-runc role: a runc-compatible wrapper that
// Docker/containerd invoke in place of runc. It inspects the OCI bundle on the
// `create` subcommand, decides whether the container opted into GeeSFS
// injection, and then always execs the real runc so the container lifecycle is
// never broken.
//
// Phase 1 (this pass) is behavior-preserving relative to the original shell
// stub: it parses args, finds config.json, reads the policy annotations, and
// for a matched container LOGS the mutations it would make. The actual
// config.json mutation is Phase 3 (internal/ocispec); until then a matched
// container starts WITHOUT the mount.
package runc

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/randomizedcoder/fuse-filesystem-inject/internal/cli"
	"github.com/randomizedcoder/fuse-filesystem-inject/internal/contract"
	"github.com/randomizedcoder/fuse-filesystem-inject/internal/ocispec"
	"github.com/randomizedcoder/fuse-filesystem-inject/internal/policy"
)

// hookPathEnv is set by the Nix wrapper to the absolute path of the
// geesefs-hook binary (its sibling in the wrapper's bin/). We cannot derive it
// from os.Executable(), which resolves to the unwrapped binary in a different
// store path.
const hookPathEnv = "GEESEFS_HOOK_PATH"

// selftestFlag is a build-time smoke used by the Nix `geesefs-runc-smoke`
// check to prove the wrapper parses and can locate the real runc.
const selftestFlag = "--geesefs-selftest"

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "geesefs-runc: "+format+"\n", args...)
}

// findRunc locates the real runc binary on PATH (the Nix wrapper puts it
// there). It is the runtime the wrapper always delegates to.
func findRunc() (string, error) {
	p, err := exec.LookPath("runc")
	if err != nil {
		return "", fmt.Errorf("real runc not found on PATH: %w", err)
	}
	return p, nil
}

// Run is the entry point for the geesefs-runc role. It returns a process exit
// code; on the normal path it never returns because it execs the real runc.
func Run(args []string) int {
	if len(args) > 0 && args[0] == selftestFlag {
		return selftest()
	}

	inv := cli.ParseRunc(args)
	if inv.Subcommand == "create" {
		if code, abort := handleCreate(inv.Bundle); abort {
			// Fail closed: an opted-in container is misconfigured, so we
			// refuse to create it rather than exec runc.
			return code
		}
	}

	return execRunc(args)
}

func selftest() int {
	runcPath, err := findRunc()
	if err != nil {
		logf("selftest: %v", err)
		return 1
	}
	logf("selftest ok (runc=%s)", runcPath)
	return 0
}

// handleCreate reads the bundle's config.json and evaluates the injection
// policy. It returns (exitCode, abort): when abort is true the caller must NOT
// exec runc — the container opted in but its policy is invalid, so we fail
// closed. When abort is false, execution proceeds to exec runc normally
// (pass-through, or a matched-and-valid container that Phase 3 will mutate).
//
// Problems reading or parsing the spec are NOT our failure to own: we pass
// through and let the real runc report them.
func handleCreate(bundle string) (exitCode int, abort bool) {
	if bundle == "" {
		if cwd, err := os.Getwd(); err == nil {
			bundle = cwd
		}
	}
	configPath := filepath.Join(bundle, "config.json")

	data, err := os.ReadFile(configPath)
	if err != nil {
		logf("no readable config.json at %s (pass-through): %v", configPath, err)
		return 0, false
	}

	var spec struct {
		Annotations map[string]string `json:"annotations"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		logf("config.json at %s is not valid JSON (pass-through): %v", configPath, err)
		return 0, false
	}

	pol, matched, err := policy.Parse(spec.Annotations)
	if err != nil {
		logf("FATAL: refusing to start container: invalid GeeSFS policy: %v", err)
		return 1, true
	}
	if !matched {
		logf("no GeeSFS policy for bundle=%s (pass-through)", bundle)
		return 0, false
	}

	hookPath, err := resolveHookPath()
	if err != nil {
		logf("FATAL: refusing to start container: %v", err)
		return 1, true
	}

	mutated, err := ocispec.Mutate(data, pol, hookPath)
	if err != nil {
		logf("FATAL: refusing to start container: mutating %s: %v", configPath, err)
		return 1, true
	}

	// Preserve the original file mode; fall back to 0644 if it can't be read.
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(configPath); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(configPath, mutated, mode); err != nil {
		logf("FATAL: refusing to start container: writing %s: %v", configPath, err)
		return 1, true
	}

	logf("GeeSFS injection applied to %s (bucket=%s mount=%s endpoint=%s)",
		configPath, pol.Bucket, pol.Mount, pol.Endpoint)
	logf("  added: %s device + cgroup rule c %d:%d rwm, CAP_SYS_ADMIN, mount target %s, createRuntime + poststop hooks",
		contract.FuseDevicePath, contract.FuseDeviceMajor, contract.FuseDeviceMinor, pol.Mount)
	return 0, false
}

// resolveHookPath returns the absolute path to the geesefs-hook binary. It
// prefers the value the Nix wrapper sets (GEESEFS_HOOK_PATH) and otherwise
// falls back to a PATH lookup.
func resolveHookPath() (string, error) {
	if p := os.Getenv(hookPathEnv); p != "" {
		return p, nil
	}
	p, err := exec.LookPath(contract.RoleHook)
	if err != nil {
		return "", fmt.Errorf("cannot locate %s (set %s or put it on PATH): %w",
			contract.RoleHook, hookPathEnv, err)
	}
	return p, nil
}

// execRunc replaces this process with the real runc, preserving the original
// arguments and environment (this is `exec runc "$@"`).
func execRunc(args []string) int {
	runcPath, err := findRunc()
	if err != nil {
		logf("FATAL: %v", err)
		return 1
	}
	argv := append([]string{runcPath}, args...)
	if err := syscall.Exec(runcPath, argv, os.Environ()); err != nil {
		logf("FATAL: exec runc failed: %v", err)
		return 1
	}
	return 0 // unreachable: Exec replaced the process image on success.
}
