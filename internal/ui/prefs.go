package ui

import (
	"os"
	"path/filepath"
	"strings"
)

// Eighteen letters are shortcuts in the normal mode, three of them acting on an
// agent's life. Typing "merci pour la relecture" at a fleet cycles the mosaic,
// restarts an agent and opens an inject line — when all that was meant was to
// talk to the agent on screen.
//
// The dialogue lock turns that around: while it is on, a printable key opens
// the inject line carrying that key, and the shortcuts move behind esc. It is
// remembered between runs, because a preference you have to set every morning
// is one you stop setting.

const prefsFile = "ui"

// prefs is what the TUI remembers about how you like to drive it.
type prefs struct {
	path string
	// dialogue routes printable keys to the agent instead of the shortcuts.
	// On by default: you are here to talk to the agent on screen, and the
	// shortcuts are the exception, not the other way round.
	dialogue bool
}

// loadPrefs reads the file. A missing or unreadable one is the default, since
// failing to remember a preference is no reason to refuse to start — and the
// default is on, so a first run behaves the way it is meant to without anyone
// having to find the switch first.
func loadPrefs(stateDir string) *prefs {
	p := &prefs{path: filepath.Join(stateDir, prefsFile), dialogue: true}
	body, err := os.ReadFile(p.path)
	if err != nil {
		return p
	}
	for _, line := range strings.Split(string(body), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		if key == "dialogue" {
			p.dialogue = value == "true"
		}
	}
	return p
}

// save writes the file, 0600 like everything else swarm keeps about a session.
func (p *prefs) save() {
	if p.path == "" {
		return
	}
	body := "dialogue=" + boolText(p.dialogue) + "\n"
	_ = os.WriteFile(p.path, []byte(body), 0o600)
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
