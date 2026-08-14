package ocispec

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/randomizedcoder/fuse-filesystem-inject/internal/policy"
)

const hookPath = "/nix/store/xxxx-geesefs-inject/bin/geesefs-hook"

func testPolicy() policy.Policy {
	return policy.Policy{Bucket: "models", Mount: "/models", Endpoint: "http://127.0.0.1:9000"}
}

// mutate is a test helper that fails on error.
func mutate(t *testing.T, in string) map[string]any {
	t.Helper()
	out, err := Mutate([]byte(in), testPolicy(), hookPath)
	if err != nil {
		t.Fatalf("Mutate returned error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("Mutate produced invalid JSON: %v", err)
	}
	return m
}

func TestMutate_AddsAllFields(t *testing.T) {
	m := mutate(t, `{"ociVersion":"1.0.2"}`)

	// /dev/fuse device present.
	devices := dig(t, m, "linux", "devices").([]any)
	if !hasDeviceWithPath(devices, "/dev/fuse") {
		t.Errorf("linux.devices missing /dev/fuse: %v", devices)
	}

	// device-cgroup allow rule present.
	rules := dig(t, m, "linux", "resources", "devices").([]any)
	if len(rules) == 0 {
		t.Errorf("linux.resources.devices is empty")
	}

	// CAP_SYS_ADMIN in each set, exactly once.
	caps := dig(t, m, "process", "capabilities").(map[string]any)
	for _, set := range capabilitySets {
		if got := countString(caps[set].([]any), capSysAdmin); got != 1 {
			t.Errorf("capabilities.%s has %d copies of %s, want 1", set, got, capSysAdmin)
		}
	}

	// mount target present.
	if !hasMountDest(m["mounts"].([]any), "/models") {
		t.Errorf("mounts missing destination /models: %v", m["mounts"])
	}

	// createRuntime hook registered with our path + policy args.
	hooks := dig(t, m, "hooks", "createRuntime").([]any)
	if len(hooks) != 1 {
		t.Fatalf("createRuntime has %d hooks, want 1", len(hooks))
	}
	h := hooks[0].(map[string]any)
	if h["path"] != hookPath {
		t.Errorf("hook path = %v, want %v", h["path"], hookPath)
	}
	args := h["args"].([]any)
	wantArgs := []any{"geesefs-hook", "models", "/models", "http://127.0.0.1:9000"}
	if len(args) != len(wantArgs) {
		t.Fatalf("hook args = %v, want %v", args, wantArgs)
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Errorf("hook args[%d] = %v, want %v", i, args[i], wantArgs[i])
		}
	}

	// ociVersion is preserved.
	if m["ociVersion"] != "1.0.2" {
		t.Errorf("ociVersion = %v, want 1.0.2", m["ociVersion"])
	}
}

func TestMutate_Idempotent(t *testing.T) {
	inputs := []struct {
		name string
		spec string
	}{
		{"empty object", `{}`},
		{"minimal", `{"ociVersion":"1.0.2"}`},
		{"already has fuse device", `{"linux":{"devices":[{"type":"c","path":"/dev/fuse","major":10,"minor":229}]}}`},
		{"already has capability", `{"process":{"capabilities":{"bounding":["CAP_SYS_ADMIN"]}}}`},
		{"already has mount", `{"mounts":[{"destination":"/models","type":"tmpfs","source":"tmpfs"}]}`},
		{"rich spec", `{"ociVersion":"1.0.2","process":{"args":["sleep","1"],"capabilities":{"bounding":["CAP_CHOWN"]}},"linux":{"devices":[{"path":"/dev/null","type":"c","major":1,"minor":3}]},"mounts":[{"destination":"/tmp","type":"tmpfs"}]}`},
	}

	for _, tc := range inputs {
		t.Run(tc.name, func(t *testing.T) {
			out1, err := Mutate([]byte(tc.spec), testPolicy(), hookPath)
			if err != nil {
				t.Fatalf("first Mutate: %v", err)
			}
			out2, err := Mutate(out1, testPolicy(), hookPath)
			if err != nil {
				t.Fatalf("second Mutate: %v", err)
			}
			if !bytes.Equal(out1, out2) {
				t.Errorf("Mutate is not idempotent:\nfirst:\n%s\nsecond:\n%s", out1, out2)
			}
		})
	}
}

func TestMutate_NoDuplicates(t *testing.T) {
	// A spec that already carries every injected field must gain no duplicates.
	spec := `{
	  "process":{"capabilities":{"bounding":["CAP_SYS_ADMIN"],"effective":["CAP_SYS_ADMIN"],"permitted":["CAP_SYS_ADMIN"]}},
	  "linux":{"devices":[{"type":"c","path":"/dev/fuse","major":10,"minor":229}],
	           "resources":{"devices":[{"allow":true,"type":"c","major":10,"minor":229,"access":"rwm"}]}},
	  "mounts":[{"destination":"/models","type":"tmpfs","source":"tmpfs"}],
	  "hooks":{"createRuntime":[{"path":"` + hookPath + `","args":["geesefs-hook","models","/models","http://127.0.0.1:9000"]}]}
	}`
	m := mutate(t, spec)

	if n := countDevicePath(dig(t, m, "linux", "devices").([]any), "/dev/fuse"); n != 1 {
		t.Errorf("/dev/fuse device count = %d, want 1", n)
	}
	if n := len(dig(t, m, "linux", "resources", "devices").([]any)); n != 1 {
		t.Errorf("device cgroup rule count = %d, want 1", n)
	}
	if n := len(dig(t, m, "hooks", "createRuntime").([]any)); n != 1 {
		t.Errorf("createRuntime hook count = %d, want 1", n)
	}
	if n := countMountDest(m["mounts"].([]any), "/models"); n != 1 {
		t.Errorf("/models mount count = %d, want 1", n)
	}
}

func TestMutate_Errors(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec string
	}{
		{"invalid json", `{not json`},
		{"json array not object", `["a","b"]`},
		{"json null", `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Mutate([]byte(tc.spec), testPolicy(), hookPath); err == nil {
				t.Errorf("Mutate(%q) = nil error, want error", tc.spec)
			}
		})
	}
}

// --- test helpers ----------------------------------------------------------

func dig(t *testing.T, m map[string]any, keys ...string) any {
	t.Helper()
	var cur any = m
	for _, k := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("dig: %v is not an object at key %q", cur, k)
		}
		cur, ok = obj[k]
		if !ok {
			t.Fatalf("dig: missing key %q", k)
		}
	}
	return cur
}

func hasDeviceWithPath(devices []any, path string) bool {
	return countDevicePath(devices, path) > 0
}

func countDevicePath(devices []any, path string) (n int) {
	for _, d := range devices {
		if dm, ok := d.(map[string]any); ok && dm["path"] == path {
			n++
		}
	}
	return n
}

func hasMountDest(mounts []any, dest string) bool {
	return countMountDest(mounts, dest) > 0
}

func countMountDest(mounts []any, dest string) (n int) {
	for _, m := range mounts {
		if mm, ok := m.(map[string]any); ok && mm["destination"] == dest {
			n++
		}
	}
	return n
}

func countString(arr []any, s string) (n int) {
	for _, e := range arr {
		if e == s {
			n++
		}
	}
	return n
}
