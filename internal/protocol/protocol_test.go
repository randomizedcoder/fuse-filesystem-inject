package protocol

import (
	"bytes"
	"strings"
	"testing"
)

func TestRequestRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		req  MountRequest
	}{
		{"typical", MountRequest{ID: "abc123", PID: 4242, Bucket: "models", Mount: "/models", Endpoint: "http://127.0.0.1:9000"}},
		{"zero", MountRequest{}},
		{"unicode-and-spaces", MountRequest{ID: "id with spaces", PID: 1, Bucket: "b", Mount: "/mnt/data", Endpoint: "https://s3.example"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteRequest(&buf, tt.req); err != nil {
				t.Fatalf("WriteRequest: %v", err)
			}
			if !strings.HasSuffix(buf.String(), "\n") {
				t.Errorf("encoded request is not newline-terminated: %q", buf.String())
			}
			got, err := ReadRequest(&buf)
			if err != nil {
				t.Fatalf("ReadRequest: %v", err)
			}
			if got != tt.req {
				t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, tt.req)
			}
		})
	}
}

func TestResponseRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		resp MountResponse
	}{
		{"ready", MountResponse{Status: StatusReady}},
		{"failed", Failed(errString("mount timed out"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteResponse(&buf, tt.resp); err != nil {
				t.Fatalf("WriteResponse: %v", err)
			}
			got, err := ReadResponse(&buf)
			if err != nil {
				t.Fatalf("ReadResponse: %v", err)
			}
			if got != tt.resp {
				t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, tt.resp)
			}
		})
	}
}

// TestReadNoTrailingNewline covers a peer that writes the JSON then closes the
// connection without a newline — still a complete message.
func TestReadNoTrailingNewline(t *testing.T) {
	got, err := ReadResponse(strings.NewReader(`{"status":"READY"}`))
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if got.Status != StatusReady {
		t.Errorf("got %+v, want READY", got)
	}
}

func TestReadEmptyIsError(t *testing.T) {
	if _, err := ReadRequest(strings.NewReader("")); err == nil {
		t.Fatal("expected an error reading an empty stream, got nil")
	}
}

func TestRequestValidate(t *testing.T) {
	tests := []struct {
		name    string
		req     MountRequest
		wantErr bool
	}{
		{"ok", MountRequest{ID: "x", PID: 1}, false},
		{"empty id", MountRequest{ID: "", PID: 1}, true},
		{"zero pid", MountRequest{ID: "x", PID: 0}, true},
		{"negative pid", MountRequest{ID: "x", PID: -3}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

type errString string

func (e errString) Error() string { return string(e) }
