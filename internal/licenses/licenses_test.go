package licenses

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The point of this package is that a copy of swarm can answer for what is
// inside it. That claim decays in one direction only: a dependency is added,
// nothing fails to build, and the binary ships terms it does not carry. So the
// question these tests ask is not whether the notices parse — it is whether
// they still describe what is actually linked.

const selfModule = "github.com/emmanuel-deloget/swarm"

// linkedModules asks the toolchain what the binary is built from, once per
// operating system: conpty is linked only on Windows and termios only away
// from it, and each still ships in that platform's binary.
func linkedModules(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		cmd := exec.Command("go", "list", "-deps",
			"-f", "{{if and .Module (not .Standard)}}{{.Module.Path}}\t{{.Module.Version}}{{end}}",
			"../../cmd/swarm")
		cmd.Env = append(os.Environ(), "GOOS="+goos)
		b, err := cmd.Output()
		if err != nil {
			// Offline, or a module cache without the other platforms' deps.
			// Skipping is honest on a laptop: claiming the notices are complete
			// when nothing was asked would not be.
			//
			// In CI it is not honest at all. This test is the only thing
			// standing between a new dependency and a binary that ships terms
			// it does not carry, and a skipped test leaves the job green — the
			// same silent-pass this whole package exists to close. So there, it
			// is a failure.
			msg := fmt.Sprintf("cannot ask the toolchain what is linked on %s: %v", goos, err)
			if os.Getenv("CI") != "" {
				t.Fatal(msg + " — in CI this check may not be skipped")
			}
			t.Skip(msg)
		}
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Split(strings.TrimSpace(line), "\t")
			if len(f) != 2 || f[0] == "" || f[0] == selfModule {
				continue
			}
			out[f[0]] = f[1]
		}
	}
	if len(out) < 20 {
		t.Fatalf("only %d modules reported linked; go list is not telling the truth", len(out))
	}
	return out
}

// TestEveryLinkedModuleHasItsNotice is the whole reason the package exists.
func TestEveryLinkedModuleHasItsNotice(t *testing.T) {
	linked := linkedModules(t)

	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]Notice{}
	for _, n := range all {
		if !n.Bundled && !n.Self {
			have[n.Name] = n
		}
	}

	for path, version := range linked {
		n, ok := have[path]
		if !ok {
			t.Errorf("%s is linked into swarm and its licence is not carried; "+
				"run `go generate ./internal/licenses`", path)
			continue
		}
		if n.Version != version {
			t.Errorf("%s is linked at %s but the notice is for %s; "+
				"run `go generate ./internal/licenses`", path, version, n.Version)
		}
	}

	// And the other direction: a notice for something no longer linked is a
	// claim about the binary that is not true.
	for path := range have {
		if _, ok := linked[path]; !ok {
			t.Errorf("a licence is carried for %s, which is not linked into swarm "+
				"any more; run `go generate ./internal/licenses`", path)
		}
	}
}

// TestEveryNoticeHasText: an index row pointing at an empty file would list a
// dependency and tell the reader nothing about its terms, which is worse than
// not listing it — it looks answered.
func TestEveryNoticeHasText(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 {
		t.Fatal("no notices at all")
	}
	for _, n := range all {
		// The shortest real licence here is a few hundred bytes; anything
		// under a hundred is a stub or a truncation.
		if len(strings.TrimSpace(n.Text)) < 100 {
			t.Errorf("%s carries %d bytes of licence text", n.Title(), len(n.Text))
		}
	}
}

// TestNoOrphanedLicenceFiles: a file left behind after a dependency is dropped
// is not harmful, but it is a lie about the binary, and it is exactly what a
// generator that only ever adds would leave.
func TestNoOrphanedLicenceFiles(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{"data/self.txt": true}
	for _, n := range all {
		name := strings.ReplaceAll(n.Name, "/", "_") + ".txt"
		used["data/"+name] = true
		used["data/manual/"+name] = true
		used["data/bundled/"+n.Name+".txt"] = true
	}

	got, err := files()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range got {
		if !used[f] {
			t.Errorf("%s is embedded but no index names it", f)
		}
	}
}

// TestTheFontLicenceIsTheSameInBothPlaces: the font's licence lives beside the
// font, because that is where its terms ask for it, and here, because this is
// where a reader looks for everything at once. Two copies drift; this is what
// stops them.
func TestTheFontLicenceIsTheSameInBothPlaces(t *testing.T) {
	beside, err := os.ReadFile("../web/assets/fonts/LICENSE.txt")
	if err != nil {
		t.Fatalf("the font ships without its licence beside it: %v", err)
	}
	here, err := data.ReadFile("data/bundled/JuliaMono.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(beside) != string(here) {
		t.Error("the font's licence differs between internal/web/assets/fonts and " +
			"internal/licenses/data/bundled; copy one over the other")
	}
}

// unrecognised names the notices whose licence Kind cannot identify, and says
// why each one is expected. The point is not that every licence must be
// recognised — it is that a new unrecognised one is noticed by a test rather
// than by a reader of the list, where "see text" beside a dependency reads as
// an admission and may well be a matching bug.
var unrecognised = map[string]string{
	"github.com/mattn/go-localereader": "ships no licence file; the hand-written " +
		"note quotes what its README declares, and quoting is not the licence text",
}

func TestEveryLicenceIsRecognisedOrKnownNotToBe(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range all {
		_, expected := unrecognised[n.Name]
		switch {
		case n.Kind() == "see text" && !expected:
			t.Errorf("%s: the licence text matches none of the known wordings. "+
				"Either add its wording to kinds, or list it in unrecognised with "+
				"the reason", n.Name)
		case n.Kind() != "see text" && expected:
			t.Errorf("%s is listed as unrecognised but now reads as %s; drop it "+
				"from unrecognised", n.Name, n.Kind())
		}
	}
}

// TestSwarmStatesItsOwnTerms: the first version of this package listed every
// licence in the binary except swarm's own. The generator skips the module
// being built — correctly, it is not a third-party notice — and nothing
// noticed that the program had stopped answering the most obvious question
// asked of it.
func TestSwarmStatesItsOwnTerms(t *testing.T) {
	self, err := Self()
	if err != nil {
		t.Fatal(err)
	}
	if self.Kind() != "MIT" {
		t.Errorf("swarm reads as %s; the README says MIT", self.Kind())
	}
	if self.Version == "" {
		t.Error("swarm states its terms without saying which build they are for")
	}

	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 || !all[0].Self {
		t.Error("swarm's own terms are not the first thing the list gives")
	}
}

// TestSwarmsLicenceIsTheSameInBothPlaces: LICENSE at the root is the one that
// counts — it is what a reader of the repository finds and what the packaging
// picks up. The copy under data exists only because go:embed cannot reach out
// of its own directory.
func TestSwarmsLicenceIsTheSameInBothPlaces(t *testing.T) {
	root, err := os.ReadFile("../../LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	here, err := data.ReadFile("data/self.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(root) != string(here) {
		t.Error("LICENSE and internal/licenses/data/self.txt have drifted apart; " +
			"run `go generate ./internal/licenses`")
	}
}
