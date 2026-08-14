//go:build !windows

package workspace

import "time"

// removeRetries is one attempt, immediately.
//
// A removal that fails here failed for a reason, and asking again would delay
// the answer without changing it. The retry exists for a Windows behaviour that
// has no equivalent on these systems.
func removeRetries() (int, time.Duration) { return 1, 0 }
