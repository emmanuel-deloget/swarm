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
