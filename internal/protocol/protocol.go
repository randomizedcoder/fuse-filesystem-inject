// Package protocol defines the wire contract between the createRuntime hook
// (client) and geesefsd (server): a single newline-delimited JSON MountRequest
// in, a single MountResponse out, over a unix socket.
//
// The hook sends one request per matched container and blocks on the response;
// geesefsd establishes the mount and replies READY or FAILED. Keeping the codec
// here (rather than inline in each role) means the two ends can never disagree
// on the framing, and the pure encode/decode functions are trivially testable.
package protocol

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/randomizedcoder/fuse-filesystem-inject/internal/contract"
)

// Status is the outcome geesefsd reports for a mount request.
type Status string

const (
	StatusReady  Status = "READY"
	StatusFailed Status = "FAILED"
)

// MountRequest asks geesefsd to establish one GeeSFS mount. ID keys the
// supervisor's per-container state; PID is the container init process geesefsd
// setns()es into. The remaining fields are the validated policy.
type MountRequest struct {
	ID       string `json:"id"`
	PID      int    `json:"pid"`
	Bucket   string `json:"bucket"`
	Mount    string `json:"mount"`
	Endpoint string `json:"endpoint"`
}

// MountResponse is geesefsd's reply. Error is set only when Status is FAILED.
type MountResponse struct {
	Status Status `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Validate checks the transport-level invariants of a request (identity and a
// usable pid). It does NOT validate the policy fields — that is policy.Validate,
// which geesefsd applies separately as defense in depth.
func (r MountRequest) Validate() error {
	switch {
	case r.ID == "":
		return fmt.Errorf("mount request is missing a container id")
	case r.PID <= 0:
		return fmt.Errorf("mount request has invalid pid %d", r.PID)
	}
	return nil
}

// Failed builds a FAILED response carrying err's message.
func Failed(err error) MountResponse {
	return MountResponse{Status: StatusFailed, Error: err.Error()}
}

// WriteRequest encodes req as one JSON line.
func WriteRequest(w io.Writer, req MountRequest) error {
	return writeLine(w, req)
}

// ReadRequest reads one JSON-line request.
func ReadRequest(r io.Reader) (MountRequest, error) {
	var req MountRequest
	err := readLine(r, &req)
	return req, err
}

// WriteResponse encodes resp as one JSON line.
func WriteResponse(w io.Writer, resp MountResponse) error {
	return writeLine(w, resp)
}

// ReadResponse reads one JSON-line response.
func ReadResponse(r io.Reader) (MountResponse, error) {
	var resp MountResponse
	err := readLine(r, &resp)
	return resp, err
}

func writeLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

func readLine(r io.Reader, v any) error {
	line, err := bufio.NewReader(r).ReadBytes('\n')
	// A message with no trailing newline (peer closed after writing) is still
	// valid as long as we got bytes; only surface a read error on empty input.
	if err != nil && (err != io.EOF || len(line) == 0) {
		return err
	}
	return json.Unmarshal(line, v)
}

// SocketPath returns the unix socket geesefsd listens on and the hook dials.
// It honours the SocketEnv override (used by tests) and otherwise uses the
// well-known contract path.
func SocketPath() string {
	if p := os.Getenv(contract.SocketEnv); p != "" {
		return p
	}
	return contract.SupervisorSocket
}
