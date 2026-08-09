package vterm

import (
	"bytes"
	"strconv"
)

// The parser this emulator is built on leaves an OSC string when it meets the
// byte 0x9C, the C1 form of the String Terminator — even when that byte is the
// middle of a UTF-8 character rather than a control. The whole Dingbats block
// U+2700–U+273F encodes with 0x9C as its second byte, and an agent CLI whose
// spinner cycles through ✳ ✻ ✶ ✽ puts one in its window title several times a
// second. The title is then cut in half and its tail printed onto the screen,
// wherever the cursor happens to be — which, in a full-screen TUI, is the
// prompt.
//
// Upstream: github.com/charmbracelet/x#848, open since April with no fix.
//
// So sequences that would be mis-parsed are kept away from the parser and
// handled here instead. Everything else is passed through untouched, and the
// cost when no such byte is in sight is one scan of the chunk.
const (
	// c1ST is the byte that ends the string too early.
	c1ST = 0x9c
	// strMax bounds what is held while waiting for a terminator. An OSC that
	// long is not a title; rather than buffer without end, hand it over and let
	// the parser do whatever it did before.
	strMax = 1 << 16
)

// oscSafe returns the chunk with every unparseable string sequence removed,
// having acted on it here. It is called from the read loop only, so the state
// it carries between chunks needs no lock.
//
// The sequence has to be tracked whether or not the offending byte is in this
// chunk: a title arrives split across reads often enough, and the byte that
// breaks it can land either side of the boundary.
func (t *Terminal) oscSafe(chunk []byte) []byte {
	// Nothing open and no escape at all: nothing can begin or end here.
	if !t.strOpen && !t.strEsc && bytes.IndexByte(chunk, 0x1b) < 0 {
		return chunk
	}

	out := make([]byte, 0, len(chunk))
	i := 0
	if t.strEsc {
		// An ESC ended the previous chunk: it opens a string only if a ']'
		// follows it here.
		t.strEsc = false
		if len(chunk) > 0 && chunk[0] == ']' {
			t.strOpen, t.strBuf = true, []byte{0x1b, ']'}
			i = 1
		} else {
			out = append(out, 0x1b)
		}
	}

	for i < len(chunk) {
		if !t.strOpen {
			// Outside a string, copy in one go up to the next escape.
			k := bytes.IndexByte(chunk[i:], 0x1b)
			if k < 0 {
				return append(out, chunk[i:]...)
			}
			out = append(out, chunk[i:i+k]...)
			i += k
			if i == len(chunk)-1 {
				t.strEsc = true
				return out
			}
			if chunk[i+1] == ']' {
				t.strOpen, t.strBuf = true, []byte{0x1b, ']'}
				i += 2
				continue
			}
			out = append(out, chunk[i])
			i++
			continue
		}

		// Inside one: byte by byte, watching for BEL or ESC \.
		b := chunk[i]
		i++
		t.strBuf = append(t.strBuf, b)
		switch {
		case b == 0x07, b == '\\' && len(t.strBuf) > 1 && t.strBuf[len(t.strBuf)-2] == 0x1b:
			out = append(out, t.closeString()...)
		case len(t.strBuf) > strMax:
			out = append(out, t.strBuf...)
			t.strBuf, t.strOpen = nil, false
		}
	}
	return out
}

// closeString ends the collected sequence: it goes through untouched unless it
// carries the byte that would truncate it, in which case it is acted on here
// and dropped.
func (t *Terminal) closeString() []byte {
	buf := t.strBuf
	t.strBuf, t.strOpen = nil, false
	if bytes.IndexByte(buf, c1ST) < 0 {
		return buf
	}
	t.handleString(buf)
	return nil
}

// handleString does what the emulator would have done with a sequence it can no
// longer be given. Only titles matter in practice — they are what carries a
// spinner — and anything else is dropped rather than guessed at, which is still
// better than half of it landing on the screen.
func (t *Terminal) handleString(seq []byte) {
	body := bytes.TrimSuffix(bytes.TrimSuffix(seq[2:], []byte{0x07}), []byte{0x1b, '\\'})
	num, data, ok := bytes.Cut(body, []byte{';'})
	if !ok {
		return
	}
	switch cmd, err := strconv.Atoi(string(num)); {
	case err != nil:
		return
	// 0 sets icon name and title, 1 the icon name, 2 the title. The emulator
	// treats all three as the title, and so do we.
	case cmd <= 2:
		if t.opts.OnTitle != nil {
			t.opts.OnTitle(string(data))
		}
	}
}
