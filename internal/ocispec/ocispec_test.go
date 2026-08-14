package ocispec

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/randomizedcoder/fuse-filesystem-inject/internal/policy"
)

const (
	hookPath        = "/nix/store/xxxx-geesefs-inject/bin/geesefs-hook"
	geesefsBin      = "/nix/store/aaaa-geesefs/bin/geesefs"
	fusermount3Bin  = "/nix/store/bbbb-fusermount3/bin/fusermount3"
	payloadGeesefs  = "/.geesefs/bin/geesefs"
	payloadFusermnt = "/.geesefs/bin/fusermount3"
)

func testPolicy() policy.Policy {
	return policy.Policy{Bucket: "models", Mount: "/models", Endpoint: "http://127.0.0.1:9000"}
}

func testPayload() Payload {
	return Payload{Geesefs: geesefsBin, Fusermount3: fusermount3Bin}
}

// mutate is a test helper that fails on error.
func mutate(t *testing.T, in string) map[string]any {
	t.Helper()
	out, err := Mutate([]byte(in), testPolicy(), hookPath, testPayload())
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

	// static payload RO bind-mounted into the container.
	checkBindMount(t, m["mounts"].([]any), payloadGeesefs, geesefsBin)
	checkBindMount(t, m["mounts"].([]any), payloadFusermnt, fusermount3Bin)

	// createRuntime hook registered with our path + "mount" + policy args.
	checkHook(t, m, "createRuntime",
		[]any{"geesefs-hook", "mount", "models", "/models", "http://127.0.0.1:9000"})

	// poststop hook registered with our path + "unmount".
	checkHook(t, m, "poststop",
		[]any{"geesefs-hook", "unmount"})

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
			out1, err := Mutate([]byte(tc.spec), testPolicy(), hookPath, testPayload())
			if err != nil {
				t.Fatalf("first Mutate: %v", err)
			}
			out2, err := Mutate(out1, testPolicy(), hookPath, testPayload())
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
	  "mounts":[{"destination":"/models","type":"tmpfs","source":"tmpfs"},
	            {"destination":"` + payloadGeesefs + `","type":"bind","source":"` + geesefsBin + `","options":["bind","ro","nosuid","nodev"]},
	            {"destination":"` + payloadFusermnt + `","type":"bind","source":"` + fusermount3Bin + `","options":["bind","ro","nosuid","nodev"]}],
	  "hooks":{"createRuntime":[{"path":"` + hookPath + `","args":["geesefs-hook","mount","models","/models","http://127.0.0.1:9000"]}],
	           "poststop":[{"path":"` + hookPath + `","args":["geesefs-hook","unmount"]}]}
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
	if n := len(dig(t, m, "hooks", "poststop").([]any)); n != 1 {
		t.Errorf("poststop hook count = %d, want 1", n)
	}
	if n := countMountDest(m["mounts"].([]any), "/models"); n != 1 {
		t.Errorf("/models mount count = %d, want 1", n)
	}
	if n := countMountDest(m["mounts"].([]any), payloadGeesefs); n != 1 {
		t.Errorf("%s bind mount count = %d, want 1", payloadGeesefs, n)
	}
	if n := countMountDest(m["mounts"].([]any), payloadFusermnt); n != 1 {
		t.Errorf("%s bind mount count = %d, want 1", payloadFusermnt, n)
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
			if _, err := Mutate([]byte(tc.spec), testPolicy(), hookPath, testPayload()); err == nil {
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

// checkHook asserts the named OCI hook list holds exactly our single hook entry
// with the given argv.
func checkHook(t *testing.T, m map[string]any, name string, wantArgs []any) {
	t.Helper()
	hooks := dig(t, m, "hooks", name).([]any)
	if len(hooks) != 1 {
		t.Fatalf("%s has %d hooks, want 1", name, len(hooks))
	}
	h := hooks[0].(map[string]any)
	if h["path"] != hookPath {
		t.Errorf("%s hook path = %v, want %v", name, h["path"], hookPath)
	}
	args := h["args"].([]any)
	if len(args) != len(wantArgs) {
		t.Fatalf("%s hook args = %v, want %v", name, args, wantArgs)
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Errorf("%s hook args[%d] = %v, want %v", name, i, args[i], wantArgs[i])
		}
	}
}

// checkBindMount asserts mounts holds exactly one RO bind mount at dest from
// source, hardened with nosuid/nodev.
func checkBindMount(t *testing.T, mounts []any, dest, source string) {
	t.Helper()
	if n := countMountDest(mounts, dest); n != 1 {
		t.Fatalf("bind mount at %s count = %d, want 1", dest, n)
	}
	for _, m := range mounts {
		mm, ok := m.(map[string]any)
		if !ok || mm["destination"] != dest {
			continue
		}
		if mm["source"] != source {
			t.Errorf("%s source = %v, want %v", dest, mm["source"], source)
		}
		if mm["type"] != "bind" {
			t.Errorf("%s type = %v, want bind", dest, mm["type"])
		}
		opts, _ := mm["options"].([]any)
		for _, want := range []string{"bind", "ro", "nosuid", "nodev"} {
			if !containsString(opts, want) {
				t.Errorf("%s options %v missing %q", dest, opts, want)
			}
		}
		return
	}
}

func containsString(arr []any, s string) bool {
	for _, e := range arr {
		if e == s {
			return true
		}
	}
	return false
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
