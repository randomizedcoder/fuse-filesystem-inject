// Package ocispec applies the GeeSFS injection to an OCI runtime spec
// (config.json). For a matched, validated policy it adds, minimally:
//
//   - the /dev/fuse device and its device-cgroup allow rule,
//   - CAP_SYS_ADMIN in the bounding/effective/permitted sets,
//   - a mount target directory (a small tmpfs at the mount path, so the
//     directory reliably exists for geesefsd to mount over), and
//   - a createRuntime hook that runs geesefs-hook.
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
)

// capSysAdmin is the single capability FUSE mounting requires. We never grant
// full --privileged (see docs/security.md).
const capSysAdmin = "CAP_SYS_ADMIN"

// capabilitySets are the sets CAP_SYS_ADMIN is added to. inheritable/ambient
// are intentionally excluded — the app process does not need to pass it on.
var capabilitySets = []string{"bounding", "effective", "permitted"}

// Mutate applies the GeeSFS injection for pol to the OCI spec in specJSON and
// returns the modified spec. hookPath is the absolute path to the geesefs-hook
// binary runc will invoke. The output is deterministic (sorted keys), so it is
// stable across runs and idempotent under re-application.
func Mutate(specJSON []byte, pol policy.Policy, hookPath string) ([]byte, error) {
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
	addCreateRuntimeHook(root, hookPath, pol)

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
	mounts, _ := root["mounts"].([]any)
	for _, m := range mounts {
		if mm, ok := m.(map[string]any); ok {
			if d, _ := mm["destination"].(string); d == mount {
				return
			}
		}
	}
	root["mounts"] = append(mounts, map[string]any{
		"destination": mount,
		"type":        "tmpfs",
		"source":      "tmpfs",
		"options":     []any{"nosuid", "nodev", "mode=755"},
	})
}

// addCreateRuntimeHook registers geesefs-hook as a createRuntime hook. The hook
// runs in the host/runtime namespace after the container namespaces exist —
// the correct place for geesefsd's host-side setns() mount. The policy travels
// as positional hook args (argv[0] selects the role); the container id and pid
// arrive on the hook's stdin as OCI State.
func addCreateRuntimeHook(root map[string]any, hookPath string, pol policy.Policy) {
	hooks := childObject(root, "hooks")
	createRuntime, _ := hooks["createRuntime"].([]any)
	for _, h := range createRuntime {
		if hm, ok := h.(map[string]any); ok {
			if p, _ := hm["path"].(string); p == hookPath {
				return
			}
		}
	}
	hooks["createRuntime"] = append(createRuntime, map[string]any{
		"path": hookPath,
		"args": []any{contract.RoleHook, pol.Bucket, pol.Mount, pol.Endpoint},
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
