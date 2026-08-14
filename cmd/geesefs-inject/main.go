// Command geesefs-inject is a single multi-call binary that implements every
// role in the GeeSFS transparent-injection toolchain:
//
//	geesefs-runc  — the OCI-config injector Docker/containerd invoke as runc
//	geesefsd      — the host-side mount supervisor (systemd service)
//	geesefs-hook  — the createRuntime hook runc calls per matched container
//
// It dispatches on the basename of argv[0] (busybox style); the Nix build
// installs one thin wrapper per role that sets argv[0] accordingly. Invoked
// directly as "geesefs-inject", the first positional argument selects the role.
// All real logic lives in internal/*, shared across roles so nothing is
// duplicated between them.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/randomizedcoder/fuse-filesystem-inject/internal/contract"
	"github.com/randomizedcoder/fuse-filesystem-inject/internal/hook"
	"github.com/randomizedcoder/fuse-filesystem-inject/internal/runc"
	"github.com/randomizedcoder/fuse-filesystem-inject/internal/supervisor"
)

func main() {
	role, args := dispatch(os.Args)

	var code int
	switch role {
	case contract.RoleRunc:
		code = runc.Run(args)
	case contract.RoleSupervisor:
		code = supervisor.Run(args)
	case contract.RoleHook:
		code = hook.Run(args)
	default:
		fmt.Fprintf(os.Stderr,
			"geesefs-inject: unknown role %q (expected %s, %s, or %s)\n",
			role, contract.RoleRunc, contract.RoleSupervisor, contract.RoleHook)
		code = 2
	}
	os.Exit(code)
}

// dispatch resolves the role and the arguments to pass it. The role is the
// basename of argv[0]; when the binary is invoked under its own name
// ("geesefs-inject") the first positional argument is used instead.
func dispatch(argv []string) (role string, args []string) {
	base := filepath.Base(argv[0])
	rest := argv[1:]
	if base == "geesefs-inject" || base == "." || base == "/" {
		if len(rest) > 0 {
			return rest[0], rest[1:]
		}
		return "", nil
	}
	return base, rest
}
