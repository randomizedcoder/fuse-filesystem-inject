// Package protocol defines the wire contract between the geesefs-hook client
// and geesefsd: a single newline-delimited JSON Request in, a single Response
// out, over a unix socket.
//
// The hook fires twice per matched container — a mount Request from the
// createRuntime hook and an unmount Request from the poststop hook — and blocks
// on the response each time; geesefsd acts and replies READY or FAILED. Keeping
// the codec here (rather than inline in each role) means the two ends can never
// disagree on the framing, and the pure encode/decode functions are trivially
// testable.
package protocol

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/randomizedcoder/fuse-filesystem-inject/internal/contract"
)

// Operation is what geesefsd is being asked to do for a container.
type Operation string

const (
	OpMount   Operation = "mount"
	OpUnmount Operation = "unmount"
)

// Status is the outcome geesefsd reports for a request.
type Status string

const (
	StatusReady  Status = "READY"
	StatusFailed Status = "FAILED"
)

// Request is one instruction to geesefsd. ID keys the supervisor's per-container
// state and is required for every operation. For OpMount, PID is the container
// init process geesefsd setns()es into and the remaining fields are the
// validated policy; for OpUnmount only Op and ID are meaningful.
type Request struct {
	Op       Operation `json:"op"`
	ID       string    `json:"id"`
	PID      int       `json:"pid,omitempty"`
	Bucket   string    `json:"bucket,omitempty"`
	Mount    string    `json:"mount,omitempty"`
	Endpoint string    `json:"endpoint,omitempty"`
}

// Response is geesefsd's reply. Error is set only when Status is FAILED.
type Response struct {
	Status Status `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Validate checks the transport-level invariants of a request (a known
// operation, an identity, and — for a mount — a usable pid). It does NOT
// validate the policy fields; that is policy.Validate, which geesefsd applies
// separately as defense in depth.
func (r Request) Validate() error {
	switch r.Op {
	case OpMount, OpUnmount:
	default:
		return fmt.Errorf("unknown operation %q", r.Op)
	}
	if r.ID == "" {
		return fmt.Errorf("%s request is missing a container id", r.Op)
	}
	if r.Op == OpMount && r.PID <= 0 {
		return fmt.Errorf("mount request has invalid pid %d", r.PID)
	}
	return nil
}

// Failed builds a FAILED response carrying err's message.
func Failed(err error) Response {
	return Response{Status: StatusFailed, Error: err.Error()}
}

// WriteRequest encodes req as one JSON line.
func WriteRequest(w io.Writer, req Request) error {
	return writeLine(w, req)
}

// ReadRequest reads one JSON-line request.
func ReadRequest(r io.Reader) (Request, error) {
	var req Request
	err := readLine(r, &req)
	return req, err
}

// WriteResponse encodes resp as one JSON line.
func WriteResponse(w io.Writer, resp Response) error {
	return writeLine(w, resp)
}

// ReadResponse reads one JSON-line response.
func ReadResponse(r io.Reader) (Response, error) {
	var resp Response
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
