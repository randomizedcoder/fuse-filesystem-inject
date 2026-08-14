// Package cli parses the runc-compatible command line that geesefs-runc
// receives. The injector only needs two things from it — the subcommand and
// the bundle path — and passes everything else through to the real runc
// untouched, so the parsing here is deliberately narrow.
package cli

import "strings"

// RuncInvocation is the parsed view of a runc CLI invocation.
type RuncInvocation struct {
	// Subcommand is the first bare (non-flag) token, e.g. "create" or "state".
	Subcommand string
	// Bundle is the value of --bundle/-b, or "" when absent (runc defaults it
	// to the current working directory).
	Bundle string
}

// runcGlobalValueFlags are the runc *global* options that consume the following
// token as their value. They must be skipped when scanning for the subcommand,
// otherwise their value (e.g. the path after --root) would be mistaken for it.
var runcGlobalValueFlags = map[string]bool{
	"--root":       true,
	"--log":        true,
	"--log-format": true,
	"--criu":       true,
}

// ParseRunc extracts the subcommand and --bundle/-b value from a runc-style
// argument list (os.Args[1:]). --bundle/-b takes the following token (or an
// =value) as its value; the first bare token that is not consumed as a flag
// value is the subcommand.
func ParseRunc(args []string) RuncInvocation {
	var inv RuncInvocation
	skipNext := false

	for i, a := range args {
		if skipNext {
			skipNext = false
			continue
		}

		switch {
		case a == "-b" || a == "--bundle":
			if i+1 < len(args) {
				inv.Bundle = args[i+1]
			}
			skipNext = true
		case strings.HasPrefix(a, "--bundle="):
			inv.Bundle = strings.TrimPrefix(a, "--bundle=")
		case strings.HasPrefix(a, "-b="):
			inv.Bundle = strings.TrimPrefix(a, "-b=")
		case runcGlobalValueFlags[a]:
			skipNext = true
		case strings.HasPrefix(a, "-"):
			// Some other boolean/unknown flag — ignore.
		default:
			if inv.Subcommand == "" {
				inv.Subcommand = a
			}
		}
	}

	return inv
}
