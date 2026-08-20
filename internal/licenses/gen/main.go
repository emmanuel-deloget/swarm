// Command gen collects the licence of every module swarm links against and
// writes it into ../data, where the licences package embeds it.
//
// It is a generator rather than a hand-kept list because a hand-kept list is
// wrong the first time a dependency is added, and wrong silently: nothing about
// a missing notice fails to compile. The list of modules comes from the
// toolchain — go list, once per operating system, because conpty is linked only
// on Windows and termios only away from it — and the texts come from the module
// cache, which is the copy the build actually used.
//
// Run it with `go generate ./internal/licenses`.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// noneUpstream marks a module that ships no licence file, whose terms are
// written down by hand in data/manual instead.
const noneUpstream = "(none upstream)"

// selfModule is the module being built; it is not a third-party notice.
const selfModule = "github.com/emmanuel-deloget/swarm"

// targets are the operating systems whose builds are asked about. A module
// linked on one platform and not another still ships in that platform's
// binary, so the notices are the union rather than whatever this machine
// happens to be.
var targets = []string{"linux", "darwin", "windows"}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run() error {
	out, err := outputDir()
	if err != nil {
		return err
	}

	mods, err := modules()
	if err != nil {
		return err
	}
	if len(mods) < 20 {
		return fmt.Errorf("only %d modules found; go list is not telling the truth", len(mods))
	}

	// Everything is rewritten, so a dependency that goes away takes its notice
	// with it instead of lingering as a claim about what is in the binary.
	if err := clean(out); err != nil {
		return err
	}

	// swarm's own licence, copied in so the binary can state its own terms
	// first. It lives at the root of the tree, which go:embed cannot reach from
	// here, so a copy is the only way — and the test beside the package is what
	// stops the copy from drifting.
	if err := copySelfLicence(out); err != nil {
		return err
	}

	var index []string
	for _, m := range mods {
		name, text, err := licenceOf(m, filepath.Join(out, "manual"))
		if err != nil {
			return err
		}
		file := strings.ReplaceAll(m.Path, "/", "_") + ".txt"
		if err := os.WriteFile(filepath.Join(out, file), []byte(text), 0o644); err != nil {
			return err
		}
		// No version. It would be a copy of something the binary already
		// records, and a copy that goes stale the moment a dependency is
		// bumped — which made every dependabot pull request red for a reason
		// that had nothing to do with licences. The text is what belongs here,
		// and a patch release does not change it.
		index = append(index, strings.Join([]string{m.Path, name, file}, "\t"))
	}

	header := "# Written by internal/licenses/gen. Do not edit; run `go generate ./internal/licenses`.\n" +
		"# Versions are deliberately absent: the binary records its own, and a copy\n" +
		"# here would go stale on every bump.\n" +
		"# module\tlicence file as named upstream\tfile in this directory\n"
	body := header + strings.Join(index, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(out, "modules.tsv"), []byte(body), 0o644); err != nil {
		return err
	}
	fmt.Printf("gen: wrote %d module notices\n", len(mods))
	return nil
}

// outputDir is ../data relative to this file's package, resolved from the
// working directory go generate hands us.
func outputDir() (string, error) {
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "internal", "licenses", "data")
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	return dir, nil
}

// moduleRoot is the directory holding go.mod. go generate runs a generator in
// the directory of the file that asked for it, which is not where the package
// paths below resolve from, so nothing here relies on the working directory.
func moduleRoot() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %w", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		return "", fmt.Errorf("not inside a module")
	}
	return filepath.Dir(gomod), nil
}

// clean removes the generated notices but leaves anything else alone: the
// font's licence is written by hand, because a font is not a Go module.
func clean(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue // manual/, which is written by hand
		}
		if strings.HasSuffix(e.Name(), ".txt") && strings.Contains(e.Name(), "_") {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return os.RemoveAll(filepath.Join(dir, "modules.tsv"))
}

// copySelfLicence copies the repository's own LICENSE next to the generated
// notices. The version is deliberately not recorded: the package reads it from
// the build at run time, so a binary states the terms of the build in hand
// rather than of whichever tree the notices were generated in.
func copySelfLicence(out string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		return fmt.Errorf("cannot find swarm's own LICENSE: %w", err)
	}
	return os.WriteFile(filepath.Join(out, "self.txt"), b, 0o644)
}

type module struct{ Path, Version, Dir string }

// modules returns every non-standard module linked into the command, across
// every target, sorted and without duplicates.
func modules() ([]module, error) {
	root, err := moduleRoot()
	if err != nil {
		return nil, err
	}
	seen := map[string]module{}
	for _, goos := range targets {
		cmd := exec.Command("go", "list", "-deps",
			"-f", "{{if and .Module (not .Standard)}}{{.Module.Path}}\t{{.Module.Version}}\t{{.Module.Dir}}{{end}}",
			"./cmd/swarm")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOOS="+goos)
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("go list for %s: %w", goos, err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Split(strings.TrimSpace(line), "\t")
			if len(f) != 3 || f[0] == "" || f[0] == selfModule {
				continue
			}
			seen[f[0]] = module{Path: f[0], Version: f[1], Dir: f[2]}
		}
	}
	var mods []module
	for _, m := range seen {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].Path < mods[j].Path })
	return mods, nil
}

// licenceNames are the filenames a licence is kept under, most specific first.
var licenceNames = []string{
	"LICENSE", "LICENSE.txt", "LICENSE.md", "LICENCE", "LICENCE.txt",
	"COPYING", "COPYING.txt", "LICENSE-MIT", "LICENSE.APACHE2",
}

// licenceOf reads a module's licence out of the module cache, or out of the
// hand-written note kept for it.
//
// A module with no licence file anywhere is an error and not a shrug: shipping
// code whose terms are not known is the thing this whole package exists to
// avoid. But some modules genuinely ship without one and state their terms
// elsewhere, and the answer to that is to write down what is actually known —
// under data/manual, in the tree, where it can be read and disagreed with —
// rather than to synthesise a licence text on the author's behalf.
func licenceOf(m module, manualDir string) (name, text string, err error) {
	for _, n := range licenceNames {
		b, readErr := os.ReadFile(filepath.Join(m.Dir, n))
		if readErr == nil {
			return n, string(b), nil
		}
	}
	note := filepath.Join(manualDir, strings.ReplaceAll(m.Path, "/", "_")+".txt")
	if b, readErr := os.ReadFile(note); readErr == nil {
		return noneUpstream, string(b), nil
	}
	return "", "", fmt.Errorf("%s@%s carries no licence file in %s, and there is "+
		"no note at %s — find its terms and write them there, or drop the dependency",
		m.Path, m.Version, m.Dir, note)
}
