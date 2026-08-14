package cli

import "testing"

func TestParseRunc(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want RuncInvocation
	}{
		{
			name: "empty",
			args: nil,
			want: RuncInvocation{},
		},
		{
			name: "create with long bundle after subcommand",
			args: []string{"create", "--bundle", "/run/bundle", "cid"},
			want: RuncInvocation{Subcommand: "create", Bundle: "/run/bundle"},
		},
		{
			name: "bundle before subcommand is not mistaken for it",
			args: []string{"--bundle", "/run/bundle", "create", "cid"},
			want: RuncInvocation{Subcommand: "create", Bundle: "/run/bundle"},
		},
		{
			name: "short -b form",
			args: []string{"create", "-b", "/x", "cid"},
			want: RuncInvocation{Subcommand: "create", Bundle: "/x"},
		},
		{
			name: "--bundle=value form",
			args: []string{"create", "--bundle=/y", "cid"},
			want: RuncInvocation{Subcommand: "create", Bundle: "/y"},
		},
		{
			name: "-b=value form",
			args: []string{"create", "-b=/z", "cid"},
			want: RuncInvocation{Subcommand: "create", Bundle: "/z"},
		},
		{
			name: "global --root value is not the subcommand (docker-style)",
			args: []string{"--root", "/run/docker/runtime-runc/moby", "create", "--bundle", "/b", "cid"},
			want: RuncInvocation{Subcommand: "create", Bundle: "/b"},
		},
		{
			name: "multiple global value flags before subcommand",
			args: []string{"--log", "/l", "--log-format", "json", "--systemd-cgroup", "create", "-b", "/b", "cid"},
			want: RuncInvocation{Subcommand: "create", Bundle: "/b"},
		},
		{
			name: "subcommand with no bundle",
			args: []string{"state", "cid"},
			want: RuncInvocation{Subcommand: "state"},
		},
		{
			name: "boolean flag after subcommand",
			args: []string{"delete", "--force", "cid"},
			want: RuncInvocation{Subcommand: "delete"},
		},
		{
			name: "trailing --bundle with no value does not panic",
			args: []string{"create", "--bundle"},
			want: RuncInvocation{Subcommand: "create"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseRunc(tt.args)
			if got != tt.want {
				t.Errorf("ParseRunc(%q) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}
