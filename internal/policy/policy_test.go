package policy

import "testing"

// ann builds an annotation map for the common enabled case.
func ann(bucket, mount, endpoint string) map[string]string {
	return map[string]string{
		"geesefs.enabled":  "true",
		"geesefs.bucket":   bucket,
		"geesefs.mount":    mount,
		"geesefs.endpoint": endpoint,
	}
}

func TestParse(t *testing.T) {
	const (
		okBucket   = "models"
		okMount    = "/models"
		okEndpoint = "http://127.0.0.1:9000"
	)

	tests := []struct {
		name        string
		annotations map[string]string
		wantMatched bool
		wantErr     bool
		want        Policy // only checked when wantMatched && !wantErr
	}{
		// --- pass-through (not opted in) --------------------------------------
		{
			name:        "nil annotations",
			annotations: nil,
			wantMatched: false,
		},
		{
			name:        "enabled absent",
			annotations: map[string]string{"geesefs.bucket": "models"},
			wantMatched: false,
		},
		{
			name:        "enabled not exactly true",
			annotations: map[string]string{"geesefs.enabled": "1"},
			wantMatched: false,
		},

		// --- valid ------------------------------------------------------------
		{
			name:        "valid https endpoint with port",
			annotations: ann(okBucket, okMount, "https://minio.example.com:9000"),
			wantMatched: true,
			want:        Policy{Bucket: okBucket, Mount: okMount, Endpoint: "https://minio.example.com:9000"},
		},
		{
			name:        "valid with trailing-slash endpoint path",
			annotations: ann("my-bucket", "/data/models", "http://127.0.0.1:9000/"),
			wantMatched: true,
			want:        Policy{Bucket: "my-bucket", Mount: "/data/models", Endpoint: "http://127.0.0.1:9000/"},
		},

		// --- invalid bucket ---------------------------------------------------
		{name: "bucket empty", annotations: ann("", okMount, okEndpoint), wantMatched: true, wantErr: true},
		{name: "bucket uppercase", annotations: ann("Models", okMount, okEndpoint), wantMatched: true, wantErr: true},
		{name: "bucket with slash", annotations: ann("a/b", okMount, okEndpoint), wantMatched: true, wantErr: true},
		{name: "bucket with space", annotations: ann("my bucket", okMount, okEndpoint), wantMatched: true, wantErr: true},
		{name: "bucket shell metachars", annotations: ann("a;rm -rf", okMount, okEndpoint), wantMatched: true, wantErr: true},
		{name: "bucket too short", annotations: ann("ab", okMount, okEndpoint), wantMatched: true, wantErr: true},
		{name: "bucket consecutive dots", annotations: ann("a..b", okMount, okEndpoint), wantMatched: true, wantErr: true},

		// --- invalid mount (escape / malformed) -------------------------------
		{name: "mount empty", annotations: ann(okBucket, "", okEndpoint), wantMatched: true, wantErr: true},
		{name: "mount relative", annotations: ann(okBucket, "models", okEndpoint), wantMatched: true, wantErr: true},
		{name: "mount traversal", annotations: ann(okBucket, "/models/../../etc", okEndpoint), wantMatched: true, wantErr: true},
		{name: "mount dotdot component", annotations: ann(okBucket, "/../etc", okEndpoint), wantMatched: true, wantErr: true},
		{name: "mount trailing slash", annotations: ann(okBucket, "/models/", okEndpoint), wantMatched: true, wantErr: true},
		{name: "mount doubled separator", annotations: ann(okBucket, "/models//data", okEndpoint), wantMatched: true, wantErr: true},
		{name: "mount is root", annotations: ann(okBucket, "/", okEndpoint), wantMatched: true, wantErr: true},

		// --- invalid endpoint -------------------------------------------------
		{name: "endpoint empty", annotations: ann(okBucket, okMount, ""), wantMatched: true, wantErr: true},
		{name: "endpoint bad scheme", annotations: ann(okBucket, okMount, "ftp://host:21"), wantMatched: true, wantErr: true},
		{name: "endpoint no scheme", annotations: ann(okBucket, okMount, "127.0.0.1:9000"), wantMatched: true, wantErr: true},
		{name: "endpoint with credentials", annotations: ann(okBucket, okMount, "http://user:pass@host:9000"), wantMatched: true, wantErr: true},
		{name: "endpoint with path", annotations: ann(okBucket, okMount, "http://host:9000/bucket"), wantMatched: true, wantErr: true},
		{name: "endpoint with query", annotations: ann(okBucket, okMount, "http://host:9000/?x=1"), wantMatched: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, matched, err := Parse(tt.annotations)

			if matched != tt.wantMatched {
				t.Fatalf("matched = %v, want %v", matched, tt.wantMatched)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantMatched && !tt.wantErr && got != tt.want {
				t.Errorf("policy = %+v, want %+v", got, tt.want)
			}
		})
	}
}
