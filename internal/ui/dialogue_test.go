package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/emmanuel-deloget/swarm/internal/agent"
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

// TestPageKeysGoWhereThereIsSomethingToPage: pgup did nothing at all in front
// of a full-screen agent. It scrolled this pane through a scrollback that an
// application on the alternate screen never fills — so the only way to page
// such an agent was to attach, press it, and detach again, for a key that had
// no other use meanwhile.
func TestPageKeysGoWhereThereIsSomethingToPage(t *testing.T) {
	for _, c := range []struct {
		what      string
		maxOffset int
		alt       bool
		here      bool
	}{
		{"a shell with history", 40, false, true},
		{"a shell with nothing above the screen", 0, false, false},
		{"a full-screen agent", 40, true, false},
		{"a full-screen agent with no scrollback", 0, true, false},
	} {
		in := &agent.Info{Name: "a", AltScreen: c.alt}
		if got := pagesHere(c.maxOffset, in); got != c.here {
			t.Errorf("%s: paging stays here = %v, want %v", c.what, got, c.here)
		}
	}

	// No agent at all is not a reason to write to one.
	if !pagesHere(10, nil) {
		t.Error("with no agent selected, a page key has nowhere else to go")
	}
}

// TestWheelGoesWhereTheAgentCanUseIt: the wheel had the same problem as the
// page keys — it scrolled a pane that a full-screen agent never fills — and
// one more of its own: what an agent expects for a wheel notch depends on
// whether it asked for the mouse at all.
func TestWheelGoesWhereTheAgentCanUseIt(t *testing.T) {
	// An agent tracking the mouse gets a mouse report, in the SGR encoding
	// swarm asks terminals for: 64 up, 65 down.
	if got := wheelBytes(true); got != "\x1b[<64;1;1M" {
		t.Errorf("wheel up = %q", got)
	}
	if got := wheelBytes(false); got != "\x1b[<65;1;1M" {
		t.Errorf("wheel down = %q", got)
	}

	// An agent that is not tracking the mouse is sent nothing. A terminal
	// would send arrows; swarm would be inventing keystrokes into an agent
	// whose arrows walk its prompt history — which is what it did on Windows,
	// where the agent's mouse mode is invisible to us.
	m := newTestModel(t)
	m.maxOffset = 0
	before := m.offset
	m.wheel(3, true)
	if m.offset != before {
		t.Error("the wheel moved a pane that had nothing to move")
	}
}

// TestClicksReachTheAgentAtTheRightCell: a click carries a position, and a
// position off by a row is worse than no click at all — it acts on the wrong
// line of somebody else's interface.
func TestClicksReachTheAgentAtTheRightCell(t *testing.T) {
	// The pane starts after the sidebar and its separator, two rows of window
	// header and two of the pane's own.
	const left = sidebarWidth + 1
	const top = 4

	// The first cell of the agent's screen is 1,1 in a mouse report.
	if col, row, ok := agentCell(left, top, 0); !ok || col != 1 || row != 1 {
		t.Errorf("the pane's first cell maps to %d,%d (ok=%v), want 1,1", col, row, ok)
	}
	// And one cell right and down is 2,2.
	if col, row, _ := agentCell(left+1, top+1, 0); col != 2 || row != 2 {
		t.Errorf("one cell in maps to %d,%d, want 2,2", col, row)
	}
	// An agent taller than the pane is showing its bottom rows, so what is on
	// the pane's first row is that far down its screen.
	if _, row, _ := agentCell(left, top, 12); row != 13 {
		t.Errorf("with 12 rows above the pane, its first row is the agent's %d, want 13", row)
	}
	// The sidebar and the header are not the agent's.
	for _, c := range [][2]int{{left - 1, top}, {left, top - 1}, {0, 0}} {
		if _, _, ok := agentCell(c[0], c[1], 0); ok {
			t.Errorf("%d,%d was taken for a cell of the agent", c[0], c[1])
		}
	}
}

// TestMouseReportsCarryTheButtonAndTheModifiers, in the SGR encoding swarm
// asks terminals for — the only one that survives past column 223.
func TestMouseReportsCarryTheButtonAndTheModifiers(t *testing.T) {
	press := func(b tea.MouseButton) tea.MouseMsg {
		return tea.MouseMsg{Button: b, Action: tea.MouseActionPress}
	}
	for _, c := range []struct {
		msg  tea.MouseMsg
		want string
	}{
		{press(tea.MouseButtonLeft), "\x1b[<0;3;4M"},
		{press(tea.MouseButtonMiddle), "\x1b[<1;3;4M"},
		{press(tea.MouseButtonRight), "\x1b[<2;3;4M"},
		{tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}, "\x1b[<0;3;4m"},
		{tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, Ctrl: true}, "\x1b[<16;3;4M"},
		{tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, Shift: true}, "\x1b[<4;3;4M"},
	} {
		if got := mouseReport(c.msg, 3, 4); got != c.want {
			t.Errorf("%v %v -> %q, want %q", c.msg.Button, c.msg.Action, got, c.want)
		}
	}
}

// TestDragsAreForwardedAsDrags: selecting text inside an agent is a press, a
// run of movements and a release. Without the movements the agent sees a click
// that never went anywhere.
func TestDragsAreForwardedAsDrags(t *testing.T) {
	drag := tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion}
	if got, want := mouseReport(drag, 3, 4), "\x1b[<32;3;4M"; got != want {
		t.Errorf("a left drag is %q, want %q", got, want)
	}
	right := tea.MouseMsg{Button: tea.MouseButtonRight, Action: tea.MouseActionMotion}
	if got, want := mouseReport(right, 1, 1), "\x1b[<34;1;1M"; got != want {
		t.Errorf("a right drag is %q, want %q", got, want)
	}
}
