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

	// The modified arrows a console really does send, and which swarm has no
	// name for: it cannot bind them or send them, only receive them. Reporting
	// the bytes is then the whole of what this command can honestly say.
	for _, seq := range []string{"\x1b[1;5A", "\x1b[1;2D", "\x1b[1;3B", "\x1b[5;5~"} {
		if got := nameForKeyBytes([]byte(seq)); got != "(no key name sends these bytes)" {
			t.Errorf("%q is reported as %q; swarm has no name for it", seq, got)
		}
	}
}
