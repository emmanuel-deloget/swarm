// Package version reports which swarm this is.
//
// Nothing is stamped in at build time: Go already records what we need.
// `go install module@v0.1.0` puts the tag in the module version, and a plain
// `go build` from a checkout records the commit, its date, and whether the tree
// was dirty. Reading those beats -ldflags, which only works when whoever builds
// remembers to pass it — and the person who most needs an accurate version is
// the one who built it in a hurry to reproduce a bug.
package version

import (
	"fmt"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// Info is what the build knows about itself.
type Info struct {
	// Release is the module version: "v0.1.0" when installed from a tag,
	// empty when built from a checkout.
	Release string
	// Revision is the commit the build came from, empty if unknown.
	Revision string
	// Time is when that commit was made.
	Time time.Time
	// Modified reports that the tree had uncommitted changes.
	Modified bool
	// Go is the toolchain that built it.
	Go string
	// OS and Arch are the target platform.
	OS, Arch string
}

// Read collects what the binary knows about its own build.
func Read() Info {
	info := Info{Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	// "(devel)" is what a build from a checkout reports; it says nothing the
	// revision below does not say better.
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		info.Release = v
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			info.Revision = s.Value
		case "vcs.time":
			info.Time, _ = time.Parse(time.RFC3339, s.Value)
		case "vcs.modified":
			info.Modified = s.Value == "true"
		}
	}
	return info
}

// Short is the version in a few characters, for a header line that has other
// things to say:
//
//	v0.1.0            built at the tag
//	v0.1.0+dirty      ... with uncommitted changes; Go adds the suffix itself
//	devel+6d79900     built past the last tag
//	devel+6d79900*    ... with uncommitted changes
//	devel             built with the VCS stamp off
//
// Past a tag Go reports a pseudo-version — "v0.1.1-0.20260808001937-6d79900424c6",
// thirty-six characters naming a release that does not exist. It is the right
// string to hand to `go get` and the wrong one to put in a header, so those
// builds are named after their commit instead.
func (i Info) Short() string {
	if i.Release != "" && !isPseudo(i.Release) {
		return i.Release
	}
	if i.Revision == "" {
		return "devel"
	}
	out := "devel+" + shortRev(i.Revision)
	if i.Modified {
		// A star rather than "-dirty": in a header every character is spent,
		// and a build that matches no commit is worth one.
		out += "*"
	}
	return out
}

// Long is the whole story, for `swarm version`.
func (i Info) Long() string {
	var b strings.Builder
	fmt.Fprintf(&b, "swarm %s\n", i.Short())
	if isPseudo(i.Release) {
		// Long has room for it, and it is what `go get` wants.
		fmt.Fprintf(&b, "  module    %s\n", i.Release)
	}
	if i.Revision != "" {
		line := "  commit    " + i.Revision
		if i.Modified {
			line += " (with uncommitted changes)"
		}
		fmt.Fprintln(&b, line)
	}
	if !i.Time.IsZero() {
		fmt.Fprintf(&b, "  committed %s\n", i.Time.Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "  built by  %s for %s/%s\n", i.Go, i.OS, i.Arch)
	if i.Release == "" && i.Revision == "" {
		fmt.Fprintln(&b, "\nNo version was recorded. This happens when the binary was built")
		fmt.Fprintln(&b, "outside a git checkout, or with -buildvcs=false.")
	}
	return b.String()
}

// pseudoVersion matches what Go synthesises for a commit that carries no tag:
// a timestamp and a twelve-character revision. The separator before the
// timestamp is a dash when there is no base tag to build on
// ("v0.0.0-20260808001937-6d79900424c6") and a dot when there is
// ("v0.1.1-0.20260808001937-6d79900424c6"). A dirty tree adds "+dirty" after
// all of it, so the match cannot simply anchor at the end.
var pseudoVersion = regexp.MustCompile(`[-.]\d{14}-[0-9a-f]{12}(\+[0-9A-Za-z.-]+)?$`)

func isPseudo(v string) bool { return pseudoVersion.MatchString(v) }

// Short returns the running binary's version in a few characters.
func Short() string { return Read().Short() }

func shortRev(rev string) string {
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}
