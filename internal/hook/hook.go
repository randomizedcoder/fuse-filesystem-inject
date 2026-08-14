// Package hook implements the geesefs-hook role: the OCI createRuntime hook
// runc invokes (in the host/runtime namespace, after the container namespaces
// exist) for a matched container. It reads the container state from stdin,
// asks geesefsd to establish the mount, and blocks until the mount is ready —
// a non-zero exit fails container startup (the mount-before-app contract).
//
// Phase 1 (this pass) registers nothing yet (geesefs-runc does not mutate the
// spec until Phase 3), so this role is not invoked. It is a no-op placeholder
// wired for dispatch; the real implementation is Phase 4.
package hook

import (
	"fmt"
	"os"
)

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "geesefs-hook: "+format+"\n", args...)
}

// Run is the entry point for the geesefs-hook role.
func Run(_ []string) int {
	logf("STUB: createRuntime hook not implemented yet (Phase 4)")
	return 0
}
