package ui

import (
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func TestMixHex(t *testing.T) {
	cases := []struct {
		a, b string
		t    float64
		want string
	}{
		{"#d2a8ff", "#000000", 0, "#d2a8ff"},
		{"#d2a8ff", "#000000", 1, "#000000"},
		{"#000000", "#ffffff", 0.5, "#808080"},
		{"#7a3ba8", "#ffffff", 1, "#ffffff"},
	}
	for _, c := range cases {
		if got := mixHex(c.a, c.b, c.t); got != c.want {
			t.Errorf("mixHex(%s, %s, %v) = %s, want %s", c.a, c.b, c.t, got, c.want)
		}
	}
	// Junk must not panic; a black envelope is better than a crashed UI.
	if got := mixHex("nope", "#ffffff", 0.5); got == "" {
		t.Error("mixHex should return something for a malformed colour")
	}
}

// TestMsgFadeStyleSpansTheWindow: the envelope has to be drawn for the whole
// fade and not a moment longer, or it either flickers or never leaves.
func TestMsgFadeStyleSpansTheWindow(t *testing.T) {
	if _, ok := msgFadeStyle(-time.Millisecond); ok {
		t.Error("a negative age should not draw")
	}
	if _, ok := msgFadeStyle(0); !ok {
		t.Error("the fade should start at zero")
	}
	if _, ok := msgFadeStyle(msgFadeFor - time.Millisecond); !ok {
		t.Error("the fade should still draw just before it ends")
	}
	if _, ok := msgFadeStyle(msgFadeFor); ok {
		t.Error("the fade should stop at its duration")
	}
	if _, ok := msgFadeStyle(time.Hour); ok {
		t.Error("a stale stamp should not draw")
	}
}

// TestMsgFadeDarkens walks the window and checks the colour only ever moves
// toward the background — a fade that brightens anywhere would read as a blink.
func TestMsgFadeDarkens(t *testing.T) {
	var last int
	for i := range msgFadeSteps {
		age := time.Duration(float64(msgFadeFor) * float64(i) / float64(msgFadeSteps))
		style, ok := msgFadeStyle(age)
		if !ok {
			t.Fatalf("step %d does not draw", i)
		}
		c, isAdaptive := style.GetForeground().(lipgloss.AdaptiveColor)
		if !isAdaptive {
			t.Fatalf("step %d is not adaptive, so it would ignore a light terminal", i)
		}
		r, g, b := parseHex(c.Dark)
		sum := r + g + b
		if i > 0 && sum > last {
			t.Errorf("step %d brightened: %s", i, c.Dark)
		}
		last = sum
		// And the light variant must move the other way, toward white.
		lr, lg, lb := parseHex(c.Light)
		if i == msgFadeSteps-1 && (lr+lg+lb) < 700 {
			t.Errorf("the light variant ends at %s, which is not near the background", c.Light)
		}
	}
}

// TestMsgFadeEndpoints: it starts at the message colour exactly, so a delivery
// looks the same whether you catch it at once or a frame later.
func TestMsgFadeEndpoints(t *testing.T) {
	first, _ := msgFadeStyle(0)
	c, _ := first.GetForeground().(lipgloss.AdaptiveColor)
	if c.Dark != colMsg.Dark || c.Light != colMsg.Light {
		t.Errorf("the fade starts at %+v, want %+v", c, colMsg)
	}
}
