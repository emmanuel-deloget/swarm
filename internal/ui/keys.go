package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// keyBytes turns a bubbletea key event back into the bytes a terminal would
// have sent, so the attached pane can forward them to the agent's pty.
//
// It cannot be perfect — bubbletea has already parsed the input — but it covers
// what you actually type at an agent: text, control keys, arrows, escape.
func keyBytes(msg tea.KeyMsg) []byte {
	switch msg.Type {
	case tea.KeyRunes:
		s := string(msg.Runes)
		if msg.Alt {
			return []byte("\x1b" + s)
		}
		return []byte(s)
	case tea.KeySpace:
		if msg.Alt {
			return []byte("\x1b ")
		}
		return []byte(" ")
	case tea.KeyEnter:
		if msg.Alt {
			return []byte("\x1b\r")
		}
		return []byte("\r")
	case tea.KeyTab:
		return []byte("\t")
	case tea.KeyShiftTab:
		return []byte("\x1b[Z")
	case tea.KeyBackspace:
		return []byte("\x7f")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyInsert:
		return []byte("\x1b[2~")
	case tea.KeyEsc:
		return []byte("\x1b")
	case tea.KeyUp:
		return csiModified('A', altBit(msg))
	case tea.KeyDown:
		return csiModified('B', altBit(msg))
	case tea.KeyRight:
		return csiModified('C', altBit(msg))
	case tea.KeyLeft:
		return csiModified('D', altBit(msg))
	case tea.KeyHome:
		return csiModified('H', altBit(msg))
	case tea.KeyEnd:
		return csiModified('F', altBit(msg))
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyF1:
		return []byte("\x1bOP")
	case tea.KeyF2:
		return []byte("\x1bOQ")
	case tea.KeyF3:
		return []byte("\x1bOR")
	case tea.KeyF4:
		return []byte("\x1bOS")
	case tea.KeyF5:
		return []byte("\x1b[15~")
	case tea.KeyF6:
		return []byte("\x1b[17~")
	case tea.KeyF7:
		return []byte("\x1b[18~")
	case tea.KeyF8:
		return []byte("\x1b[19~")
	case tea.KeyF9:
		return []byte("\x1b[20~")
	case tea.KeyF10:
		return []byte("\x1b[21~")
	case tea.KeyF11:
		return []byte("\x1b[23~")
	case tea.KeyF12:
		return []byte("\x1b[24~")
	}

	// Arrows and friends held with a modifier. bubbletea names each combination
	// as its own key, and without these the attached mode dropped them: a
	// ctrl+left that moves a word in the agent's editor did nothing at all.
	if k, ok := modified[msg.Type]; ok {
		return csiModified(k.final, k.mods|altBit(msg))
	}

	if k, ok := pagedModified[msg.Type]; ok {
		return fmt.Appendf(nil, "\x1b[%d;%d~", k.num, (k.mods|altBit(msg))+1)
	}

	// Control combinations: bubbletea reports them as named keys.
	if b, ok := ctrlBytes[msg.Type]; ok {
		return []byte{b}
	}
	return nil
}

// The modifier bits xterm encodes in a CSI parameter. The parameter sent is
// their sum plus one, so a plain key is 1 and ctrl alone is 5.
const (
	modShift = 1
	modAlt   = 2
	modCtrl  = 4
)

func altBit(msg tea.KeyMsg) int {
	if msg.Alt {
		return modAlt
	}
	return 0
}

// modified is every arrow-like key bubbletea names with a modifier, mapped to
// the final byte of its escape sequence and the modifiers held.
//
// pgup and pgdn are not here: they take the CSI 5~ / CSI 6~ form, whose
// modifier goes in a second parameter rather than after a leading 1.
var modified = map[tea.KeyType]struct {
	final byte
	mods  int
}{
	tea.KeyShiftUp:        {'A', modShift},
	tea.KeyShiftDown:      {'B', modShift},
	tea.KeyShiftRight:     {'C', modShift},
	tea.KeyShiftLeft:      {'D', modShift},
	tea.KeyShiftHome:      {'H', modShift},
	tea.KeyShiftEnd:       {'F', modShift},
	tea.KeyCtrlUp:         {'A', modCtrl},
	tea.KeyCtrlDown:       {'B', modCtrl},
	tea.KeyCtrlRight:      {'C', modCtrl},
	tea.KeyCtrlLeft:       {'D', modCtrl},
	tea.KeyCtrlHome:       {'H', modCtrl},
	tea.KeyCtrlEnd:        {'F', modCtrl},
	tea.KeyCtrlShiftUp:    {'A', modCtrl | modShift},
	tea.KeyCtrlShiftDown:  {'B', modCtrl | modShift},
	tea.KeyCtrlShiftRight: {'C', modCtrl | modShift},
	tea.KeyCtrlShiftLeft:  {'D', modCtrl | modShift},
	tea.KeyCtrlShiftHome:  {'H', modCtrl | modShift},
	tea.KeyCtrlShiftEnd:   {'F', modCtrl | modShift},
}

// pagedModified is the same for the keys that end in a tilde.
var pagedModified = map[tea.KeyType]struct {
	num  int
	mods int
}{
	tea.KeyCtrlPgUp:   {5, modCtrl},
	tea.KeyCtrlPgDown: {6, modCtrl},
}

// csiModified renders CSI 1 ; <mods+1> <final>, which is how xterm and every
// terminal that follows it reports a held modifier.
func csiModified(final byte, mods int) []byte {
	if mods == 0 {
		return []byte{0x1b, '[', final}
	}
	return fmt.Appendf(nil, "\x1b[1;%d%c", mods+1, final)
}

// ctrlBytes covers every control key bubbletea names, including the ones that
// double as something else (ctrl+i is tab, ctrl+m is enter): an attached pane
// has to be able to forward all of them, since any of them may be the key the
// agent expects — or the one the detach key was moved away from.
var ctrlBytes = map[tea.KeyType]byte{
	tea.KeyCtrlAt: 0x00, tea.KeyCtrlA: 0x01, tea.KeyCtrlB: 0x02, tea.KeyCtrlC: 0x03,
	tea.KeyCtrlD: 0x04, tea.KeyCtrlE: 0x05, tea.KeyCtrlF: 0x06, tea.KeyCtrlG: 0x07,
	tea.KeyCtrlH: 0x08, tea.KeyCtrlI: 0x09, tea.KeyCtrlJ: 0x0a, tea.KeyCtrlK: 0x0b,
	tea.KeyCtrlL: 0x0c, tea.KeyCtrlM: 0x0d, tea.KeyCtrlN: 0x0e, tea.KeyCtrlO: 0x0f,
	tea.KeyCtrlP: 0x10, tea.KeyCtrlQ: 0x11, tea.KeyCtrlR: 0x12, tea.KeyCtrlS: 0x13,
	tea.KeyCtrlT: 0x14, tea.KeyCtrlU: 0x15, tea.KeyCtrlV: 0x16, tea.KeyCtrlW: 0x17,
	tea.KeyCtrlX: 0x18, tea.KeyCtrlY: 0x19, tea.KeyCtrlZ: 0x1a,
	tea.KeyCtrlOpenBracket: 0x1b, tea.KeyCtrlBackslash: 0x1c,
	tea.KeyCtrlCloseBracket: 0x1d, tea.KeyCtrlCaret: 0x1e, tea.KeyCtrlUnderscore: 0x1f,
	tea.KeyCtrlQuestionMark: 0x7f,
}
