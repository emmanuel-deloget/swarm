package agent

import (
	"os"
	"testing"

	"github.com/emmanuel-deloget/swarm/internal/probe"
)

// The child these tests drive is the test binary itself; see internal/probe.

func TestMain(m *testing.M) {
	if probe.Run(os.Args[1:]) {
		return
	}
	os.Exit(m.Run())
}

// probeCmd is the command for an agent whose behaviour the test chooses.
func probeCmd(t *testing.T, verbs ...string) []string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return probe.Argv(self, verbs...)
}
