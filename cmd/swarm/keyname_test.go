package main

import "testing"

// The bytes below were measured with `swarm keys -read` on a Windows 10
// console. What matters is not that they are Windows bytes — a Linux terminal
// sends the same ones — but that this report answered "no key name" for half
// of them, including ^G, the key that detaches there.
func TestEveryMeasuredKeyIsNamed(t *testing.T) {
	for _, c := range []struct{ bytes, want string }{
		{"\x1b[A", "up"}, {"\x1b[C", "right"}, {"\x1b[H", "home"},
		{"\x1b[5~", "pageup"}, {"\x1b[Z", "backtab"}, // shift+tab, under the name swarm lists first, {"\t", "tab"},
		{"a", "a"}, {"A", "A"}, {"/", "/"},
		{"\x01", "ctrl+a"}, {"\x07", "ctrl+g"}, {"\x04", "ctrl+d"},
		{"\x1ba", "alt+a"},
		// ctrl+\ and ctrl+] on a Windows console: the character itself, which
		// is why neither can detach there.
		{"\\", "\\"}, {"]", "]"},
	} {
		if got := nameForKeyBytes([]byte(c.bytes)); got != c.want {
			t.Errorf("%q is reported as %q, want %q", c.bytes, got, c.want)
		}
	}

	// The modified navigation keys a console really does send. Naming them was
	// the point of measuring: swarm could receive them and had no name for
	// them, so they could be neither sent nor bound.
	for seq, want := range map[string]string{
		"\x1b[1;5A": "ctrl+up", "\x1b[1;2D": "shift+left",
		"\x1b[1;3B": "alt+down", "\x1b[5;5~": "ctrl+pgup",
		"\x1b[1;6C": "ctrl+shift+right",
	} {
		if got := nameForKeyBytes([]byte(seq)); got != want {
			t.Errorf("%q is reported as %q, want %q", seq, got, want)
		}
	}

	// And something no key sends still says so.
	if got := nameForKeyBytes([]byte("\x1b[99;99R")); got != "(no key name sends these bytes)" {
		t.Errorf("an unknown sequence is reported as %q", got)
	}
}
