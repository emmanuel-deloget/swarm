//go:build !windows

package config

import (
	"fmt"
	"os"
)

// checkSecretPerm refuses a secret anyone but its owner can read. A shared
// secret in a group-readable file is not shared with the sender: it is shared
// with everyone on the machine, and the whole point of the signature is that
// only the two ends can produce it.
//
// The refusal carries the fix, because a refusal that only says no leaves the
// reader to guess which of several plausible things is wrong.
func checkSecretPerm(path string, mode os.FileMode) error {
	if perm := mode.Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s is mode %#o: it must not be readable by group or others (chmod 600 %s)", path, perm, path)
	}
	return nil
}
