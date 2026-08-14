// Package hook implements the geesefs-hook role: the OCI hooks runc invokes (in
// the host/runtime namespace) for a matched container. geesefs-runc registers
// it twice, distinguished by its first argument:
//
//	createRuntime → "mount <bucket> <mount> <endpoint>": ask geesefsd to
//	                establish the mount and block until it is ready. Any failure
//	                returns non-zero, which fails container startup — the
//	                mount-before-app / fail-closed contract from
//	                docs/injection-lifecycle.md (starting the application over an
//	                empty mount is more dangerous than refusing to start).
//	poststop      → "unmount": ask geesefsd to reap GeeSFS and drop the mount's
//	                state once the container has exited.
//
// Both read the OCI container State from stdin for the container id (and, for
// mount, the init pid).
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/randomizedcoder/fuse-filesystem-inject/internal/protocol"
)

// dialTimeout bounds how long we wait to reach geesefsd; responseTimeout bounds
// the whole request (geesefsd does the readiness poll and must reply first, so
// this is deliberately longer than the supervisor's own mount timeout).
const (
	dialTimeout     = 5 * time.Second
	responseTimeout = 120 * time.Second
)

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "geesefs-hook: "+format+"\n", args...)
}

// ociState is the subset of the OCI runtime State (runtime-spec state.go) the
// hook needs: the container id keys geesefsd's state, and pid is the container
// init process geesefsd setns()es into.
type ociState struct {
	ID  string `json:"id"`
	PID int    `json:"pid"`
}

// Run is the entry point for the geesefs-hook role. args[0] selects the
// operation ("mount" or "unmount"); a mount also carries the policy positionally
// (bucket, mount, endpoint), as registered by geesefs-runc.
func Run(args []string) int {
	if len(args) == 0 {
		logf("usage: geesefs-hook (mount <bucket> <mount> <endpoint> | unmount) (OCI State on stdin)")
		return 2
	}

	req, err := buildRequest(args, os.Stdin)
	if err != nil {
		logf("FATAL: %v", err)
		return 1
	}

	resp, err := request(protocol.SocketPath(), req)
	if err != nil {
		logf("FATAL: %s request to geesefsd: %v", req.Op, err)
		return 1
	}
	if resp.Status != protocol.StatusReady {
		logf("FATAL: geesefsd %s failed: %s", req.Op, resp.Error)
		return 1
	}

	logf("%s ok (container %s)", req.Op, req.ID)
	return 0
}

// buildRequest assembles the geesefsd request from the hook's argv and the OCI
// State on stdin, and validates it.
func buildRequest(args []string, stdin io.Reader) (protocol.Request, error) {
	state, err := readState(stdin)
	if err != nil {
		return protocol.Request{}, fmt.Errorf("reading OCI state from stdin: %w", err)
	}

	var req protocol.Request
	switch op := protocol.Operation(args[0]); op {
	case protocol.OpMount:
		if len(args) != 4 {
			return protocol.Request{}, fmt.Errorf("mount needs <bucket> <mount> <endpoint>")
		}
		req = protocol.Request{
			Op:       op,
			ID:       state.ID,
			PID:      state.PID,
			Bucket:   args[1],
			Mount:    args[2],
			Endpoint: args[3],
		}
	case protocol.OpUnmount:
		req = protocol.Request{Op: op, ID: state.ID}
	default:
		return protocol.Request{}, fmt.Errorf("unknown operation %q", args[0])
	}

	if err := req.Validate(); err != nil {
		return protocol.Request{}, err
	}
	return req, nil
}

// readState decodes the OCI State JSON runc writes to the hook's stdin.
func readState(r io.Reader) (ociState, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return ociState{}, err
	}
	var st ociState
	if err := json.Unmarshal(data, &st); err != nil {
		return ociState{}, fmt.Errorf("invalid OCI state JSON: %w", err)
	}
	return st, nil
}

// request connects to geesefsd, sends req, and returns its response. The
// connection deadline bounds the whole exchange so the hook never blocks
// container startup indefinitely on an unreachable supervisor.
func request(socket string, req protocol.Request) (protocol.Response, error) {
	conn, err := net.DialTimeout("unix", socket, dialTimeout)
	if err != nil {
		return protocol.Response{}, fmt.Errorf("dial %s: %w", socket, err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(responseTimeout)); err != nil {
		return protocol.Response{}, err
	}
	if err := protocol.WriteRequest(conn, req); err != nil {
		return protocol.Response{}, fmt.Errorf("send request: %w", err)
	}
	resp, err := protocol.ReadResponse(conn)
	if err != nil {
		return protocol.Response{}, fmt.Errorf("read response: %w", err)
	}
	return resp, nil
}
