package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/emmanuel-deloget/swarm/internal/licenses"
)

// `swarm licenses` is how a copy of swarm answers for what is inside it, which
// makes it one of the few commands whose output is the point rather than a
// convenience. It also runs without a fleet, a socket or a config — someone
// asking what a binary contains has, by assumption, nothing else set up.

func TestLicensesRunsWithNothingSetUp(t *testing.T) {
	bin := buildSwarm(t)

	// An empty directory: no swarm.yaml, no socket, nothing to connect to.
	cmd := exec.Command(bin, "licenses")
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("swarm licenses failed with no config present: %v\n%s", err, out)
	}
	text := string(out)

	all, err := licenses.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 20 {
		t.Fatalf("only %d notices carried", len(all))
	}
	for _, n := range all {
		if !strings.Contains(text, n.Name) {
			t.Errorf("%s is bundled and `swarm licenses` does not list it", n.Name)
		}
	}
}

func TestLicensesPrintsOneInFull(t *testing.T) {
	bin := buildSwarm(t)

	cmd := exec.Command(bin, "licenses", "juliamono")
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("swarm licenses juliamono: %v\n%s", err, out)
	}
	text := string(out)

	// The font's licence is the one that requires its text to travel with the
	// software, so printing the name and not the terms would miss the point.
	if !strings.Contains(text, "SIL OPEN FONT LICENSE") {
		t.Error("`swarm licenses juliamono` does not print the licence itself")
	}
	// And only that one: asking for a name is asking for a text, not for all
	// of them.
	if strings.Contains(text, "github.com/creack/pty") {
		t.Error("asking for one licence printed others too")
	}
}

func TestLicensesRefusesANameItDoesNotCarry(t *testing.T) {
	bin := buildSwarm(t)

	cmd := exec.Command(bin, "licenses", "definitely-not-bundled")
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("a name that is not bundled exited 0:\n%s", out)
	}
	// Silence would read as "bundled, and its licence is empty", which is the
	// one answer this command must never give.
	if !strings.Contains(string(out), "nothing bundled matches") {
		t.Errorf("the error does not say what went wrong:\n%s", out)
	}
}
