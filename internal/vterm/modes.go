package vterm

// Bracketed paste is a mode the *application* turns on (DECSET 2004). A real
// terminal only wraps a paste in the delimiters when the application asked for
// them; wrapping unconditionally leaves "^[[200~" sitting in the prompt of
// anything that did not.
//
// The emulator does not expose the mode, so we watch it go by in the output
// stream. The sequences are short and always sent verbatim, so a small
// carry-over buffer is enough to survive being split across two reads.

const modeBracketedPaste = 2004

// modeFocusEvent is DECSET 1004: the application asks to be told when its
// terminal gains or loses the focus. Agent CLIs use it to decide how much of
// their interface to draw — and one that enabled it and never hears back can
// stay in its unfocused rendering forever.
const modeFocusEvent = 1004

// modeSyncUpdate is DECSET 2026: the application is drawing a frame and asks
// that nothing be shown until it is done. Claude Code, and every other TUI that
// redraws a whole screen, brackets each frame with it.
//
// swarm watches it for a different reason than displaying: a frame is computed
// against a geometry, and resizing the emulator halfway through applies the
// second half of that computation to a screen that is no longer the one it was
// written for. See Terminal.Resize.
const modeSyncUpdate = 2026

// carryMax is how many bytes of a possibly-truncated sequence we keep between
// reads: ESC [ ? then a few parameters is well under this.
const carryMax = 32

// scanModes updates the tracked terminal modes from a chunk of output and
// returns the bytes to keep for the next call.
func (t *Terminal) scanModes(chunk []byte) []byte {
	buf := chunk
	if len(t.modeCarry) > 0 {
		// A fresh slice: appending onto modeCarry would let buf share its
		// backing array, and the tail of buf is copied back into it below.
		buf = make([]byte, 0, len(t.modeCarry)+len(chunk))
		buf = append(buf, t.modeCarry...)
		buf = append(buf, chunk...)
	}

	i := 0
	lastComplete := 0
	for i < len(buf) {
		// Look for CSI ? ... (h|l)
		if buf[i] != 0x1b {
			i++
			lastComplete = i
			continue
		}
		if i+2 >= len(buf) {
			break // possibly the start of a sequence: carry it over
		}
		if buf[i+1] != '[' || buf[i+2] != '?' {
			i++
			lastComplete = i
			continue
		}

		j := i + 3
		start := j
		for j < len(buf) && (isDigit(buf[j]) || buf[j] == ';') {
			j++
		}
		if j >= len(buf) {
			break // parameters not finished yet
		}
		final := buf[j]
		if final == 'h' || final == 'l' {
			if hasParam(buf[start:j], modeBracketedPaste) {
				t.bracketed.Store(final == 'h')
			}
			if hasParam(buf[start:j], modeSyncUpdate) {
				t.syncOn.Store(final == 'h')
			}
			if final == 'h' && hasParam(buf[start:j], modeFocusEvent) {
				// Answer once the emulator has seen the sequence too,
				// otherwise the mode is not set yet and the event is dropped.
				t.focusPending.Store(true)
			}
		}
		i = j + 1
		lastComplete = i
	}

	if lastComplete >= len(buf) {
		t.modeCarry = nil
		return nil
	}
	tail := buf[lastComplete:]
	if len(tail) > carryMax {
		tail = tail[len(tail)-carryMax:]
	}
	t.modeCarry = append(t.modeCarry[:0], tail...)
	return t.modeCarry
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// hasParam reports whether a ";"-separated parameter list contains want.
func hasParam(params []byte, want int) bool {
	value := 0
	seen := false
	for _, b := range params {
		if isDigit(b) {
			value = value*10 + int(b-'0')
			seen = true
			continue
		}
		if seen && value == want {
			return true
		}
		value, seen = 0, false
	}
	return seen && value == want
}

// BracketedPaste reports whether the application running in this terminal has
// asked for pasted text to be delimited.
func (t *Terminal) BracketedPaste() bool { return t.bracketed.Load() }

// SetFocus tells the agent whether the window showing it has the focus. It is a
// no-op until the application enables focus reporting, and the state is resent
// when it does.
func (t *Terminal) SetFocus(focused bool) {
	if t.focused.Swap(focused) == focused {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sendFocusLocked()
}

// Focused reports what swarm last told the agent.
func (t *Terminal) Focused() bool { return t.focused.Load() }

// sendFocusLocked hands the focus state to the emulator, which forwards it to
// the child only if focus reporting is on. The caller holds mu; the emulator
// writes into a pipe drained by replyReadLoop, which does not take mu.
func (t *Terminal) sendFocusLocked() {
	if t.focused.Load() {
		t.emu.Focus()
	} else {
		t.emu.Blur()
	}
}
