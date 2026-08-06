package vterm

import (
	"fmt"
	"strings"
	"unicode"
)

// Bracketed paste delimiters. Wrapping a payload in these tells a well-behaved
// TUI "this is pasted text", so a multi-line prompt lands as one message
// instead of being submitted line by line.
const (
	PasteStart = "\x1b[200~"
	PasteEnd   = "\x1b[201~"
)

// Submit is the byte an interactive prompt reads as "go".
const Submit = "\r"

// SanitizeText strips control characters that would be interpreted as terminal
// input rather than content. Newlines and tabs survive; ESC, ^C, ^D and
// friends do not, because injected text often comes from another agent and
// must not be able to drive the receiver's terminal.
func SanitizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\n', '\t':
			b.WriteRune(r)
		case '\r':
			b.WriteRune('\n')
		default:
			if r == 0x7f || (unicode.IsControl(r) && r != '\n' && r != '\t') {
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// PasteSequence renders text as a bracketed paste payload.
func PasteSequence(text string) string {
	return PasteStart + text + PasteEnd
}

// keyAliases maps a human key name to the bytes a terminal would send.
var keyAliases = map[string]string{
	"enter":     "\r",
	"return":    "\r",
	"cr":        "\r",
	"newline":   "\n",
	"lf":        "\n",
	"tab":       "\t",
	"shift+tab": "\x1b[Z",
	"backtab":   "\x1b[Z",
	"esc":       "\x1b",
	"escape":    "\x1b",
	"space":     " ",
	"backspace": "\x7f",
	"bs":        "\x7f",
	"delete":    "\x1b[3~",
	"del":       "\x1b[3~",
	"insert":    "\x1b[2~",
	"up":        "\x1b[A",
	"down":      "\x1b[B",
	"right":     "\x1b[C",
	"left":      "\x1b[D",
	"home":      "\x1b[H",
	"end":       "\x1b[F",
	"pageup":    "\x1b[5~",
	"pgup":      "\x1b[5~",
	"pagedown":  "\x1b[6~",
	"pgdn":      "\x1b[6~",
	"f1":        "\x1bOP",
	"f2":        "\x1bOQ",
	"f3":        "\x1bOR",
	"f4":        "\x1bOS",
	"f5":        "\x1b[15~",
	"f6":        "\x1b[17~",
	"f7":        "\x1b[18~",
	"f8":        "\x1b[19~",
	"f9":        "\x1b[20~",
	"f10":       "\x1b[21~",
	"f11":       "\x1b[23~",
	"f12":       "\x1b[24~",
	// Agent CLIs commonly bind these two to "insert a newline without
	// submitting" and "submit", which is exactly what one needs when driving
	// them from the outside.
	"alt+enter":   "\x1b\r",
	"shift+enter": "\x1b\r",
	"ctrl+enter":  "\n",
}

// KeySequence translates a key name into the bytes to write to a terminal.
// It understands the aliases above, "ctrl+<char>", "alt+<char>", "^<char>",
// and literal single characters.
func KeySequence(name string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return "", fmt.Errorf("empty key name")
	}
	if seq, ok := keyAliases[key]; ok {
		return seq, nil
	}
	if strings.HasPrefix(key, "^") && len(key) == 2 {
		return ctrl(rune(key[1]))
	}
	if rest, ok := strings.CutPrefix(key, "ctrl+"); ok {
		if seq, ok := keyAliases[rest]; ok && len(seq) == 1 {
			return seq, nil
		}
		if len([]rune(rest)) != 1 {
			return "", fmt.Errorf("unsupported key %q", name)
		}
		return ctrl([]rune(rest)[0])
	}
	if rest, ok := strings.CutPrefix(key, "alt+"); ok {
		if seq, ok := keyAliases[rest]; ok {
			return "\x1b" + seq, nil
		}
		if len([]rune(rest)) != 1 {
			return "", fmt.Errorf("unsupported key %q", name)
		}
		return "\x1b" + rest, nil
	}
	if r := []rune(name); len(r) == 1 {
		return string(r), nil
	}
	return "", fmt.Errorf("unknown key %q", name)
}

func ctrl(r rune) (string, error) {
	switch {
	case r >= 'a' && r <= 'z':
		return string(r - 'a' + 1), nil
	case r >= 'A' && r <= 'Z':
		return string(r - 'A' + 1), nil
	case r == '[':
		return "\x1b", nil
	case r == '\\':
		return "\x1c", nil
	case r == ']':
		return "\x1d", nil
	case r == '^':
		return "\x1e", nil
	case r == '_':
		return "\x1f", nil
	case r == '?':
		return "\x7f", nil
	case r == '@', r == ' ':
		return "\x00", nil
	}
	return "", fmt.Errorf("no control code for %q", string(r))
}

// KeySequences translates a whitespace-separated list of key names.
func KeySequences(names string) (string, error) {
	var b strings.Builder
	for _, n := range strings.Fields(names) {
		seq, err := KeySequence(n)
		if err != nil {
			return "", err
		}
		b.WriteString(seq)
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("no keys in %q", names)
	}
	return b.String(), nil
}
