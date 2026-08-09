package ui

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// The command line is one field opened with a different prefix depending on the
// key that opened it — i for inject, s for send, K for keys. So an agent's name
// and the shape of what follows differ per verb, and one flat history would
// mostly offer lines that cannot be reused. The history is therefore filtered
// by verb: opening with `s` walks back through what was sent, not through the
// last file staged.
//
// A line typed into a fleet is worth keeping across restarts — a long prompt
// especially — so it is written to the state directory. It holds what you
// typed, which is why the file is 0600, like the input log.

const (
	// historyMax bounds the file. Old enough lines name agents that no longer
	// exist and paths that have moved.
	historyMax = 500
	// historyFile lives beside the logs, under the state directory.
	historyFile = "history"
)

// history is the lines validated at the command line, oldest first.
type history struct {
	path  string
	lines []string

	// walk is where ↑ has reached, as an index into the filtered view, and
	// pending is what was on the line before walking began, so ↓ can put it
	// back. Both reset when the line is opened.
	walk    int
	pending string

	// search is the reverse-i-search state: term is what has been typed into
	// it, at the index it matched, and base the line to restore on cancel.
	searching bool
	term      string
	at        int
	base      string
}

// loadHistory reads the file, missing or unreadable being a fresh history
// rather than an error: not remembering is a poor reason to refuse to start.
func loadHistory(stateDir string) *history {
	h := &history{path: filepath.Join(stateDir, historyFile), walk: -1}
	f, err := os.Open(h.path)
	if err != nil {
		return h
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			h.lines = append(h.lines, line)
		}
	}
	if len(h.lines) > historyMax {
		h.lines = h.lines[len(h.lines)-historyMax:]
	}
	return h
}

// add records a validated line and writes it out. A repeat of the last line is
// dropped: pressing enter twice should not cost two entries of ↑.
func (h *history) add(line string) {
	line = strings.TrimSpace(line)
	if line == "" || (len(h.lines) > 0 && h.lines[len(h.lines)-1] == line) {
		return
	}
	h.lines = append(h.lines, line)
	if len(h.lines) > historyMax {
		h.lines = h.lines[len(h.lines)-historyMax:]
	}
	h.save()
}

// save rewrites the file. Rewriting rather than appending keeps it bounded
// without a second pass, and the file is small enough that it costs nothing.
func (h *history) save() {
	if h.path == "" {
		return
	}
	var b strings.Builder
	for _, l := range h.lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	_ = os.WriteFile(h.path, []byte(b.String()), 0o600)
}

// forVerb returns the lines that belong to a verb, oldest first. An empty verb
// — the bare `:` command line — sees everything, since anything can be typed
// there.
func (h *history) forVerb(verb string) []string {
	if verb == "" {
		return h.lines
	}
	var out []string
	for _, l := range h.lines {
		if v, _ := cut(l); v == verb {
			out = append(out, l)
		}
	}
	return out
}

// begin resets the walk for a newly opened line.
func (h *history) begin() {
	h.walk, h.pending = -1, ""
	h.searching, h.term, h.at, h.base = false, "", -1, ""
}

// prev walks one entry back, returning the line to show and whether there was
// one. current is what is on the line now, kept so ↓ can return to it.
func (h *history) prev(verb, current string) (string, bool) {
	lines := h.forVerb(verb)
	if len(lines) == 0 {
		return "", false
	}
	if h.walk < 0 {
		h.pending = current
		h.walk = len(lines)
	}
	if h.walk == 0 {
		return "", false
	}
	h.walk--
	return lines[h.walk], true
}

// next walks forward, and past the newest entry restores what was being typed.
func (h *history) next(verb string) (string, bool) {
	lines := h.forVerb(verb)
	if h.walk < 0 || len(lines) == 0 {
		return "", false
	}
	h.walk++
	if h.walk >= len(lines) {
		h.walk = -1
		return h.pending, true
	}
	return lines[h.walk], true
}

// startSearch enters reverse-i-search, remembering the line to restore.
func (h *history) startSearch(current string) {
	h.searching, h.term, h.at, h.base = true, "", -1, current
}

// searchNext looks for the newest entry before `at` containing term. It returns
// the match and whether one was found; a failed search leaves the state alone,
// so the term stays on screen and one more character can rescue it.
func (h *history) searchNext(verb string, from int) (string, int, bool) {
	lines := h.forVerb(verb)
	if h.term == "" || len(lines) == 0 {
		return "", -1, false
	}
	if from < 0 || from > len(lines) {
		from = len(lines)
	}
	for i := from - 1; i >= 0; i-- {
		if strings.Contains(lines[i], h.term) {
			return lines[i], i, true
		}
	}
	return "", -1, false
}

// searchPrompt is what replaces the usual prompt while searching, in the shape
// readline made familiar.
func (h *history) searchPrompt(found bool) string {
	if found || h.term == "" {
		return "(reverse-i-search)`" + h.term + "': "
	}
	return "(failed reverse-i-search)`" + h.term + "': "
}
