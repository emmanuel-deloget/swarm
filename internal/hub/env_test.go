package hub

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// swarmVars returns the SWARM_* variables an agent is actually given.
func swarmVars(t *testing.T, h *Hub) map[string]string {
	t.Helper()
	a, err := h.Agent("alpha")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, kv := range h.agentEnv(a.Config(), filepath.Join(h.StateDir(), "bin")) {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, "SWARM_") {
			out[k] = v
		}
	}
	return out
}

const oneAgent = `
web: {enabled: false}
agents:
  - name: alpha
    role: dev
    command: [cat]
`

// TestEveryVariableIsDocumented: a variable an agent is given and nobody wrote
// down is a variable nobody uses. This is the check that keeps the two lists
// together — it fails when a variable is added, which is the point.
func TestEveryVariableIsDocumented(t *testing.T) {
	h := fleet(t, oneAgent)

	for _, doc := range []string{"../../README.md", "../../docs/configuration.md"} {
		body, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		var missing []string
		for name := range swarmVars(t, h) {
			if !strings.Contains(string(body), "$"+name) {
				missing = append(missing, name)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s never mentions %s", doc, strings.Join(missing, ", "))
		}
	}
}

// TestRootIsWhereTheConfigIs: the one path an agent cannot work out for itself.
// Its own clone is somewhere under the state directory, and relative paths in
// the config mean nothing from there.
func TestRootIsWhereTheConfigIs(t *testing.T) {
	h := fleet(t, oneAgent)
	vars := swarmVars(t, h)

	want := h.Config().Dir()
	if got := vars["SWARM_ROOT"]; got != want {
		t.Errorf("SWARM_ROOT is %q, want the config's directory %q", got, want)
	}
	for _, name := range []string{"SWARM_ROOT", "SWARM_SHARED", "SWARM_STATE_DIR", "SWARM_SOCKET"} {
		if v := vars[name]; !filepath.IsAbs(v) {
			t.Errorf("%s is %q, which is relative — it would mean something else in a clone", name, v)
		}
	}
}

// TestNoLinesOrColumns: an agent is resized to the pane showing it, and nobody
// can change a running process's environment — so LINES and COLUMNS could only
// ever be the geometry at launch, wrong from the first relayout onwards.
//
// Python's shutil.get_terminal_size, which Textual and Rich are built on,
// prefers them to asking the terminal. Mistral Vibe kept drawing for the 40
// rows it was started with on a 33-row pty, losing the top of every dialog.
func TestNoLinesOrColumns(t *testing.T) {
	t.Setenv("LINES", "40")
	t.Setenv("COLUMNS", "160")

	h := fleet(t, oneAgent)
	a, err := h.Agent("alpha")
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range h.agentEnv(a.Config(), "") {
		k, v, _ := strings.Cut(kv, "=")
		if k == "LINES" || k == "COLUMNS" {
			t.Errorf("%s=%s reaches the agent; it would be a stale promise about the geometry", k, v)
		}
	}
}
