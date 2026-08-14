package hook

import (
	"strings"
	"testing"

	"github.com/randomizedcoder/fuse-filesystem-inject/internal/protocol"
)

func TestReadState(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantID  string
		wantPID int
		wantErr bool
	}{
		{
			name:    "minimal",
			input:   `{"id":"c1","pid":1234}`,
			wantID:  "c1",
			wantPID: 1234,
		},
		{
			name:    "full oci state with extra fields ignored",
			input:   `{"ociVersion":"1.0.2","id":"abc","status":"created","pid":99,"bundle":"/run/b","annotations":{"geesefs.enabled":"true"}}`,
			wantID:  "abc",
			wantPID: 99,
		},
		{
			name:    "missing pid decodes to zero (caller validates)",
			input:   `{"id":"c1"}`,
			wantID:  "c1",
			wantPID: 0,
		},
		{
			name:    "not json",
			input:   `not json`,
			wantErr: true,
		},
		{
			name:    "empty",
			input:   ``,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, err := readState(strings.NewReader(tt.input))
			if (err != nil) != tt.wantErr {
				t.Fatalf("readState err=%v, wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if st.ID != tt.wantID || st.PID != tt.wantPID {
				t.Errorf("got id=%q pid=%d, want id=%q pid=%d", st.ID, st.PID, tt.wantID, tt.wantPID)
			}
		})
	}
}

func TestBuildRequest(t *testing.T) {
	const state = `{"id":"c1","pid":1234}`
	tests := []struct {
		name    string
		args    []string
		stdin   string
		want    protocol.Request
		wantErr bool
	}{
		{
			name:  "mount",
			args:  []string{"mount", "models", "/models", "http://127.0.0.1:9000"},
			stdin: state,
			want:  protocol.Request{Op: protocol.OpMount, ID: "c1", PID: 1234, Bucket: "models", Mount: "/models", Endpoint: "http://127.0.0.1:9000"},
		},
		{
			name:  "unmount ignores pid",
			args:  []string{"unmount"},
			stdin: `{"id":"c1"}`,
			want:  protocol.Request{Op: protocol.OpUnmount, ID: "c1"},
		},
		{"mount missing policy args", []string{"mount", "models"}, state, protocol.Request{}, true},
		{"unknown op", []string{"frobnicate"}, state, protocol.Request{}, true},
		{"mount with empty id from state fails validation", []string{"mount", "models", "/models", "http://x"}, `{"pid":5}`, protocol.Request{}, true},
		{"unmount with empty id fails validation", []string{"unmount"}, `{"pid":5}`, protocol.Request{}, true},
		{"bad state json", []string{"unmount"}, `nope`, protocol.Request{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildRequest(tt.args, strings.NewReader(tt.stdin))
			if (err != nil) != tt.wantErr {
				t.Fatalf("buildRequest err=%v, wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got != tt.want {
				t.Errorf("buildRequest =\n %+v\nwant\n %+v", got, tt.want)
			}
		})
	}
}
