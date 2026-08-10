package agent

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/config"
	"github.com/emmanuel-deloget/swarm/internal/event"
)

// Several keys sent at once used to leave in a single write. An agent whose UI
// changes state on a key acts on the first and drops the rest with the buffer
// it was holding: `enter shift+tab shift+tab shift+tab` moved a fleet one step
// instead of three.

func keyAgent(t *testing.T, delay time.Duration) *Agent {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.AgentConfig{
		Name:     "alpha",
		Command:  probeCmd(t, "echo"),
		Cols:     80,
		Rows:     24,
		KeyDelay: delay,
	}
	cfg.Workdir = dir
	a := New(Options{
		Config:       cfg,
		Log:          event.NewLog(64),
		Env:          os.Environ(),
		InputLogFile: dir + "/alpha.input.log",
	})
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Stop(time.Second) })
	return a
}

func inputLog(t *testing.T, a *Agent) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(a.inputLogPath)
		if err == nil {
			last = string(b)
			if strings.Count(last, "\tkeys\t") >= 4 {
				return last
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return last
}

// TestEachKeyIsItsOwnWrite is the fix: four keys, four writes.
func TestEachKeyIsItsOwnWrite(t *testing.T) {
	a := keyAgent(t, 5*time.Millisecond)
	if err := a.SendKeys("enter shift+tab shift+tab shift+tab"); err != nil {
		t.Fatal(err)
	}
	log := inputLog(t, a)
	if n := strings.Count(log, "\tkeys\t"); n != 4 {
		t.Errorf("%d writes for four keys:\n%s", n, log)
	}
	// And the bytes are still the right ones, in order.
	for _, want := range []string{`"\r"`, `"\x1b[Z"`} {
		if !strings.Contains(log, want) {
			t.Errorf("the log never shows %s:\n%s", want, log)
		}
	}
}

// TestTheDelayIsHonoured: without it the writes land in one read again.
func TestTheDelayIsHonoured(t *testing.T) {
	a := keyAgent(t, 60*time.Millisecond)
	start := time.Now()
	if err := a.SendKeys("up up up"); err != nil {
		t.Fatal(err)
	}
	// Two gaps between three keys.
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("three keys with a 60ms delay took %s", elapsed)
	}
}

// TestZeroDelayStillSendsEverything, for whoever wants them as one burst.
func TestZeroDelayStillSendsEverything(t *testing.T) {
	a := keyAgent(t, 0)
	if err := a.SendKeys("enter shift+tab shift+tab shift+tab"); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(inputLog(t, a), "\tkeys\t"); n != 4 {
		t.Errorf("%d writes, want four even with no delay", n)
	}
}

// TestAnUnknownKeySendsNothing: validation happens before the first write, so a
// typo in the middle cannot leave half a sequence delivered.
func TestAnUnknownKeySendsNothing(t *testing.T) {
	a := keyAgent(t, time.Millisecond)
	if err := a.SendKeys("enter nosuchkey enter"); err == nil {
		t.Fatal("an unknown key was accepted")
	}
	if b, err := os.ReadFile(a.inputLogPath); err == nil && strings.Contains(string(b), "\tkeys\t") {
		t.Errorf("something was sent before the error:\n%s", b)
	}
}
