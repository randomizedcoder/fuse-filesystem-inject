// Package contract holds the cross-component contract shared by the GeeSFS
// injection tooling: the role names, the OCI annotation keys, the FUSE device
// numbers, and the default mount path.
//
// These values MUST stay in sync with nix/constants.nix, which holds the
// Nix-side copies used by the modules and the integration test. The
// `contract-parity` Nix check asserts the two agree so they can never drift.
package contract

// Role names. The multi-call binary dispatches on argv[0] basename; the Nix
// build installs one wrapper per role under these names.
const (
	RoleRunc       = "geesefs-runc"
	RoleSupervisor = "geesefsd"
	RoleHook       = "geesefs-hook"
)

// OCI annotation keys. Docker labels surface to the OCI runtime as annotations
// under these keys. All values are UNTRUSTED input (see docs/security.md).
const (
	AnnEnabled  = "geesefs.enabled"
	AnnBucket   = "geesefs.bucket"
	AnnMount    = "geesefs.mount"
	AnnEndpoint = "geesefs.endpoint"
)

// FUSE character device, per the Linux kernel device registry
// (Documentation/admin-guide/devices.txt): misc major 10, fuse minor 229.
const (
	FuseDeviceMajor = 10
	FuseDeviceMinor = 229
	FuseDevicePath  = "/dev/fuse"
)

// DefaultMountPath is where the S3 bucket is mounted when the annotation does
// not specify one. Matches constants.mountPath on the Nix side.
const DefaultMountPath = "/models"

// geesefsd listens on a unix socket; the createRuntime hook connects to it to
// request a mount and block until the mount is ready.
const (
	// SupervisorSocket is the default unix socket geesefsd listens on.
	SupervisorSocket = "/run/geesefsd.sock"
	// SocketEnv overrides SupervisorSocket for both ends (used by tests).
	SocketEnv = "GEESEFS_SOCKET"
)
