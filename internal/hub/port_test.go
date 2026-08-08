package hub

import "testing"

// TestPortForIsStable: an agent that restarts must keep its port, or whatever
// was pointing at its server is now pointing at nothing.
func TestPortForIsStable(t *testing.T) {
	h := &Hub{}
	first := h.portFor("dev-1", "PORT")
	if first == 0 {
		t.Fatal("no port was allocated")
	}
	if again := h.portFor("dev-1", "PORT"); again != first {
		t.Errorf("the port moved between calls: %d then %d", first, again)
	}
}

// TestPortForIsPerAgentAndPerVariable: two agents both want 3000, which is the
// whole reason this exists.
func TestPortForIsPerAgentAndPerVariable(t *testing.T) {
	h := &Hub{}
	a := h.portFor("dev-1", "PORT")
	b := h.portFor("dev-2", "PORT")
	second := h.portFor("dev-1", "DEBUG_PORT")
	if a == b {
		t.Errorf("two agents were handed the same port %d", a)
	}
	if a == second {
		t.Errorf("two variables of one agent were handed the same port %d", a)
	}
}

func TestFreePortIsUsable(t *testing.T) {
	if p := freePort(); p < 1024 || p > 65535 {
		t.Errorf("freePort = %d, which is not a port anyone can bind", p)
	}
}
