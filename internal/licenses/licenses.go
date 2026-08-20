// Package licenses carries the terms of everything swarm ships inside its
// binary, so that a copy of swarm can always answer for what is in it.
//
// Two kinds of thing end up here. Go modules, whose notices are collected from
// the module cache by internal/licenses/gen — the copy the build actually
// linked, not a guess at it. And works that are not Go modules, of which there
// is currently one: the font the web UI draws with, whose licence requires the
// text to travel with the software.
//
// The list is generated rather than kept by hand because a hand-kept list is
// wrong the first time a dependency changes, and wrong in silence — nothing
// about a missing notice fails to build. What keeps it honest is the test
// beside this file, which asks the toolchain what is linked and checks that
// every answer has a notice here.
//
//go:generate go run ./gen
package licenses

import (
	"embed"
	"fmt"
	"io/fs"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/emmanuel-deloget/swarm/internal/version"
)

//go:embed data
var data embed.FS

// Notice is one work and the terms it is offered under.
type Notice struct {
	// Name is the module path, or the name of the work for what is not a
	// module.
	Name string
	// Version is the module version, or the release for a bundled work.
	Version string
	// About says what a bundled work is. Empty for modules, whose path says
	// it.
	About string
	// File is what the licence was called upstream, which is worth keeping:
	// "LICENSE-MIT" and "COPYING" are themselves a statement.
	File string
	// Text is the licence itself, verbatim.
	Text string
	// Bundled distinguishes a work carried in the binary from a module linked
	// into it.
	Bundled bool
	// Self marks swarm's own terms. It is the first thing anyone asking this
	// question wants, and it was the one licence the first version of this
	// package left out — the generator skips the module being built, and
	// nothing noticed that the program had stopped stating its own terms.
	Self bool
}

// Title is how a notice is named in a list.
func (n Notice) Title() string {
	if n.Version == "" {
		return n.Name
	}
	return n.Name + " " + n.Version
}

// kinds names a licence from wording that only that licence uses. Matching on
// a distinctive sentence rather than on a title, because plenty of files begin
// with a copyright line and never name the licence at all.
//
// Order matters: the OFL and the Apache licence both contain the word BSD
// somewhere, and BSD's distinctive sentence appears inside longer licences.
var kinds = []struct{ name, phrase string }{
	{"SIL OFL 1.1", "SIL OPEN FONT LICENSE"},
	{"Apache 2.0", "Apache License"},
	{"MPL 2.0", "Mozilla Public License"},
	// Two spellings in the wild: the ISC text says "and/or distribute", the
	// older OpenBSD-style one says "and distribute". Matching the part they
	// share, which is still nothing any other licence opens with.
	{"ISC", "Permission to use, copy, modify, and"},
	{"MIT", "Permission is hereby granted, free of charge"},
	{"BSD", "Redistribution and use in source and binary forms"},
}

// Kind names the licence, or says it could not tell. It is a convenience for
// listing many at once and never a substitute for the text: an answer of "MIT"
// here means the text contains MIT's permission sentence, not that a lawyer
// agreed.
func (n Notice) Kind() string {
	for _, k := range kinds {
		if strings.Contains(n.Text, k.phrase) {
			return k.name
		}
	}
	return "see text"
}

// linked returns the version of every module compiled into this binary.
//
// Go records them at build time, which makes this the only source that cannot
// be wrong: a version written down beside a licence is a copy, and a copy is
// stale from the next bump onwards. `go version -m` reads the same table.
//
// Empty for a test binary, which records no dependencies — so a notice shown
// from a test carries no version rather than a wrong one.
func linked() map[string]string {
	out := map[string]string{}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return out
	}
	for _, d := range bi.Deps {
		v := d.Version
		// A replaced module runs as its replacement, so that is the version in
		// the binary.
		if d.Replace != nil && d.Replace.Version != "" {
			v = d.Replace.Version
		}
		out[d.Path] = v
	}
	return out
}

// Self returns swarm's own terms.
//
// The version comes from the build rather than from the generated data, so a
// binary answers for itself and not for the tree its notices were made in.
func Self() (Notice, error) {
	text, err := data.ReadFile("data/self.txt")
	if err != nil {
		return Notice{}, fmt.Errorf("licenses: swarm ships without its own licence: %w", err)
	}
	return Notice{
		Name:    "swarm",
		Version: version.Short(),
		About:   "this program",
		File:    "LICENSE",
		Text:    string(text),
		Self:    true,
	}, nil
}

// All returns every notice: swarm's own terms first, then the works bundled
// into the binary, then the modules it links, by path.
func All() ([]Notice, error) {
	self, err := Self()
	if err != nil {
		return nil, err
	}
	bundled, err := read("data/bundled.tsv", true)
	if err != nil {
		return nil, err
	}
	mods, err := read("data/modules.tsv", false)
	if err != nil {
		return nil, err
	}
	sort.Slice(bundled, func(i, j int) bool { return bundled[i].Name < bundled[j].Name })
	sort.Slice(mods, func(i, j int) bool { return mods[i].Name < mods[j].Name })
	out := append([]Notice{self}, bundled...)
	return append(out, mods...), nil
}

// Find returns the notices whose name contains q, case-insensitively. An empty
// query matches everything, so a caller need not special-case it.
func Find(q string) ([]Notice, error) {
	all, err := All()
	if err != nil {
		return nil, err
	}
	if q == "" {
		return all, nil
	}
	q = strings.ToLower(q)
	var out []Notice
	for _, n := range all {
		if strings.Contains(strings.ToLower(n.Name), q) {
			out = append(out, n)
		}
	}
	return out, nil
}

// read parses one index and loads the text each row points at.
func read(index string, bundled bool) ([]Notice, error) {
	body, err := data.ReadFile(index)
	if err != nil {
		return nil, fmt.Errorf("licenses: %w", err)
	}
	dir := "data"
	if bundled {
		dir = "data/bundled"
	}

	// A bundled work names its own version, since nothing else knows it: the
	// font is not a Go module and the binary records nothing about it.
	fields := 3
	if bundled {
		fields = 4
	}
	rows, err := parseIndex(index, string(body), fields)
	if err != nil {
		return nil, err
	}
	versions := linked()

	var out []Notice
	for _, f := range rows {
		file := f[fields-1]
		text, err := data.ReadFile(dir + "/" + file)
		if err != nil {
			return nil, fmt.Errorf("licenses: %s names %s, which is not here: %w", index, file, err)
		}
		n := Notice{Name: f[0], Text: string(text), Bundled: bundled}
		if bundled {
			n.Version, n.About = f[1], f[2]
		} else {
			n.File, n.Version = f[1], versions[f[0]]
		}
		out = append(out, n)
	}
	return out, nil
}

// parseIndex splits an index into its rows, of four fields each.
//
// Separate from read so that the line handling can be tested without an
// embedded file to point at, which is how the carriage return below is now
// checked rather than remembered.
func parseIndex(index, body string, fields int) ([][4]string, error) {
	var out [][4]string
	for i, line := range strings.Split(body, "\n") {
		// A Windows checkout rewrites these files to CRLF, and a field ending
		// in a stray carriage return names a file that is not there. It cost a
		// red CI job whose own error message ended, invisibly, halfway
		// through — the terminal put the rest of it back over the beginning.
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != fields {
			return nil, fmt.Errorf("licenses: %s line %d has %d fields, want %d",
				index, i+1, len(f), fields)
		}
		var row [4]string
		copy(row[:], f)
		out = append(out, row)
	}
	return out, nil
}

// files lists what is embedded, which the test uses to catch a notice that is
// present but pointed at by nothing.
func files() ([]string, error) {
	var out []string
	err := fs.WalkDir(data, "data", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".txt") {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}
