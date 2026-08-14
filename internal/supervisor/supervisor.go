// Package supervisor implements the geesefsd role: the host-side GeeSFS mount
// supervisor that runs as a systemd service on the VM. It owns the operational
// lifecycle of each injected mount — entering the target container's mount
// namespace, starting GeeSFS, verifying readiness, and cleaning up on exit.
//
// Phase 1 (this pass) is behavior-preserving relative to the original shell
// stub: `daemon` runs an idle supervisor loop so the unit stays Up, and `mount`
// logs the intended nsenter+mount and fails closed. The real setns()+mount is
// Phase 4.
package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/randomizedcoder/fuse-filesystem-inject/internal/contract"
)

const selftestFlag = "--geesefs-selftest"

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "geesefsd: "+format+"\n", args...)
}

// Run is the entry point for the geesefsd role.
func Run(args []string) int {
	if len(args) > 0 && args[0] == selftestFlag {
		return selftest()
	}

	cmd := "daemon"
	if len(args) > 0 {
		cmd = args[0]
	}

	switch cmd {
	case "daemon":
		return daemon()
	case "mount":
		return mountStub(args[1:])
	default:
		logf("usage: geesefsd (daemon | mount <id> <bucket> <mount> <endpoint>)")
		return 2
	}
}

func selftest() int {
	code := 0
	for _, tool := range []string{"geesefs", "nsenter"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			logf("selftest: %s not found on PATH: %v", tool, err)
			code = 1
			continue
		}
		logf("selftest: found %s=%s", tool, p)
	}
	if code == 0 {
		logf("selftest ok")
	}
	return code
}

// daemon runs the long-lived supervisor. Phase 1 keeps the unit Up until
// systemd stops it (SIGTERM/SIGINT); Phase 4 replaces the idle wait with the
// hook listener + mount lifecycle.
func daemon() int {
	logf("supervisor started (default mount path: %s)", contract.DefaultMountPath)
	logf("STUB: real hook listener + setns mount lifecycle not implemented yet")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	s := <-sig
	logf("received %s, shutting down", s)
	return 0
}

// mountStub logs the mount request it would service and fails closed, so
// container startup does not proceed over an empty mount before Phase 4 lands.
func mountStub(args []string) int {
	get := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	id, bucket, mount, endpoint := get(0), get(1), get(2), get(3)

	logf("mount request: id=%s bucket=%s mount=%s endpoint=%s", id, bucket, mount, endpoint)
	logf("WOULD: nsenter -t <container-pid> -m -- geesefs --endpoint %s %s %s", endpoint, bucket, mount)
	logf("       then poll findmnt until the fuse.geesefs mount is ready.")
	logf("STUB: setns()+mount not implemented; failing so startup does not")
	logf("      proceed over an empty %s (see docs/injection-lifecycle.md).", mount)
	return 1
}
