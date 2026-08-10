package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Eighteen letters are shortcuts, three of them acting on an agent's life.
// Typing "merci" at a fleet cycled the mosaic, restarted an agent and opened an
// inject line — when all that was meant was to talk to the agent on screen.

func normal(m *model, k tea.KeyMsg) { m.handleKey(k) }

// TestTypingReachesTheAgentUnderTheLock, carrying the letter that started it.
func TestTypingReachesTheAgentUnderTheLock(t *testing.T) {
	m := newTestModel(t)
	m.prefs.dialogue = true

	normal(m, runes("m"))
	if m.mode != modeCommand {
		t.Fatalf("typing did not open the line (mode %v)", m.mode)
	}
	if got, want := m.input.Value(), "inject dev-1 m"; got != want {
		t.Errorf("the line reads %q, want %q", got, want)
	}

	// And the rest of the word follows normally.
	for _, r := range "erci" {
		m.handleCommandKey(runes(string(r)))
	}
	if got, want := m.input.Value(), "inject dev-1 merci"; got != want {
		t.Errorf("the line reads %q, want %q", got, want)
	}
}

// TestTheShortcutsAreStillReachable behind esc, one key at a time.
func TestTheShortcutsAreStillReachable(t *testing.T) {
	m := newTestModel(t)
	m.prefs.dialogue = true

	normal(m, tea.KeyMsg{Type: tea.KeyEsc})
	if !m.escNext {
		t.Fatal("esc did not arm the prefix")
	}
	normal(m, runes("m"))
	if m.mode != modeMosaic {
		t.Errorf("esc m did not reach the mosaic shortcut (mode %v)", m.mode)
	}
	if m.escNext {
		t.Error("the prefix outlived the key it was for")
	}
}

// TestTheLockDoesNotSwallowNavigation: arrows and tab are not text, so they go
// on selecting agents.
func TestTheLockDoesNotSwallowNavigation(t *testing.T) {
	m := newTestModel(t)
	m.prefs.dialogue = true
	first := m.currentName()

	normal(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.mode == modeCommand {
		t.Fatal("the down arrow opened a line")
	}
	if m.currentName() == first {
		t.Error("the down arrow did not move the selection")
	}
}

// TestWithoutTheLockNothingChanges: the default is what it always was.
func TestWithoutTheLockNothingChanges(t *testing.T) {
	m := newTestModel(t)
	m.prefs.dialogue = false

	normal(m, runes("m"))
	if m.mode != modeMosaic {
		t.Errorf("m did not reach the mosaic (mode %v)", m.mode)
	}
}

// TestTheLockIsRememberedWhenTurnedOff: on is the default, so the setting worth
// remembering is the one that says otherwise.
func TestTheLockIsRememberedWhenTurnedOff(t *testing.T) {
	dir := t.TempDir()
	p := loadPrefs(dir)
	if !p.dialogue {
		t.Error("the lock is off by default")
	}
	p.dialogue = false
	p.save()

	if again := loadPrefs(dir); again.dialogue {
		t.Error("turning the lock off was not remembered")
	}
	st, err := os.Stat(filepath.Join(dir, prefsFile))
	if err != nil {
		t.Fatal(err)
	}
	// Unix only: Windows reports every readable file as 0666, and who may
	// open it is decided by an ACL that mode bits cannot express.
	if perm := st.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o600 {
		t.Errorf("the preferences file is %o, want 600", perm)
	}
}

// TestAFirstRunTalksToTheAgent: nobody should have to find a switch before the
// keyboard does the obvious thing.
func TestAFirstRunTalksToTheAgent(t *testing.T) {
	p := loadPrefs(filepath.Join(t.TempDir(), "nowhere"))
	if !p.dialogue {
		t.Error("a fleet with no preferences file starts with the lock off")
	}
	p.save() // must not panic
}

// TestTheBarSaysWhichModeYouAreIn: an invisible mode is how the next surprise
// happens.
func TestTheBarSaysWhichModeYouAreIn(t *testing.T) {
	m := newTestModel(t)
	m.prefs.dialogue = true
	if bar := m.statusLine(); !strings.Contains(bar, "DIALOGUE") {
		t.Errorf("the bar does not show the lock: %q", bar)
	}
	m.prefs.dialogue = false
	if bar := m.statusLine(); strings.Contains(bar, "DIALOGUE") {
		t.Errorf("the bar still shows the lock when it is off: %q", bar)
	}
}

// TestEscTwiceLeavesTheLock: one esc is "let me do one thing", two is "give me
// the keyboard back" — and the second is remembered.
func TestEscTwiceLeavesTheLock(t *testing.T) {
	m := newTestModel(t)
	m.prefs.dialogue = true
	m.prefs.path = filepath.Join(t.TempDir(), prefsFile)

	normal(m, tea.KeyMsg{Type: tea.KeyEsc})
	normal(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.prefs.dialogue {
		t.Fatal("esc esc did not leave the lock")
	}
	normal(m, runes("m"))
	if m.mode != modeMosaic {
		t.Errorf("after leaving, m did not reach the mosaic (mode %v)", m.mode)
	}
	if again := loadPrefs(filepath.Dir(m.prefs.path)); again.dialogue {
		t.Error("leaving the lock was not remembered")
	}
}

// TestTheBarOffersTheWayBackIn: with the lock off, nothing on screen says the
// lock exists — and a feature you cannot see is one you do not use.
func TestTheBarOffersTheWayBackIn(t *testing.T) {
	m := newTestModel(t)
	m.prefs.dialogue = false
	if bar := m.statusLine(); !strings.Contains(bar, "dialogue") {
		t.Errorf("the shortcut bar never mentions dialogue: %q", bar)
	}
}

// TestTheLockStillOffersAttach: ↵ is not text, so the lock never sees it and
// attaching goes on working — but a way in that nothing points at is a way in
// nobody takes.
func TestTheLockStillOffersAttach(t *testing.T) {
	m := newTestModel(t)
	m.prefs.dialogue = true

	bar := m.statusLine()
	if !strings.Contains(bar, "attach") {
		t.Errorf("the dialogue bar never mentions attaching: %q", bar)
	}
	if !strings.Contains(bar, "DIALOGUE") {
		t.Errorf("the dialogue bar stopped saying which mode it is: %q", bar)
	}

	// And ↵ really does reach the shortcut. The agents of a test model are not
	// running, so attaching refuses — and that refusal is the proof: had the
	// lock swallowed the key, there would be neither a mode change nor a word
	// about it.
	normal(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode == modeCommand {
		t.Fatal("↵ under the lock opened the dialogue line")
	}
	if m.status == "" {
		t.Error("↵ under the lock reached nothing at all")
	}
}

// TestAttachedWinsOverTheLock: once attached the keyboard belongs to the agent,
// whatever the lock says, and a bar claiming otherwise is worse than none.
func TestAttachedWinsOverTheLock(t *testing.T) {
	m := newTestModel(t)
	m.prefs.dialogue = true
	m.mode = modeAttached

	bar := m.statusLine()
	if strings.Contains(bar, "DIALOGUE") {
		t.Errorf("the bar still claims dialogue while attached: %q", bar)
	}
	if !strings.Contains(bar, "ATTACHED") {
		t.Errorf("the bar does not say it is attached: %q", bar)
	}
}
