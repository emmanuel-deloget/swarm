package web

import (
	"io"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// The terminal font is bundled because a machine without a suitable monospace
// font of its own draws a terminal wrong — box-drawing characters borrowed
// from a proportional font, frames wider than the text they enclose. The catch
// is that this is invisible on any machine that does have one, which is every
// machine the code is written on: the page falls back, silently, and looks
// perfect. So the checks here are for the ways the bundle can quietly stop
// working, none of which would show up in front of a developer.

var cssURL = regexp.MustCompile(`url\("([^"]+)"\)`)

// fontsInStylesheet returns the paths every url() in the stylesheet asks for,
// which for this page is exactly the font faces.
func fontsInStylesheet(t *testing.T, css string) []string {
	t.Helper()
	var out []string
	for _, m := range cssURL.FindAllStringSubmatch(css, -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatal("the stylesheet asks for no font at all; the bundled font is how a " +
			"terminal renders correctly on a machine that has no monospace font of its own")
	}
	return out
}

func stylesheet(t *testing.T, ts string) string {
	t.Helper()
	res, err := http.Get(ts + "/style.css?t=secret")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// TestEveryFontTheStylesheetAsksForIsServed: a face named in @font-face but not
// routed is a 404 the browser answers by falling back to a system font. On a
// developer's machine that fallback is a perfectly good monospace font and
// nothing looks wrong; on a phone it is whatever has the glyph, and the frames
// come apart.
func TestEveryFontTheStylesheetAsksForIsServed(t *testing.T) {
	h := newTestHub(t)
	ts := newTestServer(t, h, Options{})
	css := stylesheet(t, ts.URL)

	for _, ref := range fontsInStylesheet(t, css) {
		// The stylesheet is served from /, so its url() is the request path.
		res, err := http.Get(ts.URL + "/" + ref + "?t=secret")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusOK {
			t.Errorf("the stylesheet asks for %s and the server answers %d", ref, res.StatusCode)
			continue
		}
		if got := res.Header.Get("Content-Type"); got != "font/woff2" {
			t.Errorf("%s is served as %q, not font/woff2", ref, got)
		}
		if len(body) == 0 {
			t.Errorf("%s is served empty", ref)
		}
		// And it is the file in the binary, not something else with that name.
		want, err := assets.ReadFile("assets/" + ref)
		if err != nil {
			t.Errorf("%s is served but not embedded: %v", ref, err)
			continue
		}
		if len(want) != len(body) {
			t.Errorf("%s: served %d bytes, embedded %d", ref, len(body), len(want))
		}
	}

	// A name that was never routed must not be reachable: the routes are built
	// from a list precisely so that the path cannot name a file.
	res, err := http.Get(ts.URL + "/fonts/NotAFace.woff2?t=secret")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode == http.StatusOK {
		t.Error("/fonts/ serves a face that was never declared, so the path names the file")
	}
}

// TestTheBundledFontIsTheOneTheTerminalUses: shipping the font, serving it and
// then not asking for it first is the failure that leaves no trace. Every
// machine with a monospace font of its own renders the page beautifully, and
// the one that needed the bundle is the one that does not get it.
func TestTheBundledFontIsTheOneTheTerminalUses(t *testing.T) {
	h := newTestHub(t)
	ts := newTestServer(t, h, Options{})
	css := stylesheet(t, ts.URL)

	// The family the @font-face rules define...
	const face = "@font-face"
	i := strings.Index(css, face)
	if i < 0 {
		t.Fatal("no @font-face: the font is not bundled at all")
	}
	family := betweenQuotes(css[i:], "font-family:")
	if family == "" {
		t.Fatal("an @font-face rule with no font-family")
	}

	// ...has to be the first one --mono asks for. Anything else and the browser
	// uses a font the machine already had, which is the situation the bundle
	// exists to end.
	mono := afterKey(css, "--mono:")
	if mono == "" {
		t.Fatal("no --mono in the stylesheet")
	}
	first := strings.TrimSpace(strings.Split(mono, ",")[0])
	if strings.Trim(first, `"`) != family {
		t.Errorf("--mono asks for %s first, but the bundled face is %q; the bundle "+
			"would only be reached on a machine that has nothing else", first, family)
	}

	// A terminal is a grid of cells, so no two characters may be drawn as one
	// glyph. JuliaMono ships calt and liga enabled: left alone, "!=" renders as
	// a single glyph and the screen shows something the agent never wrote.
	for _, sel := range []string{".screen {", ".cell > .cs {"} {
		block := cssBlock(css, sel)
		if block == "" {
			t.Errorf("no %s block in the stylesheet", sel)
			continue
		}
		if !strings.Contains(block, "font-variant-ligatures: none") {
			t.Errorf("%s draws a terminal but does not turn ligatures off", sel)
		}
	}

	// The licence travels with the files. Both fonts carried so far have asked
	// for that in writing, and a font added without one is a licence breach
	// that no other test would notice.
	entries, err := fs.ReadDir(assets, "assets/fonts")
	if err != nil {
		t.Fatalf("no bundled fonts directory: %v", err)
	}
	var faces, licences int
	for _, e := range entries {
		switch {
		case strings.HasSuffix(e.Name(), ".woff2"):
			faces++
		case strings.Contains(strings.ToUpper(e.Name()), "LICENSE"):
			licences++
		}
	}
	if faces > 0 && licences == 0 {
		t.Errorf("%d font files are shipped with no licence beside them", faces)
	}
}

// cssBlock returns the body of the first block opened by sel, which is safe
// here because the selectors it is asked for are flat: no nesting, so the first
// closing brace is theirs.
func cssBlock(css, sel string) string {
	i := strings.Index(css, sel)
	if i < 0 {
		return ""
	}
	rest := css[i+len(sel):]
	j := strings.Index(rest, "}")
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// afterKey returns what a declaration is set to, up to its semicolon.
func afterKey(css, key string) string {
	i := strings.Index(css, key)
	if i < 0 {
		return ""
	}
	rest := css[i+len(key):]
	j := strings.Index(rest, ";")
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:j])
}

// betweenQuotes returns the quoted value of the first declaration named key.
func betweenQuotes(css, key string) string {
	v := afterKey(css, key)
	if !strings.HasPrefix(v, `"`) {
		return ""
	}
	if j := strings.Index(v[1:], `"`); j >= 0 {
		return v[1 : 1+j]
	}
	return ""
}
