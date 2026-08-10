package agent

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/config"
	"github.com/emmanuel-deloget/swarm/internal/event"
)

// A command that is simply broken used to relaunch every two seconds, forever,
// filling the log and the process table until somebody noticed. The counter was
// already there; nothing read it.

func restarting(t *testing.T, cfg *config.AgentConfig) (*Agent, *event.Log) {
	t.Helper()
	cfg.Workdir = t.TempDir()
	if cfg.Cols == 0 {
		cfg.Cols, cfg.Rows = 80, 24
	}
	yes := true
	cfg.RestartOnExit = &yes
	log := event.NewLog(256)
	a := New(Options{Config: cfg, Log: log, Env: os.Environ()})
	t.Cleanup(func() { _ = a.Stop(time.Second) })
	return a, log
}

func logText(l *event.Log) string {
	var b strings.Builder
	for _, e := range l.History(-1) {
		b.WriteString(e.Agent + " " + e.Text + "\n")
	}
	return b.String()
}

func waitLogged(t *testing.T, l *event.Log, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := logText(l); strings.Contains(got, want) {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	return logText(l)
}

// TestABrokenCommandIsGivenUpOn: the point of the limit. The message says what
// to do, because whoever reads it has to do something.
func TestABrokenCommandIsGivenUpOn(t *testing.T) {
	a, log := restarting(t, &config.AgentConfig{
		Name:           "broken",
		Command:        []string{"sh", "-c", "exit 1"},
		RestartBackoff: 5 * time.Millisecond,
		RestartMaxWait: 40 * time.Millisecond,
		RestartMax:     3,
	})
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}

	got := waitLogged(t, log, "not restarting again")
	if !strings.Contains(got, "died 3 times in a row") {
		t.Errorf("the log never says it gave up after three:\n%s", got)
	}
	if !strings.Contains(got, "swarm start broken") {
		t.Errorf("the message does not say what to do:\n%s", got)
	}

	// And it really stopped: no further restart after the verdict.
	before := strings.Count(got, "restarting after exit")
	time.Sleep(200 * time.Millisecond)
	if after := strings.Count(logText(log), "restarting after exit"); after != before {
		t.Errorf("%d restarts before giving up, %d after", before, after)
	}
}

// TestTheWaitDoubles rather than staying flat.
func TestTheWaitDoubles(t *testing.T) {
	a, _ := restarting(t, &config.AgentConfig{
		Name:           "broken",
		Command:        []string{"sh", "-c", "exit 1"},
		RestartBackoff: 10 * time.Millisecond,
		RestartMaxWait: 80 * time.Millisecond,
		RestartMax:     0, // no limit, so the streak can grow
	})

	var waits []time.Duration
	for range 5 {
		w, _, giveUp := a.restartPlan(time.Millisecond) // each run was short
		if giveUp {
			t.Fatal("gave up with no limit set")
		}
		waits = append(waits, w)
	}
	want := []time.Duration{
		10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond,
		80 * time.Millisecond, 80 * time.Millisecond,
	}
	for i, w := range waits {
		if w != want[i] {
			t.Errorf("wait %d is %s, want %s", i+1, w, want[i])
		}
	}
}

// TestARunThatLastedStartsOver: an agent that dies once a day is not a crash
// loop, and must not be answered as if it were.
func TestARunThatLastedStartsOver(t *testing.T) {
	a, _ := restarting(t, &config.AgentConfig{
		Name:           "flaky",
		Command:        []string{"sh", "-c", "exit 1"},
		RestartBackoff: 10 * time.Millisecond,
		RestartMaxWait: 50 * time.Millisecond,
		RestartMax:     3,
	})

	// Two quick deaths build a streak.
	a.restartPlan(time.Millisecond)
	if w, streak, _ := a.restartPlan(time.Millisecond); streak != 2 || w != 20*time.Millisecond {
		t.Fatalf("after two quick deaths: streak %d, wait %s", streak, w)
	}
	// A death after a real run is not part of it.
	w, streak, giveUp := a.restartPlan(time.Second)
	if giveUp || streak != 1 || w != 10*time.Millisecond {
		t.Errorf("after a long run: streak %d, wait %s, giveUp %v", streak, w, giveUp)
	}
}

// TestNoLimitKeepsTrying, for whoever wants the old behaviour back.
func TestNoLimitKeepsTrying(t *testing.T) {
	a, _ := restarting(t, &config.AgentConfig{
		Name:           "stubborn",
		Command:        []string{"sh", "-c", "exit 1"},
		RestartBackoff: time.Millisecond,
		RestartMaxWait: time.Millisecond,
		RestartMax:     0,
	})
	for i := range 50 {
		if _, _, giveUp := a.restartPlan(time.Microsecond); giveUp {
			t.Fatalf("gave up at %d with no limit set", i+1)
		}
	}
}
