package supervisor

import (
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/randomizedcoder/fuse-filesystem-inject/internal/protocol"
)

func TestGeesefsArgs(t *testing.T) {
	req := protocol.Request{PID: 4242, Bucket: "models", Mount: "/models", Endpoint: "http://127.0.0.1:9000"}
	got := geesefsArgs(req)
	want := []string{
		"-t", "4242", "-m", "--",
		"geesefs", "-f", "--endpoint", "http://127.0.0.1:9000", "models", "/models",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("geesefsArgs:\n got %q\nwant %q", got, want)
	}
}

func TestGeesefsArgsEntersMountNsOnly(t *testing.T) {
	// Regression guard for the security-relevant invariant: enter the mount
	// namespace (-m) but NOT the network namespace, so the host S3 endpoint
	// stays reachable.
	got := geesefsArgs(protocol.Request{PID: 1, Bucket: "b", Mount: "/m", Endpoint: "http://x"})
	var hasM, hasN bool
	for _, a := range got {
		switch a {
		case "-m":
			hasM = true
		case "-n":
			hasN = true
		}
	}
	if !hasM {
		t.Error("expected -m (enter mount namespace)")
	}
	if hasN {
		t.Error("must NOT enter the network namespace (-n) — host endpoint must stay reachable")
	}
}

func TestFindmntArgs(t *testing.T) {
	got := findmntArgs(99, "/models")
	want := []string{"-t", "99", "-m", "--", "findmnt", "-n", "-o", "FSTYPE", "/models"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findmntArgs:\n got %q\nwant %q", got, want)
	}
}

func TestMountReady(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{"live geesefs mount", "fuse.geesefs\n", true},
		{"no trailing newline", "fuse.geesefs", true},
		{"leading/trailing space", "  fuse.geesefs  \n", true},
		{"not mounted (empty)", "", false},
		{"different fs", "tmpfs\n", false},
		{"other fuse fs", "fuse.sshfs\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mountReady(tt.output); got != tt.want {
				t.Errorf("mountReady(%q) = %v, want %v", tt.output, got, tt.want)
			}
		})
	}
}

func TestRegistry(t *testing.T) {
	r := newRegistry()

	if _, ok := r.get("missing"); ok {
		t.Error("get on empty registry should report not found")
	}
	if _, ok := r.remove("missing"); ok {
		t.Error("remove on empty registry should report not found")
	}

	a := &mountState{req: protocol.Request{ID: "a", PID: 1}}
	b := &mountState{req: protocol.Request{ID: "b", PID: 2}}
	r.add(a)
	r.add(b)

	if got, ok := r.get("a"); !ok || got != a {
		t.Errorf("get(a) = %v, %v; want %v, true", got, ok, a)
	}
	if want := []string{"a", "b"}; !reflect.DeepEqual(r.ids(), want) {
		t.Errorf("ids() = %v, want %v", r.ids(), want)
	}

	// re-add under the same id replaces (idempotent keying)
	a2 := &mountState{req: protocol.Request{ID: "a", PID: 3}}
	r.add(a2)
	if got, _ := r.get("a"); got.req.PID != 3 {
		t.Errorf("re-add should replace entry; got pid %d, want 3", got.req.PID)
	}

	got, ok := r.remove("a")
	if !ok || got != a2 {
		t.Errorf("remove(a) = %v, %v; want %v, true", got, ok, a2)
	}
	if _, ok := r.get("a"); ok {
		t.Error("a should be gone after remove")
	}
	if want := []string{"b"}; !reflect.DeepEqual(r.ids(), want) {
		t.Errorf("ids() after remove = %v, want %v", r.ids(), want)
	}
}

// fakeMounter records the requests it is asked to mount/unmount and can be told
// to fail, so serve() can be tested without setns/geesefs.
type fakeMounter struct {
	mounts   []protocol.Request
	unmounts []string
	failErr  error
}

func (f *fakeMounter) Mount(req protocol.Request) (*os.Process, error) {
	f.mounts = append(f.mounts, req)
	if f.failErr != nil {
		return nil, f.failErr
	}
	return nil, nil
}

func (f *fakeMounter) Unmount(st *mountState) error {
	f.unmounts = append(f.unmounts, st.req.ID)
	return f.failErr
}

func newTestServer(m mounter) *server { return &server{reg: newRegistry(), mounter: m} }

func validReq() protocol.Request {
	return protocol.Request{Op: protocol.OpMount, ID: "c1", PID: 100, Bucket: "models", Mount: "/models", Endpoint: "http://127.0.0.1:9000"}
}

func unmountReq(id string) protocol.Request {
	return protocol.Request{Op: protocol.OpUnmount, ID: id}
}

func TestServe(t *testing.T) {
	t.Run("valid request mounts and reports READY", func(t *testing.T) {
		fm := &fakeMounter{}
		s := newTestServer(fm)
		resp := s.serve(validReq())
		if resp.Status != protocol.StatusReady {
			t.Fatalf("status = %v (%s), want READY", resp.Status, resp.Error)
		}
		if len(fm.mounts) != 1 {
			t.Fatalf("expected exactly one mount call, got %d", len(fm.mounts))
		}
		if _, ok := s.reg.get("c1"); !ok {
			t.Error("expected c1 recorded in registry")
		}
	})

	t.Run("already mounted is idempotent (no second mount)", func(t *testing.T) {
		fm := &fakeMounter{}
		s := newTestServer(fm)
		s.serve(validReq())
		resp := s.serve(validReq())
		if resp.Status != protocol.StatusReady {
			t.Fatalf("status = %v, want READY", resp.Status)
		}
		if len(fm.mounts) != 1 {
			t.Errorf("expected mount called once, got %d", len(fm.mounts))
		}
	})

	t.Run("mounter error reports FAILED and records nothing", func(t *testing.T) {
		fm := &fakeMounter{failErr: fmt.Errorf("boom")}
		s := newTestServer(fm)
		resp := s.serve(validReq())
		if resp.Status != protocol.StatusFailed {
			t.Fatalf("status = %v, want FAILED", resp.Status)
		}
		if _, ok := s.reg.get("c1"); ok {
			t.Error("a failed mount must not be recorded")
		}
	})

	t.Run("invalid transport (bad pid) fails closed without mounting", func(t *testing.T) {
		fm := &fakeMounter{}
		s := newTestServer(fm)
		req := validReq()
		req.PID = 0
		resp := s.serve(req)
		if resp.Status != protocol.StatusFailed {
			t.Fatalf("status = %v, want FAILED", resp.Status)
		}
		if len(fm.mounts) != 0 {
			t.Error("must not attempt a mount for an invalid request")
		}
	})

	t.Run("invalid policy (mount escape) fails closed without mounting", func(t *testing.T) {
		fm := &fakeMounter{}
		s := newTestServer(fm)
		req := validReq()
		req.Mount = "/../etc"
		resp := s.serve(req)
		if resp.Status != protocol.StatusFailed {
			t.Fatalf("status = %v, want FAILED", resp.Status)
		}
		if len(fm.mounts) != 0 {
			t.Error("must not attempt a mount for an invalid policy")
		}
	})

	t.Run("unmount reaps and drops state", func(t *testing.T) {
		fm := &fakeMounter{}
		s := newTestServer(fm)
		s.serve(validReq())

		resp := s.serve(unmountReq("c1"))
		if resp.Status != protocol.StatusReady {
			t.Fatalf("status = %v (%s), want READY", resp.Status, resp.Error)
		}
		if len(fm.unmounts) != 1 || fm.unmounts[0] != "c1" {
			t.Errorf("expected one unmount of c1, got %v", fm.unmounts)
		}
		if _, ok := s.reg.get("c1"); ok {
			t.Error("state must be dropped after unmount")
		}
	})

	t.Run("unmount of unknown container is idempotent success", func(t *testing.T) {
		fm := &fakeMounter{}
		s := newTestServer(fm)
		resp := s.serve(unmountReq("never-mounted"))
		if resp.Status != protocol.StatusReady {
			t.Fatalf("status = %v, want READY", resp.Status)
		}
		if len(fm.unmounts) != 0 {
			t.Error("must not reap anything for an unknown container")
		}
	})

	t.Run("unmount touches only its own container (isolation)", func(t *testing.T) {
		fm := &fakeMounter{}
		s := newTestServer(fm)
		c1 := validReq()
		c2 := validReq()
		c2.ID, c2.PID = "c2", 200
		s.serve(c1)
		s.serve(c2)

		s.serve(unmountReq("c1"))

		if _, ok := s.reg.get("c1"); ok {
			t.Error("c1 should be gone")
		}
		if _, ok := s.reg.get("c2"); !ok {
			t.Error("c2 must be untouched by c1's unmount")
		}
		if len(fm.unmounts) != 1 || fm.unmounts[0] != "c1" {
			t.Errorf("expected only c1 reaped, got %v", fm.unmounts)
		}
	})
}
