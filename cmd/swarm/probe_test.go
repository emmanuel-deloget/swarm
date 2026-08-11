package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/emmanuel-deloget/swarm/internal/probe"
)

// TestMain exists to take the built probe away again: it is one binary for the
// whole run, so no single test owns it.
func TestMain(m *testing.M) {
	code := m.Run()
	if probeBin != "" {
		_ = os.RemoveAll(filepath.Dir(probeBin))
	}
	os.Exit(code)
}

var (
	probeOnce     sync.Once
	probeBin      string
	errProbeBuild error
)

// buildProbe compiles the child once per run, next to the swarm binary these
// tests already build.
//
// A separate program rather than this test binary run again: the trick works
// elsewhere and costs no build, but here the child is started by swarm, inside
// a pty, on three operating systems — and a test binary carries a TestMain to
// reach, testing flags to survive and race instrumentation to load before it
// can say anything. When it stayed silent on macOS there was no way to tell
// which of those had gone wrong. A plain program has none of them.
func buildProbe(t *testing.T) string {
	t.Helper()
	probeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "swarm-probe")
		if err != nil {
			errProbeBuild = err
			return
		}
		name := "probe"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		probeBin = filepath.Join(dir, name)
		out, err := exec.Command("go", "build", "-o", probeBin,
			"github.com/emmanuel-deloget/swarm/internal/probe/cmd/probe").CombinedOutput()
		if err != nil {
			errProbeBuild = err
			t.Logf("building the probe: %s", out)
		}
	})
	if errProbeBuild != nil {
		t.Fatalf("building the probe: %v", errProbeBuild)
	}
	return probeBin
}

// probeAgents turns the agent shorthands the end-to-end configs use into
// command lines running that program; see internal/probe.
//
//	[probe-alpha]     says "alpha ready", then echoes lines as "alpha saw:…"
//	[probe-beta]      the same, as beta
//	[probe-saw]       says "ready", then echoes lines as "saw:…"
//	[probe-ready]     says "ready" and stays up
//	[probe-a1]        says "a1 ready" and stays up
//	[probe-talker]    says "talker ready", then sends every line to alpha
//	[probe-numbered]  writes 60 numbered lines, then stays up
//	[probe-size]      writes the geometry it believes it has, three times a second
//	[probe-catv]      shows arriving bytes with control characters visible
//
// The names say what the child does rather than which Unix command it
// impersonates: nothing here runs sh, cat or stty, and a test claiming
// otherwise would be misreporting what it measured.
//
// Single quotes in the YAML so a Windows path keeps its backslashes.
func probeAgents(t *testing.T, body string) string {
	t.Helper()
	self := buildProbe(t)
	argv := func(verbs ...string) string {
		quoted := make([]string, 0, len(verbs)+2)
		for _, v := range probe.Argv(self, verbs...) {
			quoted = append(quoted, "'"+v+"'")
		}
		return "[" + strings.Join(quoted, ", ") + "]"
	}
	for token, verbs := range map[string][]string{
		"[probe-alpha]":    {"print", "alpha ready", "lines", "alpha saw:"},
		"[probe-beta]":     {"print", "beta ready", "lines", "beta saw:"},
		"[probe-saw]":      {"print", "ready", "lines", "saw:"},
		"[probe-ready]":    {"print", "ready", "sleep", "3600"},
		"[probe-a1]":       {"print", "a1 ready", "sleep", "3600"},
		"[probe-talker]":   {"print", "talker ready", "sendlines", "alpha"},
		"[probe-numbered]": {"numbered", "60", "sleep", "3600"},
		"[probe-size]":     {"size", "0.3"},
		"[probe-catv]":     {"print", "a1 ready", "catv"},
	} {
		body = strings.ReplaceAll(body, token, argv(verbs...))
	}
	return body
}
