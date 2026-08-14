// Package supervisor implements the geesefsd role: the host-side GeeSFS mount
// supervisor that runs as a systemd service on the VM. It listens on a unix
// socket for mount requests from the createRuntime hook, and for each opted-in
// container it enters the container's mount namespace, starts GeeSFS, verifies
// the FUSE mount is ready, and records the mount keyed by container id.
//
// The mount happens in the container's MOUNT namespace only (nsenter -m); the
// network namespace is deliberately left as the host's so the S3 endpoint (e.g.
// in-VM MinIO) stays reachable. GeeSFS runs in the foreground under geesefsd so
// the supervisor owns its lifetime (readiness, and cleanup in Phase 5).
package supervisor

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/randomizedcoder/fuse-filesystem-inject/internal/contract"
	"github.com/randomizedcoder/fuse-filesystem-inject/internal/policy"
	"github.com/randomizedcoder/fuse-filesystem-inject/internal/protocol"
)

const selftestFlag = "--geesefs-selftest"

// Mount lifecycle bounds. mountTimeout caps how long we wait for GeeSFS to make
// the FUSE mount appear; the hook's own response timeout is longer so geesefsd
// always gets to report FAILED first.
const (
	mountTimeout = 90 * time.Second
	pollInterval = 500 * time.Millisecond
	// stopTimeout bounds how long a graceful GeeSFS SIGTERM is given before we
	// SIGKILL it during cleanup.
	stopTimeout = 10 * time.Second
)

// geesefsFSType is what findmnt reports for a live GeeSFS mount.
const geesefsFSType = "fuse.geesefs"

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "geesefsd: "+format+"\n", args...)
}

// Run is the entry point for the geesefsd role.
func Run(args []string) int {
	if len(args) > 0 && args[0] == selftestFlag {
		return selftest()
	}

	cmd := "daemon"
	if len(args) > 0 {
		cmd = args[0]
	}

	switch cmd {
	case "daemon":
		return daemon()
	default:
		logf("usage: geesefsd [daemon]")
		return 2
	}
}

func selftest() int {
	code := 0
	for _, tool := range []string{"geesefs", "nsenter", "findmnt"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			logf("selftest: %s not found on PATH: %v", tool, err)
			code = 1
			continue
		}
		logf("selftest: found %s=%s", tool, p)
	}
	if code == 0 {
		logf("selftest ok")
	}
	return code
}

// daemon runs the long-lived supervisor: it listens on the unix socket and
// serves one mount request per connection until it is signalled to stop.
func daemon() int {
	socket := protocol.SocketPath()
	// A stale socket from a previous run would make Listen fail with EADDRINUSE.
	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		logf("FATAL: removing stale socket %s: %v", socket, err)
		return 1
	}
	l, err := net.Listen("unix", socket)
	if err != nil {
		logf("FATAL: listening on %s: %v", socket, err)
		return 1
	}
	defer l.Close()

	// Root-only: a client of this socket can ask geesefsd to setns() into an
	// arbitrary pid and mount there, so only root (the hook's context) may reach
	// it. Never widen these permissions.
	if err := os.Chmod(socket, 0o600); err != nil {
		logf("FATAL: securing socket %s: %v", socket, err)
		return 1
	}

	logf("supervisor listening on %s (default mount path: %s)", socket, contract.DefaultMountPath)

	srv := &server{reg: newRegistry(), mounter: realMounter{}}

	// A signal closes the listener, which unblocks Accept and ends the loop.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		s := <-sig
		logf("received %s, shutting down", s)
		l.Close()
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			// Listener closed (shutdown) — normal exit.
			break
		}
		go srv.handle(conn)
	}
	return 0
}

// server serves mount requests. mounter is an interface so the request-handling
// logic (validation, idempotency, state) can be tested without setns/geesefs.
type server struct {
	reg     *registry
	mounter mounter
}

// handle serves one connection: read a request, act, write the response.
func (s *server) handle(conn net.Conn) {
	defer conn.Close()

	req, err := protocol.ReadRequest(conn)
	if err != nil {
		logf("bad request: %v", err)
		_ = protocol.WriteResponse(conn, protocol.Failed(fmt.Errorf("unreadable request: %w", err)))
		return
	}
	resp := s.serve(req)
	if err := protocol.WriteResponse(conn, resp); err != nil {
		logf("writing response for %s: %v", req.ID, err)
	}
}

// serve is the core: it validates the request and dispatches to the mount or
// unmount handler. Validation covers transport invariants and (for a mount) the
// policy, re-checked here as defense in depth since this is the privileged
// component.
func (s *server) serve(req protocol.Request) protocol.Response {
	if err := req.Validate(); err != nil {
		return protocol.Failed(err)
	}
	switch req.Op {
	case protocol.OpMount:
		return s.serveMount(req)
	case protocol.OpUnmount:
		return s.serveUnmount(req)
	default:
		// Unreachable: Validate already rejected unknown operations.
		return protocol.Failed(fmt.Errorf("unsupported operation %q", req.Op))
	}
}

// serveMount establishes the mount for req, idempotently, and records it.
func (s *server) serveMount(req protocol.Request) protocol.Response {
	if err := policy.Validate(policy.Policy{Bucket: req.Bucket, Mount: req.Mount, Endpoint: req.Endpoint}); err != nil {
		logf("rejecting mount for %s: %v", req.ID, err)
		return protocol.Failed(err)
	}

	if _, ok := s.reg.get(req.ID); ok {
		logf("container %s already mounted (idempotent)", req.ID)
		return protocol.Response{Status: protocol.StatusReady}
	}

	logf("mounting bucket=%s at %s in container %s (pid %d)", req.Bucket, req.Mount, req.ID, req.PID)
	proc, err := s.mounter.Mount(req)
	if err != nil {
		logf("mount failed for %s: %v", req.ID, err)
		return protocol.Failed(err)
	}

	s.reg.add(&mountState{req: req, proc: proc})
	logf("mount ready for %s", req.ID)
	return protocol.Response{Status: protocol.StatusReady}
}

// serveUnmount reaps the GeeSFS process for req's container and drops its state.
// It is idempotent: unmounting an unknown (or already-cleaned) container is a
// no-op success, so a duplicate poststop never fails. Only this container's own
// entry is touched — the per-id keying is what keeps containers isolated.
func (s *server) serveUnmount(req protocol.Request) protocol.Response {
	st, ok := s.reg.remove(req.ID)
	if !ok {
		logf("no mount to clean up for %s (idempotent)", req.ID)
		return protocol.Response{Status: protocol.StatusReady}
	}
	if err := s.mounter.Unmount(st); err != nil {
		// State is already dropped; report the failure but do not resurrect it.
		logf("cleanup for %s reported: %v", req.ID, err)
		return protocol.Failed(err)
	}
	logf("cleaned up %s", req.ID)
	return protocol.Response{Status: protocol.StatusReady}
}

// mounter establishes and tears down a single container's mount. Mount returns
// the GeeSFS process handle so the supervisor can reap it on Unmount. It is an
// interface so the request-handling logic can be tested without setns/geesefs.
type mounter interface {
	Mount(req protocol.Request) (*os.Process, error)
	Unmount(st *mountState) error
}

// realMounter performs the actual setns()+mount by shelling out to nsenter and
// geesefs, then polling findmnt until the FUSE mount is live.
type realMounter struct{}

func (realMounter) Mount(req protocol.Request) (*os.Process, error) {
	cmd := exec.Command("nsenter", geesefsArgs(req)...)
	// GeeSFS reads S3 credentials from the environment (AWS_ACCESS_KEY_ID etc.),
	// which geesefsd inherits from its systemd unit — they never touch argv.
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting geesefs: %w", err)
	}

	if err := waitForMount(req); err != nil {
		// Mount never came up: don't leak the GeeSFS process.
		stopProcess(cmd.Process, stopTimeout)
		return nil, err
	}
	return cmd.Process, nil
}

// Unmount reaps the container's GeeSFS process. A clean SIGTERM lets GeeSFS
// unmount the FUSE filesystem itself; when its process is gone, so is the mount
// (which only ever existed in the container's mount namespace, never on the
// host). By poststop time the namespace is usually already torn down, so this
// is best-effort reaping rather than an explicit umount.
func (realMounter) Unmount(st *mountState) error {
	if st.proc == nil {
		return nil
	}
	stopProcess(st.proc, stopTimeout)
	return nil
}

// stopProcess asks proc to exit (SIGTERM), waits up to timeout, then SIGKILLs
// if it is still alive, and reaps it either way.
func stopProcess(proc *os.Process, timeout time.Duration) {
	_ = proc.Signal(syscall.SIGTERM)

	done := make(chan struct{})
	go func() {
		_, _ = proc.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		_ = proc.Kill()
		<-done
	}
}

// waitForMount polls findmnt inside the container mount namespace until the
// GeeSFS FUSE mount appears or the bounded timeout elapses (fail closed).
func waitForMount(req protocol.Request) error {
	deadline := time.Now().Add(mountTimeout)
	for {
		out, _ := exec.Command("nsenter", findmntArgs(req.PID, req.Mount)...).Output()
		if mountReady(string(out)) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("mount at %s not ready after %s", req.Mount, mountTimeout)
		}
		time.Sleep(pollInterval)
	}
}

// --- pure helpers (table-tested) -------------------------------------------

// geesefsArgs builds the nsenter argv that launches GeeSFS inside the target
// container's MOUNT namespace only (-m), leaving the network namespace as the
// host's so the S3 endpoint stays reachable. GeeSFS runs in the foreground (-f)
// so geesefsd owns its lifetime.
func geesefsArgs(req protocol.Request) []string {
	return []string{
		"-t", strconv.Itoa(req.PID), "-m", "--",
		"geesefs", "-f", "--endpoint", req.Endpoint, req.Bucket, req.Mount,
	}
}

// findmntArgs builds the nsenter argv that queries the mount's filesystem type
// inside the container mount namespace.
func findmntArgs(pid int, mount string) []string {
	return []string{
		"-t", strconv.Itoa(pid), "-m", "--",
		"findmnt", "-n", "-o", "FSTYPE", mount,
	}
}

// mountReady reports whether findmnt's FSTYPE output names a live GeeSFS mount.
func mountReady(findmntOutput string) bool {
	return strings.TrimSpace(findmntOutput) == geesefsFSType
}
