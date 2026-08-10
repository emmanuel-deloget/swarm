//go:build !windows

package hub

import (
	"os"
	"path/filepath"
)

// linkShim points <state>/bin/swarm at the running binary, so an agent can run
// `swarm send` without knowing where swarm was installed.
//
// A symlink rather than a copy: the shim then follows the binary across
// upgrades, and a fleet started before an upgrade does not keep handing its
// agents the old one.
func linkShim(dir, exe string) error {
	link := filepath.Join(dir, "swarm")
	if current, err := os.Readlink(link); err == nil && current == exe {
		return nil
	}
	_ = os.Remove(link)
	return os.Symlink(exe, link)
}
