package main

import (
	"os"
	"strings"
	"testing"

	"github.com/emmanuel-deloget/swarm/internal/probe"
)

func TestMain(m *testing.M) {
	if probe.Run(os.Args[1:]) {
		return
	}
	os.Exit(m.Run())
}

// probeAgents turns the agent shorthands the end-to-end configs use into
// command lines running the test binary; see internal/probe.
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
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
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
