//go:build windows

package config

import "os"

// checkSecretPerm has nothing to check on Windows, and says so rather than
// pretending either way.
//
// Windows does not carry POSIX modes. Go reports every readable file as 0666,
// or 0444 when it is read-only, and neither number describes who may open it —
// that is the file's ACL, which mode bits cannot express. Applying the Unix
// rule here would reject every secret ever written, advising a chmod that
// cannot be performed; skipping the check while keeping the promise would be
// worse, since a guarantee nobody verifies is one people rely on.
//
// So the promise is withdrawn on Windows, in the documentation as well as
// here. Restoring it means reading the DACL — GetNamedSecurityInfo, then
// refusing any entry granting read to a SID other than the owner, SYSTEM and
// the local administrators — and that check has to be built against a real
// machine: one written blind would refuse legitimate files, which on a
// security check is the failure that gets it disabled.
func checkSecretPerm(string, os.FileMode) error { return nil }
