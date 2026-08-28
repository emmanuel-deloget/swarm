package ui

import (
	"strings"
	"testing"
)

// TestTheMarkIsDrawnWhileAnAgentHasSaidNothing: an agent CLI can take a while
// to draw its first frame, and a blank pane during that is indistinguishable
// from one that failed to start.
//
// The condition is bytes, not lines. A running agent's terminal renders as a
// screenful of empty strings rather than no strings at all, so asking whether
// there are lines only ever answers "is there a terminal" — which is why the
// first version of this never drew anything.
func TestTheMarkIsDrawnWhileAnAgentHasSaidNothing(t *testing.T) {
	m := newTestModel(t)
	m.infos = m.h.Infos()
	if len(m.infos) == 0 {
		t.Fatal("no agents")
	}

	// A process is up and has printed nothing.
	m.infos[0].Pid = 1234
	m.infos[0].BytesOut = 0
	m.sel = 0
	pane := strings.Join(m.paneLines(80, 20), "\n")
	if !hasBraille(pane) {
		t.Errorf("the mark is not in the pane of an agent that has said nothing:\n%s", pane)
	}
	if !strings.Contains(pane, "is starting") {
		t.Error("the pane does not say what it is waiting for")
	}

	// The moment it prints anything, its own screen takes the pane back.
	m.infos[0].BytesOut = 12
	pane = strings.Join(m.paneLines(80, 20), "\n")
	if hasBraille(pane) {
		t.Errorf("the mark is still drawn after the agent produced output:\n%s", pane)
	}

	// And an agent with no process at all is not starting, it is stopped.
	m.infos[0].Pid = 0
	m.infos[0].BytesOut = 0
	pane = strings.Join(m.paneLines(80, 20), "\n")
	if hasBraille(pane) {
		t.Error("the mark is drawn for an agent that is not running")
	}
	if !strings.Contains(pane, "not running") {
		t.Errorf("a stopped agent does not say so:\n%s", pane)
	}
}

func hasBraille(s string) bool {
	for _, r := range s {
		if isBraille(r) {
			return true
		}
	}
	return false
}
