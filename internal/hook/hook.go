// Package hook implements the geesefs-hook role: the OCI createRuntime hook
// runc invokes (in the host/runtime namespace, after the container namespaces
// exist) for a matched container. It reads the container State from stdin,
// asks geesefsd to establish the mount, and blocks until the mount is ready.
//
// Any failure returns a non-zero exit code, which fails container startup — the
// mount-before-app / fail-closed contract from docs/injection-lifecycle.md:
// starting the application over an empty mount is more dangerous than refusing
// to start.
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

// Run is the entry point for the geesefs-hook role. args carries the policy the
// spec's hook registration passed positionally: bucket, mount, endpoint.
func Run(args []string) int {
	if len(args) != 3 {
		logf("usage: geesefs-hook <bucket> <mount> <endpoint> (OCI State on stdin)")
		return 2
	}
	bucket, mount, endpoint := args[0], args[1], args[2]

	state, err := readState(os.Stdin)
	if err != nil {
		logf("FATAL: reading OCI state from stdin: %v", err)
		return 1
	}

	req := protocol.MountRequest{
		ID:       state.ID,
		PID:      state.PID,
		Bucket:   bucket,
		Mount:    mount,
		Endpoint: endpoint,
	}
	if err := req.Validate(); err != nil {
		logf("FATAL: %v", err)
		return 1
	}

	resp, err := requestMount(protocol.SocketPath(), req)
	if err != nil {
		logf("FATAL: requesting mount from geesefsd: %v", err)
		return 1
	}
	if resp.Status != protocol.StatusReady {
		logf("FATAL: geesefsd did not establish the mount: %s", resp.Error)
		return 1
	}

	logf("mount ready: bucket=%s mount=%s (container %s)", bucket, mount, state.ID)
	return 0
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

// requestMount connects to geesefsd, sends req, and returns its response. The
// connection deadline bounds the whole exchange so the hook never blocks
// container startup indefinitely on an unreachable supervisor.
func requestMount(socket string, req protocol.MountRequest) (protocol.MountResponse, error) {
	conn, err := net.DialTimeout("unix", socket, dialTimeout)
	if err != nil {
		return protocol.MountResponse{}, fmt.Errorf("dial %s: %w", socket, err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(responseTimeout)); err != nil {
		return protocol.MountResponse{}, err
	}
	if err := protocol.WriteRequest(conn, req); err != nil {
		return protocol.MountResponse{}, fmt.Errorf("send request: %w", err)
	}
	resp, err := protocol.ReadResponse(conn)
	if err != nil {
		return protocol.MountResponse{}, fmt.Errorf("read response: %w", err)
	}
	return resp, nil
}
