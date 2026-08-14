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
)

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
		inspectBundle(inv.Bundle)
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

// inspectBundle reads the bundle's config.json and logs the injection decision.
// It never fails the container: any problem reading or parsing the spec falls
// back to a plain pass-through (the exec of the real runc still happens).
func inspectBundle(bundle string) {
	if bundle == "" {
		if cwd, err := os.Getwd(); err == nil {
			bundle = cwd
		}
	}
	configPath := filepath.Join(bundle, "config.json")

	data, err := os.ReadFile(configPath)
	if err != nil {
		logf("no readable config.json at %s (pass-through): %v", configPath, err)
		return
	}

	var spec struct {
		Annotations map[string]string `json:"annotations"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		logf("config.json at %s is not valid JSON (pass-through): %v", configPath, err)
		return
	}

	if spec.Annotations[contract.AnnEnabled] != "true" {
		logf("no GeeSFS policy for bundle=%s (pass-through)", bundle)
		return
	}

	bucket := spec.Annotations[contract.AnnBucket]
	mount := spec.Annotations[contract.AnnMount]
	endpoint := spec.Annotations[contract.AnnEndpoint]

	logf("GeeSFS injection MATCHED for bundle=%s", bundle)
	logf("  bucket=%s mount=%s endpoint=%s", bucket, mount, endpoint)
	logf("  WOULD mutate %s to add:", configPath)
	logf("    - device %s (+ device-cgroup rule c %d:%d rwm)",
		contract.FuseDevicePath, contract.FuseDeviceMajor, contract.FuseDeviceMinor)
	logf("    - capability CAP_SYS_ADMIN (bounding/effective/permitted)")
	logf("    - mount target dir %s in the rootfs", mount)
	logf("    - createRuntime hook -> geesefsd mount <id> <bucket> <mount> <endpoint>")
	logf("  STUB: config.json mutation is deferred to Phase 3; the container")
	logf("        will start WITHOUT the mount until then.")
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
