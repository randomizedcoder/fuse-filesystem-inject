package hook

import (
	"strings"
	"testing"
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
