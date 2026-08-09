package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newHistory(t *testing.T, lines ...string) *history {
	t.Helper()
	h := loadHistory(t.TempDir())
	h.begin()
	for _, l := range lines {
		h.add(l)
	}
	h.begin()
	return h
}

// The command line is opened with a different prefix per key, so one flat
// history would mostly offer lines that cannot be reused where you are.

// TestUpWalksOnlyTheVerbYouOpenedWith is the whole point of a history per mode.
func TestUpWalksOnlyTheVerbYouOpenedWith(t *testing.T) {
	h := newHistory(t,
		"send alpha first",
		"inject alpha ls",
		"send alpha second",
		"keys alpha esc",
	)

	var got []string
	for {
		line, ok := h.prev("send", "send alpha ")
		if !ok {
			break
		}
		got = append(got, line)
	}
	want := []string{"send alpha second", "send alpha first"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("walking back through `send` gave %v, want %v", got, want)
	}
}

// TestTheBareCommandLineSeesEverything: `:` can be given any verb, so filtering
// it would hide most of what was typed there.
func TestTheBareCommandLineSeesEverything(t *testing.T) {
	h := newHistory(t, "send alpha one", "keys alpha esc")
	if line, _ := h.prev("", ""); line != "keys alpha esc" {
		t.Errorf("the bare line walked back to %q", line)
	}
}

// TestDownReturnsWhatYouWereWriting: walking away from a half-written line and
// coming back must not lose it.
func TestDownReturnsWhatYouWereWriting(t *testing.T) {
	h := newHistory(t, "send alpha done")

	if _, ok := h.prev("send", "send alpha half-writ"); !ok {
		t.Fatal("nothing to walk back to")
	}
	line, ok := h.next("send")
	if !ok || line != "send alpha half-writ" {
		t.Errorf("coming forward gave %q (ok=%v)", line, ok)
	}
}

// TestARepeatCostsNothing: pressing enter twice should not cost two presses of
// up to get past.
func TestARepeatCostsNothing(t *testing.T) {
	h := newHistory(t, "send alpha same", "send alpha same")
	if n := len(h.forVerb("send")); n != 1 {
		t.Errorf("%d entries for a line typed twice", n)
	}
}

// TestSearchFindsTheNewestFirst, then walks back through the older matches.
func TestSearchFindsTheNewestFirst(t *testing.T) {
	h := newHistory(t,
		"send alpha look at the parser",
		"send alpha unrelated",
		"send alpha parser again",
	)
	h.startSearch("")
	h.term = "parser"

	line, at, ok := h.searchNext("send", -1)
	if !ok || line != "send alpha parser again" {
		t.Fatalf("first match was %q", line)
	}
	line, _, ok = h.searchNext("send", at)
	if !ok || line != "send alpha look at the parser" {
		t.Errorf("second match was %q", line)
	}
}

// TestAFailedSearchSaysSo rather than silently showing the last match.
func TestAFailedSearchSaysSo(t *testing.T) {
	h := newHistory(t, "send alpha hello")
	h.startSearch("")
	h.term = "nowhere"

	if _, _, ok := h.searchNext("send", -1); ok {
		t.Error("a term that is in nothing found something")
	}
	if p := h.searchPrompt(false); !strings.Contains(p, "failed") {
		t.Errorf("the prompt should say the search failed: %q", p)
	}
}

// TestSearchStaysWithinTheVerb: ctrl+r from `s` must not offer an inject.
func TestSearchStaysWithinTheVerb(t *testing.T) {
	h := newHistory(t, "inject alpha parser", "send alpha other")
	h.startSearch("")
	h.term = "parser"

	if line, _, ok := h.searchNext("send", -1); ok {
		t.Errorf("searching `send` found %q", line)
	}
}

// TestItSurvivesARestart, because a long prompt is worth retyping never.
func TestItSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	h := loadHistory(dir)
	h.add("send alpha remember me")

	again := loadHistory(dir)
	if got := again.forVerb("send"); len(got) != 1 || got[0] != "send alpha remember me" {
		t.Errorf("after reloading: %v", got)
	}

	st, err := os.Stat(filepath.Join(dir, historyFile))
	if err != nil {
		t.Fatal(err)
	}
	// It holds what you typed into a fleet, like the input log does.
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("the history file is %o, want 600", perm)
	}
}

// TestItStaysBounded: old entries name agents that no longer exist.
func TestItStaysBounded(t *testing.T) {
	h := loadHistory(t.TempDir())
	for i := range historyMax + 50 {
		h.add("send alpha " + strings.Repeat("x", i%7+1) + string(rune('a'+i%26)))
	}
	if len(h.lines) > historyMax {
		t.Errorf("%d entries kept, want at most %d", len(h.lines), historyMax)
	}
}

// TestAMissingFileIsNotAnError: not remembering is a poor reason to refuse to
// start.
func TestAMissingFileIsNotAnError(t *testing.T) {
	h := loadHistory(filepath.Join(t.TempDir(), "does", "not", "exist"))
	if len(h.lines) != 0 {
		t.Errorf("lines from nowhere: %v", h.lines)
	}
	h.add("send alpha x") // must not panic
}

// The tests above drive the history directly; these go through the keyboard,
// because the wiring is where a feature like this actually breaks.

func press(m *model, k tea.KeyMsg) { m.handleCommandKey(k) }

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// TestCtrlRThenTypingFindsTheLine, and enter runs what it found.
func TestCtrlRThenTypingFindsTheLine(t *testing.T) {
	m := newTestModel(t)
	m.hist.add("send dev-1 look at the parser")
	m.hist.add("send dev-1 something else")

	m.openCommand("send dev-1 ")
	press(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if !strings.Contains(m.input.Prompt, "reverse-i-search") {
		t.Fatalf("ctrl+r did not change the prompt: %q", m.input.Prompt)
	}
	for _, r := range "parser" {
		press(m, runes(string(r)))
	}
	if got := m.input.Value(); got != "send dev-1 look at the parser" {
		t.Errorf("the search left %q on the line", got)
	}
}

// TestEscapeRestoresWhatYouWereWriting: a cancelled search must not eat the
// line it was started from.
func TestEscapeRestoresWhatYouWereWriting(t *testing.T) {
	m := newTestModel(t)
	m.hist.add("send dev-1 old line")

	m.openCommand("send dev-1 ")
	press(m, runes("half"))
	before := m.input.Value()

	press(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	press(m, runes("old"))
	press(m, tea.KeyMsg{Type: tea.KeyEsc})

	if m.hist.searching {
		t.Error("escape left the search on")
	}
	if got := m.input.Value(); got != before {
		t.Errorf("the line came back as %q, want %q", got, before)
	}
	if m.input.Prompt != commandPrompt {
		t.Errorf("the prompt stayed at %q", m.input.Prompt)
	}
}

// TestUpFromAnEmptyHistoryDoesNothing, rather than clearing the prefix the key
// put there.
func TestUpFromAnEmptyHistoryDoesNothing(t *testing.T) {
	m := newTestModel(t)
	m.openCommand("keys dev-1 ")
	press(m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.input.Value(); got != "keys dev-1 " {
		t.Errorf("up on an empty history left %q", got)
	}
}

// TestOpeningAgainStartsFromTheNewest: the walk is per opening, not global.
func TestOpeningAgainStartsFromTheNewest(t *testing.T) {
	m := newTestModel(t)
	m.hist.add("send dev-1 one")
	m.hist.add("send dev-1 two")

	m.openCommand("send dev-1 ")
	press(m, tea.KeyMsg{Type: tea.KeyUp})
	press(m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.input.Value(); got != "send dev-1 one" {
		t.Fatalf("two ups gave %q", got)
	}

	m.openCommand("send dev-1 ")
	press(m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.input.Value(); got != "send dev-1 two" {
		t.Errorf("reopening then up gave %q, want the newest", got)
	}
}

// TestKeyNamesCanBeTyped: bubbles matches its bindings on the message's
// String(), and a run of runes stringifies to the runes themselves — so typing
// u then p quickly produced a message the field read as the up arrow and
// swallowed. Every name below is one you would want after `:keys`, and an
// ordinary word you might send to an agent.
func TestKeyNamesCanBeTyped(t *testing.T) {
	for _, word := range []string{
		"up", "down", "left", "right", "home", "end", "delete", "backspace",
		"tab", "esc", "enter", "space", "pgup", "f1", "hello",
	} {
		m := newTestModel(t)
		m.openCommand("keys dev-1 ")
		press(m, runes(word))
		if got, want := m.input.Value(), "keys dev-1 "+word; got != want {
			t.Errorf("typing %q left %q", word, got)
		}
	}
}

// TestSeveralKeysInOneLine is what the K key is for.
func TestSeveralKeysInOneLine(t *testing.T) {
	m := newTestModel(t)
	m.openCommand("keys dev-1 ")
	for _, part := range []string{"up", " ", "up", " ", "down"} {
		press(m, runes(part))
	}
	if got, want := m.input.Value(), "keys dev-1 up up down"; got != want {
		t.Errorf("the line reads %q, want %q", got, want)
	}
}

// TestTheRealArrowStillWalksTheHistory: splitting runes must not blunt the key
// itself.
func TestTheRealArrowStillWalksTheHistory(t *testing.T) {
	m := newTestModel(t)
	m.hist.add("keys dev-1 esc")
	m.openCommand("keys dev-1 ")
	press(m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.input.Value(); got != "keys dev-1 esc" {
		t.Errorf("the up arrow gave %q", got)
	}
}

// TestAPasteIsNotSplit: a long run cannot collide with a binding, and feeding
// it in one rune at a time would be quadratic.
func TestAPasteIsNotSplit(t *testing.T) {
	m := newTestModel(t)
	m.openCommand("send dev-1 ")
	long := strings.Repeat("some pasted text ", 20)
	press(m, runes(long))
	if got, want := m.input.Value(), "send dev-1 "+long; got != want {
		t.Errorf("a paste arrived as %q", got)
	}
}
