package main

import (
	"flag"
	"io"
	"strings"
	"testing"
)

func TestParseArgsAcceptsFlagsAfterTheTarget(t *testing.T) {
	newSet := func() (*flag.FlagSet, *string, *bool, *stringList) {
		fs := flag.NewFlagSet("inject", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		textFile := fs.String("text-file", "", "")
		submit := fs.Bool("submit", true, "")
		var files stringList
		fs.Var(&files, "file", "")
		return fs, textFile, submit, &files
	}

	// The natural form: target first, flags after, message last.
	fs, _, _, files := newSet()
	if err := parseArgs(fs, []string{"dev-1", "-file", "shot.png", "what is wrong here?"}, 1); err != nil {
		t.Fatal(err)
	}
	if fs.Arg(0) != "dev-1" {
		t.Errorf("target = %q, want dev-1", fs.Arg(0))
	}
	if len(*files) != 1 || (*files)[0] != "shot.png" {
		t.Errorf("files = %v, want [shot.png]", *files)
	}
	if got := strings.Join(fs.Args()[1:], " "); got != "what is wrong here?" {
		t.Errorf("message = %q", got)
	}

	// Boolean flags do not swallow the next argument.
	fs, _, submit, _ := newSet()
	if err := parseArgs(fs, []string{"dev-1", "-submit=false", "half a thought"}, 1); err != nil {
		t.Fatal(err)
	}
	if *submit {
		t.Error("-submit=false was not applied")
	}
	if got := strings.Join(fs.Args()[1:], " "); got != "half a thought" {
		t.Errorf("message = %q", got)
	}

	// Flags before the target still work.
	fs, textFile, _, _ := newSet()
	if err := parseArgs(fs, []string{"-text-file", "notes.md", "dev-1"}, 1); err != nil {
		t.Fatal(err)
	}
	if *textFile != "notes.md" || fs.Arg(0) != "dev-1" {
		t.Errorf("text-file = %q, target = %q", *textFile, fs.Arg(0))
	}
}

func TestParseArgsKeepsFreeTextIntact(t *testing.T) {
	newSet := func() (*flag.FlagSet, *bool) {
		fs := flag.NewFlagSet("send", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		asJSON := fs.Bool("json", false, "")
		return fs, asJSON
	}

	// A message that mentions a real flag name must not be parsed as one:
	// free text starts at the first non-flag past the target.
	fs, asJSON := newSet()
	if err := parseArgs(fs, []string{"dev-1", "check", "the", "-json", "output"}, 1); err != nil {
		t.Fatal(err)
	}
	if *asJSON {
		t.Error("-json inside the message was taken as a flag")
	}
	if got := strings.Join(fs.Args()[1:], " "); got != "check the -json output" {
		t.Errorf("message = %q", got)
	}

	// A broadcast has no target, so the message starts immediately.
	fs, asJSON = newSet()
	if err := parseArgs(fs, []string{"stopping", "in", "-json", "minutes"}, 0); err != nil {
		t.Fatal(err)
	}
	if *asJSON {
		t.Error("-json inside a broadcast was taken as a flag")
	}
	if got := strings.Join(fs.Args(), " "); got != "stopping in -json minutes" {
		t.Errorf("message = %q", got)
	}

	// "--" ends flag parsing outright.
	fs, asJSON = newSet()
	if err := parseArgs(fs, []string{"dev-1", "--", "-json", "is", "literal"}, 1); err != nil {
		t.Fatal(err)
	}
	if *asJSON {
		t.Error("-json after -- was taken as a flag")
	}
	if got := strings.Join(fs.Args()[1:], " "); got != "-json is literal" {
		t.Errorf("message = %q", got)
	}
}

func TestParseArgsWithoutFreeText(t *testing.T) {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	follow := fs.Bool("f", false, "")
	raw := fs.Bool("raw", false, "")

	// textAfter < 0: flags are recognised anywhere.
	if err := parseArgs(fs, []string{"dev-1", "-f", "-raw"}, -1); err != nil {
		t.Fatal(err)
	}
	if !*follow || !*raw {
		t.Errorf("flags after the agent name were not applied: -f=%v -raw=%v", *follow, *raw)
	}
	if fs.Arg(0) != "dev-1" || fs.NArg() != 1 {
		t.Errorf("args = %v, want [dev-1]", fs.Args())
	}
}

func TestKeysAcceptsFlagsAfterKeyNames(t *testing.T) {
	// Key names are tokens, not prose: a flag among them is a flag. Treating
	// them as free text made `swarm keys a1 ctrl+l -c swarm.yaml` fail with
	// "unknown key \"-c\"".
	fs := flag.NewFlagSet("keys", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfg := fs.String("c", "", "")
	list := fs.Bool("list", false, "")

	if err := parseArgs(fs, []string{"a1", "esc", "ctrl+c", "-c", "swarm.yaml"}, -1); err != nil {
		t.Fatal(err)
	}
	if *cfg != "swarm.yaml" {
		t.Errorf("-c = %q, want swarm.yaml", *cfg)
	}
	if got := strings.Join(fs.Args(), " "); got != "a1 esc ctrl+c" {
		t.Errorf("key names = %q, want %q", got, "a1 esc ctrl+c")
	}
	if *list {
		t.Error("-list was not passed")
	}
}
