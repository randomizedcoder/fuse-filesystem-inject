// Package ocispec applies the GeeSFS injection to an OCI runtime spec
// (config.json). For a matched, validated policy it adds, minimally:
//
//   - the /dev/fuse device and its device-cgroup allow rule,
//   - CAP_SYS_ADMIN in the bounding/effective/permitted sets,
//   - a mount target directory (a small tmpfs at the mount path, so the
//     directory reliably exists for geesefsd to mount over),
//   - read-only bind mounts of the static geesefs + fusermount3 binaries into
//     the container (so the mount tooling is self-contained — see Payload), and
//   - a createRuntime hook (to establish the mount) and a matching poststop
//     hook (to reap it), both running geesefs-hook.
//
// The transformation is pure and idempotent: applying it to an already-injected
// spec adds nothing and yields an equivalent document. It operates on the spec
// as a generic JSON object (map[string]any) so that every field it does not
// touch is preserved verbatim — using typed structs would silently drop unknown
// fields on re-marshal.
package ocispec

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/randomizedcoder/fuse-filesystem-inject/internal/contract"
	"github.com/randomizedcoder/fuse-filesystem-inject/internal/policy"
	"github.com/randomizedcoder/fuse-filesystem-inject/internal/protocol"
)

// capSysAdmin is the single capability FUSE mounting requires. We never grant
// full --privileged (see docs/security.md).
const capSysAdmin = "CAP_SYS_ADMIN"

// capabilitySets are the sets CAP_SYS_ADMIN is added to. inheritable/ambient
// are intentionally excluded — the app process does not need to pass it on.
var capabilitySets = []string{"bounding", "effective", "permitted"}

// Payload holds the host paths of the static GeeSFS binaries that geesefs-runc
// RO bind-mounts into the container. They are the injected mount tooling; the
// container execs them (via geesefsd) instead of anything from the host closure.
type Payload struct {
	Geesefs     string
	Fusermount3 string
}

// Mutate applies the GeeSFS injection for pol to the OCI spec in specJSON and
// returns the modified spec. hookPath is the absolute path to the geesefs-hook
// binary runc will invoke; payload gives the host paths of the static binaries
// to bind-mount in. The output is deterministic (sorted keys), so it is stable
// across runs and idempotent under re-application.
func Mutate(specJSON []byte, pol policy.Policy, hookPath string, payload Payload) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(specJSON))
	dec.UseNumber() // keep numbers exact; avoids float64 round-tripping

	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("parse OCI spec: %w", err)
	}
	if root == nil {
		return nil, fmt.Errorf("parse OCI spec: expected a JSON object")
	}

	addFuseDevice(root)
	addDeviceCgroupRule(root)
	addCapability(root, capSysAdmin)
	addMountTarget(root, pol.Mount)
	addBindMount(root, contract.PayloadGeesefs, payload.Geesefs)
	addBindMount(root, contract.PayloadFusermount3, payload.Fusermount3)
	addHook(root, "createRuntime", hookPath, mountHookArgs(pol))
	addHook(root, "poststop", hookPath, unmountHookArgs())

	out, err := json.MarshalIndent(root, "", "\t")
	if err != nil {
		return nil, fmt.Errorf("marshal OCI spec: %w", err)
	}
	return append(out, '\n'), nil
}

// addFuseDevice adds the /dev/fuse character device to .linux.devices.
func addFuseDevice(root map[string]any) {
	linux := childObject(root, "linux")
	devices, _ := linux["devices"].([]any)
	for _, d := range devices {
		if dm, ok := d.(map[string]any); ok {
			if p, _ := dm["path"].(string); p == contract.FuseDevicePath {
				return
			}
		}
	}
	linux["devices"] = append(devices, map[string]any{
		"type":     "c",
		"path":     contract.FuseDevicePath,
		"major":    contract.FuseDeviceMajor,
		"minor":    contract.FuseDeviceMinor,
		"fileMode": 0o600,
		"uid":      0,
		"gid":      0,
	})
}

// addDeviceCgroupRule allows the FUSE device through the devices cgroup.
func addDeviceCgroupRule(root map[string]any) {
	resources := childObject(childObject(root, "linux"), "resources")
	rules, _ := resources["devices"].([]any)
	for _, r := range rules {
		if rm, ok := r.(map[string]any); ok {
			t, _ := rm["type"].(string)
			if t == "c" && numIs(rm["major"], contract.FuseDeviceMajor) && numIs(rm["minor"], contract.FuseDeviceMinor) {
				return
			}
		}
	}
	resources["devices"] = append(rules, map[string]any{
		"allow":  true,
		"type":   "c",
		"major":  contract.FuseDeviceMajor,
		"minor":  contract.FuseDeviceMinor,
		"access": "rwm",
	})
}

// addCapability adds capName to each of the bounding/effective/permitted sets.
func addCapability(root map[string]any, capName string) {
	caps := childObject(childObject(root, "process"), "capabilities")
	for _, set := range capabilitySets {
		caps[set] = appendStringUnique(caps[set], capName)
	}
}

// addMountTarget ensures the mount path exists in the rootfs by adding a small
// tmpfs mount there; geesefsd later mounts GeeSFS over it.
func addMountTarget(root map[string]any, mount string) {
	addMount(root, map[string]any{
		"destination": mount,
		"type":        "tmpfs",
		"source":      "tmpfs",
		"options":     []any{"nosuid", "nodev", "mode=755"},
	})
}

// addBindMount RO bind-mounts a single host file into the container at dest.
// This is how the static GeeSFS payload reaches the container without pulling
// in the host's /nix closure; the source is a dependency-free static binary and
// nosuid/nodev harden the mount (we exec it as container-root + CAP_SYS_ADMIN,
// so it never needs setuid).
func addBindMount(root map[string]any, dest, source string) {
	addMount(root, map[string]any{
		"destination": dest,
		"type":        "bind",
		"source":      source,
		"options":     []any{"bind", "ro", "nosuid", "nodev"},
	})
}

// addMount appends a mount entry unless one with the same destination already
// exists (idempotent). mount must carry a string "destination".
func addMount(root map[string]any, mount map[string]any) {
	dest, _ := mount["destination"].(string)
	mounts, _ := root["mounts"].([]any)
	for _, m := range mounts {
		if mm, ok := m.(map[string]any); ok {
			if d, _ := mm["destination"].(string); d == dest {
				return
			}
		}
	}
	root["mounts"] = append(mounts, mount)
}

// mountHookArgs is the argv for the createRuntime hook: the role (argv[0], which
// the wrapper also fixes), the "mount" operation, and the policy positionally.
// The container id and pid arrive on the hook's stdin as OCI State.
func mountHookArgs(pol policy.Policy) []any {
	return []any{contract.RoleHook, string(protocol.OpMount), pol.Bucket, pol.Mount, pol.Endpoint}
}

// unmountHookArgs is the argv for the poststop hook: just the "unmount"
// operation. The container id it cleans up arrives on stdin as OCI State.
func unmountHookArgs() []any {
	return []any{contract.RoleHook, string(protocol.OpUnmount)}
}

// addHook registers geesefs-hook under the named OCI hook (createRuntime for the
// mount, poststop for the unmount), both of which run in the host/runtime
// namespace — the correct place for geesefsd's host-side setns() work. It is
// idempotent per (hook, args): re-application adds nothing.
func addHook(root map[string]any, name, hookPath string, args []any) {
	hooks := childObject(root, "hooks")
	existing, _ := hooks[name].([]any)
	for _, h := range existing {
		if hm, ok := h.(map[string]any); ok {
			if p, _ := hm["path"].(string); p == hookPath {
				return
			}
		}
	}
	hooks[name] = append(existing, map[string]any{
		"path": hookPath,
		"args": args,
	})
}

// --- small generic helpers -------------------------------------------------

// childObject returns parent[key] as a map, creating and attaching an empty one
// if it is absent or not an object.
func childObject(parent map[string]any, key string) map[string]any {
	if existing, ok := parent[key].(map[string]any); ok {
		return existing
	}
	m := map[string]any{}
	parent[key] = m
	return m
}

// appendStringUnique appends s to v (treated as a []any of strings) unless it is
// already present, and returns the resulting slice.
func appendStringUnique(v any, s string) []any {
	arr, _ := v.([]any)
	for _, e := range arr {
		if es, _ := e.(string); es == s {
			return arr
		}
	}
	return append(arr, s)
}

// numIs reports whether v (a json.Number or float64) equals want.
func numIs(v any, want int) bool {
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		return err == nil && int(i) == want
	case float64:
		return int(n) == want
	default:
		return false
	}
}
