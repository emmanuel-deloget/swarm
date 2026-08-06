package vterm

import (
	"strings"
	"testing"
	"time"
)

func TestHTMLKeepsColours(t *testing.T) {
	term, err := Start(Options{
		Command: []string{"sh", "-c", `printf '\033[31mRED\033[0m plain \033[1;38;5;208mORANGE\033[0m\n'; sleep 5`},
		Cols:    40,
		Rows:    5,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	waitFor(t, "coloured output", func() bool { return strings.Contains(term.Text(), "ORANGE") })

	// The HTML rendering turns styling into inline CSS, which is what the web
	// UI displays; it must never leak an escape sequence.
	lines := term.HTMLLines()
	if len(lines) != 5 {
		t.Fatalf("HTMLLines returned %d lines, want one per row", len(lines))
	}
	html := strings.Join(lines, "")
	if strings.Contains(html, "\x1b") {
		t.Error("the HTML rendering leaked an escape sequence")
	}
	if !strings.Contains(html, "color:") {
		t.Errorf("the HTML rendering lost the colours: %q", lines[0])
	}
	if !strings.Contains(html, "RED") || !strings.Contains(html, "ORANGE") {
		t.Errorf("the HTML rendering lost the content: %q", lines[0])
	}
}

func TestHTMLEscapesMarkup(t *testing.T) {
	term, err := Start(Options{
		Command: []string{"sh", "-c", `printf '<script>alert("x")</script> & done\n'; sleep 5`},
		Cols:    60,
		Rows:    3,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = term.Stop(time.Second) }()

	waitFor(t, "output", func() bool { return strings.Contains(term.Text(), "done") })

	html := strings.Join(term.HTMLLines(), "")
	for _, bad := range []string{"<script>", "</script>", `alert("x")`} {
		if strings.Contains(html, bad) {
			t.Errorf("the HTML rendering did not escape %q", bad)
		}
	}
	for _, want := range []string{"&lt;script&gt;", "&amp;", "&quot;"} {
		if !strings.Contains(html, want) {
			t.Errorf("the HTML rendering should contain %q", want)
		}
	}
}
