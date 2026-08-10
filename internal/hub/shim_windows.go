//go:build windows

package hub

import (
	"os"
	"path/filepath"
)

// linkShim writes <state>\bin\swarm.cmd, a one-line launcher for the running
// binary.
//
// Not a symlink: creating one on Windows needs SeCreateSymbolicLinkPrivilege,
// which an ordinary account does not have unless developer mode is on — the
// shim would then be missing exactly on the machines least able to diagnose
// why `swarm send` is not found. Not a copy either: a copy would freeze the
// binary a fleet was started with, and go on handing agents the old one after
// an upgrade.
//
// The .cmd extension is what makes it work as `swarm`: PATHEXT lists it, so
// the command interpreter, PowerShell and Go's own exec.LookPath all find it
// under the bare name. %* passes the arguments through, and the quotes around
// the path survive a swarm installed under "Program Files".
func linkShim(dir, exe string) error {
	script := "@\"" + exe + "\" %*\r\n"
	return os.WriteFile(filepath.Join(dir, "swarm.cmd"), []byte(script), 0o755)
}
