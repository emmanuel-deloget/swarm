package workspace

import "time"

// removeRetries is how many times a removal is attempted, and the wait between
// tries.
//
// Windows refuses to delete a directory whose files are still held open, which
// happens routinely to one written seconds earlier: the search indexer and the
// virus scanner both take handles, and both let go on their own. A second is
// long enough for that and short enough that nobody watching a fleet notices.
func removeRetries() (int, time.Duration) { return 3, time.Second }
