package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/emmanuel-deloget/swarm/internal/vterm"
)

// completes says what tab should offer for a command's arguments.
type completes int

const (
	// completesNothing is for free text: there is nothing to guess.
	completesNothing completes = iota
	// completesTarget offers agents, groups, roles and "all".
	completesTarget
	// completesKeys offers key names, after a target.
	completesKeys
	// completesPath offers file names, after a target.
	completesPath
)

// command is one verb of the ":" line. The same table drives the help screen and
// tab completion, so a command cannot be offered without being documented.
type command struct {
	name string
	// args is the argument summary shown in the help.
	args string
	help string
	// arg1 is what tab completes as the first argument, arg2 for the rest.
	arg1, arg2 completes
}

var commands = []command{
	{"inject", "<target> <text>", "type text and submit it", completesTarget, completesNothing},
	{"type", "<target> <text>", "type text without submitting", completesTarget, completesNothing},
	{"keys", "<target> <keys>", "send key presses", completesTarget, completesKeys},
	{"send", "<target> <text>", "bus message, from you", completesTarget, completesNothing},
	{"broadcast", "<text>", "bus message to everyone", completesNothing, completesNothing},
	{"file", "<target> <path>", "stage a file and inject its path", completesTarget, completesPath},
	{"start", "<target>", "start the agents", completesTarget, completesNothing},
	{"stop", "<target>", "stop the agents", completesTarget, completesNothing},
	{"restart", "<target>", "restart the agents", completesTarget, completesNothing},
	{"resize", "<target> <cols>x<rows>", "set the terminal size by hand", completesTarget, completesNothing},
	{"web", "", "show the remote-control URL", completesNothing, completesNothing},
	{"shared", "", "show the shared directory", completesNothing, completesNothing},
	{"help", "", "this screen", completesNothing, completesNothing},
	{"q", "", "quit", completesNothing, completesNothing},
}

func lookupCommand(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

// complete advances the command line by one tab press.
//
// One candidate is filled in outright. Several that share more than what was
// typed extend the word to the part they agree on, and stop there — the user
// may know what comes next better than the first candidate does. Several that
// agree on nothing more put the first one on the line and list the rest;
// pressing tab again on an untouched line moves to the next, and round again.
func (m *model) complete() {
	line := m.input.Value()

	// Cycling: the line is still exactly what the last completion produced.
	if m.completeAt != "" && line == m.completeAt && len(m.completions) > 1 {
		m.completeIdx = (m.completeIdx + 1) % len(m.completions)
		m.applyCompletion(m.completions[m.completeIdx])
		return
	}

	head, word := splitLastWord(line)
	candidates := m.candidatesFor(head, word)
	if len(candidates) == 0 {
		m.status = "nothing to complete here"
		return
	}

	if len(candidates) == 1 {
		m.completions, m.completeIdx = nil, 0
		m.completeAt = ""
		m.input.SetValue(head + candidates[0] + " ")
		m.input.CursorEnd()
		m.status = ""
		return
	}

	m.completions, m.completeIdx = candidates, 0
	if shared := commonPrefix(candidates); len(shared) > len(word) {
		// Extend as far as they agree, and stop: the user may know what comes
		// next better than the first candidate does.
		m.completeAt = ""
		m.input.SetValue(head + shared)
		m.input.CursorEnd()
	} else {
		m.applyCompletion(candidates[0])
	}
	m.status = strings.Join(candidates, "  ")
}

// applyCompletion puts one candidate on the line and remembers the result, so
// the next tab knows it is cycling rather than starting over.
func (m *model) applyCompletion(candidate string) {
	head, _ := splitLastWord(m.input.Value())
	value := head + candidate
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.completeAt = value
	m.status = strings.Join(m.completions, "  ")
}

// candidatesFor returns what may follow, given everything typed before the word
// being completed.
func (m *model) candidatesFor(head, word string) []string {
	fields := strings.Fields(head)

	// Still on the verb.
	if len(fields) == 0 {
		return matching(commandNames(), word)
	}

	spec, ok := lookupCommand(fields[0])
	if !ok {
		return nil
	}
	kind := spec.arg2
	if len(fields) == 1 {
		kind = spec.arg1
	}

	switch kind {
	case completesTarget:
		return matching(m.targetNames(), word)
	case completesKeys:
		return matching(vterm.KeyNames(), word)
	case completesPath:
		return m.pathCandidates(word)
	default:
		return nil
	}
}

func commandNames() []string {
	out := make([]string, 0, len(commands))
	for _, c := range commands {
		out = append(out, c.name)
	}
	sort.Strings(out)
	return out
}

// targetNames is everything accepted where a target is expected.
func (m *model) targetNames() []string {
	cfg := m.h.Config()
	out := []string{"all"}
	out = append(out, m.h.Names()...)

	seen := map[string]bool{}
	for name := range cfg.Groups {
		out = append(out, "@"+name)
		seen[name] = true
	}
	for i := range cfg.Agents {
		if role := cfg.Agents[i].Role; role != "" && !seen[role] {
			seen[role] = true
			out = append(out, "@"+role)
		}
	}
	sort.Strings(out)
	return out
}

// pathCandidates completes a file name against the directory it names, so
// `:file` does not require typing a path from memory.
func (m *model) pathCandidates(word string) []string {
	dir, prefix := filepath.Split(word)
	lookup := dir
	if lookup == "" {
		lookup = m.h.Config().Dir()
	} else if strings.HasPrefix(lookup, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			lookup = filepath.Join(home, lookup[2:])
		}
	}

	entries, err := os.ReadDir(lookup)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if e.IsDir() {
			name += string(filepath.Separator)
		}
		out = append(out, dir+name)
	}
	sort.Strings(out)
	return out
}

// splitLastWord cuts a line into everything up to the word being completed, and
// that word. A trailing space means the word is empty: a new argument starts.
func splitLastWord(line string) (head, word string) {
	i := strings.LastIndexAny(line, " \t")
	if i < 0 {
		return "", line
	}
	return line[:i+1], line[i+1:]
}

func matching(candidates []string, prefix string) []string {
	var out []string
	for _, c := range candidates {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func commonPrefix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	prefix := values[0]
	for _, v := range values[1:] {
		for !strings.HasPrefix(v, prefix) {
			prefix = prefix[:len(prefix)-1]
			if prefix == "" {
				return ""
			}
		}
	}
	return prefix
}
