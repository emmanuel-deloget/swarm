package version

import (
	"strings"
	"testing"
	"time"
)

// TestShort covers the four builds anyone actually has in their hands.
func TestShort(t *testing.T) {
	cases := []struct {
		name string
		in   Info
		want string
	}{
		{"at a tag", Info{Release: "v0.1.0"}, "v0.1.0"},
		{"at a tag, dirty tree", Info{Release: "v0.1.0+dirty"}, "v0.1.0+dirty"},
		{
			// Go names a commit past the last tag after the release that does
			// not exist yet. Thirty-six characters, and a version number nobody
			// has ever published: not what belongs in a header.
			"past the last tag",
			Info{Release: "v0.1.1-0.20260808001937-6d79900424c6", Revision: "6d79900424c6ddf0b694d0acee0baea36bc6d845"},
			"devel+6d79900",
		},
		{
			"past the last tag, dirty tree",
			Info{Release: "v0.1.1-0.20260808001937-6d79900424c6", Revision: "6d79900424c6ddf0b694d0acee0baea36bc6d845", Modified: true},
			"devel+6d79900*",
		},
		{"no version at all", Info{}, "devel"},
		{"revision only", Info{Revision: "abcdef1234567890"}, "devel+abcdef1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.in.Short(); got != c.want {
				t.Errorf("Short() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestShortFitsAHeader: the whole point of Short is that it can sit next to the
// session name and the agent counts without pushing them off the line.
func TestShortFitsAHeader(t *testing.T) {
	for _, in := range []Info{
		{Release: "v0.1.0"},
		{Release: "v0.1.1-0.20260808001937-6d79900424c6", Revision: "6d79900424c6ddf0b694d0acee0baea36bc6d845", Modified: true},
		{},
	} {
		if got := in.Short(); len(got) > 16 {
			t.Errorf("Short() = %q is %d characters, too long for the header", got, len(got))
		}
	}
}

func TestLongKeepsThePseudoVersion(t *testing.T) {
	in := Info{
		Release:  "v0.1.1-0.20260808001937-6d79900424c6",
		Revision: "6d79900424c6ddf0b694d0acee0baea36bc6d845",
		Time:     time.Date(2026, 8, 8, 0, 19, 37, 0, time.UTC),
		Go:       "go1.26.5", OS: "linux", Arch: "amd64",
	}
	got := in.Long()
	// Short hides it; Long must not, since it is what `go get` takes.
	if !strings.Contains(got, "v0.1.1-0.20260808001937-6d79900424c6") {
		t.Errorf("Long() should keep the module version:\n%s", got)
	}
	for _, want := range []string{"devel+6d79900", "6d79900424c6ddf0b694d0acee0baea36bc6d845", "go1.26.5", "linux/amd64"} {
		if !strings.Contains(got, want) {
			t.Errorf("Long() should mention %q:\n%s", want, got)
		}
	}
}

func TestLongSaysWhenNothingWasRecorded(t *testing.T) {
	got := Info{Go: "go1.26.5", OS: "linux", Arch: "amd64"}.Long()
	if !strings.Contains(got, "buildvcs=false") {
		t.Errorf("Long() should explain an empty version rather than show a blank:\n%s", got)
	}
}

// TestReadDoesNotPanic: whatever the binary was built from, asking is safe.
func TestReadDoesNotPanic(t *testing.T) {
	if s := Read().Short(); s == "" {
		t.Error("Short() should never be empty")
	}
}

// TestIsPseudo pins both shapes Go produces, since telling them from a real tag
// is the whole difference between a readable header and a wall of digits.
func TestIsPseudo(t *testing.T) {
	pseudo := []string{
		"v0.0.0-20260808001937-6d79900424c6", // no base tag: dash before the date
		"v0.1.1-0.20260808001937-6d79900424c6",
		"v1.2.4-0.20260101120000-abcdef123456",
		"v1.2.3-rc.1.0.20260101120000-abcdef123456",
		// A dirty tree gets "+dirty" appended after all of it, which is what a
		// developer building from their own checkout actually sees.
		"v0.1.1-0.20260808001937-6d79900424c6+dirty",
	}
	tagged := []string{"v0.1.0", "v0.1.0+dirty", "v1.2.3", "v2.0.0-rc.1", ""}
	for _, v := range pseudo {
		if !isPseudo(v) {
			t.Errorf("isPseudo(%q) = false, want true", v)
		}
	}
	for _, v := range tagged {
		if isPseudo(v) {
			t.Errorf("isPseudo(%q) = true, want false", v)
		}
	}
}
