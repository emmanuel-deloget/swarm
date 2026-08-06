package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/emmanuel-deloget/swarm/internal/agent"
	"github.com/emmanuel-deloget/swarm/internal/hub"
)

// runCommand executes a line typed in the ":" bar. Every command takes a
// target, and an omitted target means the selected agent — that is what makes
// the shortcuts (i, s, K) feel direct.
func (m *model) runCommand(line string) tea.Cmd {
	if line == "" {
		return nil
	}
	verb, rest := cut(line)
	switch verb {
	case "q", "quit", "exit":
		return tea.Quit

	case "inject", "i":
		return m.injectCmd(rest, true)
	case "type", "t":
		return m.injectCmd(rest, false)

	case "keys", "key", "k":
		target, keys := m.splitTarget(rest)
		if keys == "" {
			return errCmd("usage: :keys <target> <key>...")
		}
		return m.async(func() (string, error) {
			res, err := m.h.Keys(target, keys)
			return summary(res, "keys sent"), err
		})

	case "send", "s":
		target, body := m.splitTarget(rest)
		if body == "" {
			return errCmd("usage: :send <target> <message>")
		}
		return m.async(func() (string, error) {
			msgs, err := m.h.Send("user", target, body, nil)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("message sent to %d agent(s)", len(msgs)), nil
		})

	case "broadcast", "b", "all":
		if rest == "" {
			return errCmd("usage: :broadcast <message>")
		}
		return m.async(func() (string, error) {
			msgs, err := m.h.Send("user", "all", rest, nil)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("broadcast to %d agent(s)", len(msgs)), nil
		})

	case "file", "attach-file":
		target, path := m.splitTarget(rest)
		if path == "" {
			return errCmd("usage: :file <target> <path>")
		}
		return m.fileCmd(target, path)

	case "start":
		target := m.orCurrent(rest)
		return m.async(func() (string, error) {
			res, err := m.h.Start(target)
			return summary(res, "started"), err
		})
	case "stop":
		target := m.orCurrent(rest)
		return m.async(func() (string, error) {
			res, err := m.h.Stop(target, 5*time.Second)
			return summary(res, "stopped"), err
		})
	case "restart":
		target := m.orCurrent(rest)
		return m.async(func() (string, error) {
			res, err := m.h.Restart(target, 5*time.Second)
			return summary(res, "restarted"), err
		})

	case "resize":
		target, size := m.splitTarget(rest)
		var cols, rows int
		if _, err := fmt.Sscanf(strings.ReplaceAll(size, "x", " "), "%d %d", &cols, &rows); err != nil {
			return errCmd("usage: :resize <target> <cols>x<rows>")
		}
		return m.async(func() (string, error) {
			agents, err := m.h.Resolve(target)
			if err != nil {
				return "", err
			}
			for _, a := range agents {
				if err := a.Resize(cols, rows); err != nil {
					return "", err
				}
			}
			return fmt.Sprintf("resized to %dx%d", cols, rows), nil
		})

	case "web":
		url, tok := m.h.WebURL()
		if url == "" {
			return errCmd("the web server is disabled")
		}
		return okCmd(url + "?t=" + tok)

	case "shared":
		return okCmd(m.h.Config().Shared)

	case "help", "?":
		m.returnTo = modeNormal
		m.mode = modeHelp
		return nil
	}
	return errCmd(fmt.Sprintf("unknown command %q (try :help)", verb))
}

// lifecycle is the keyboard-shortcut path to start/stop/restart, bypassing the
// command bar.
func (m *model) lifecycle(verb, target string) tea.Cmd {
	if target == "" {
		return errCmd("no agent selected")
	}
	return m.runCommand(verb + " " + target)
}

func (m *model) injectCmd(rest string, submit bool) tea.Cmd {
	target, text := m.splitTarget(rest)
	if text == "" {
		return errCmd("usage: :inject <target> <text>")
	}
	return m.async(func() (string, error) {
		res, err := m.h.Inject(target, text, agent.InjectOptions{Submit: submit})
		return summary(res, "injected"), err
	})
}

func (m *model) fileCmd(target, path string) tea.Cmd {
	return m.async(func() (string, error) {
		expanded := path
		if strings.HasPrefix(expanded, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				expanded = filepath.Join(home, expanded[2:])
			}
		}
		staged, err := m.h.CopyToShared(expanded)
		if err != nil {
			return "", err
		}
		res, err := m.h.Inject(target, staged, agent.InjectOptions{Submit: true})
		if err != nil {
			return "", err
		}
		return summary(res, "sent "+filepath.Base(staged)), nil
	})
}

// splitTarget takes the first word as a target when it names something known,
// and otherwise treats the whole string as the payload for the selected agent.
func (m *model) splitTarget(rest string) (target, payload string) {
	first, tail := cut(rest)
	if first == "" {
		return m.currentName(), ""
	}
	if _, err := m.h.Config().Resolve(first); err == nil {
		return first, tail
	}
	return m.currentName(), rest
}

func (m *model) orCurrent(s string) string {
	if strings.TrimSpace(s) == "" {
		return m.currentName()
	}
	return strings.TrimSpace(s)
}

// async runs a hub operation off the UI goroutine and reports the outcome in
// the status line.
func (m *model) async(fn func() (string, error)) tea.Cmd {
	return func() tea.Msg {
		text, err := fn()
		if err != nil {
			return resultMsg{err.Error(), true}
		}
		return resultMsg{text, false}
	}
}

func okCmd(text string) tea.Cmd {
	return func() tea.Msg { return resultMsg{text, false} }
}

func errCmd(text string) tea.Cmd {
	return func() tea.Msg { return resultMsg{text, true} }
}

func summary(res []hub.TargetResult, verb string) string {
	ok, failed := 0, 0
	firstErr := ""
	for _, r := range res {
		if r.OK {
			ok++
		} else {
			failed++
			if firstErr == "" {
				firstErr = r.Error
			}
		}
	}
	if failed == 0 {
		if ok == 1 && len(res) == 1 {
			return res[0].Agent + ": " + verb
		}
		return fmt.Sprintf("%s on %d agents", verb, ok)
	}
	return fmt.Sprintf("%s on %d agents, %d failed: %s", verb, ok, failed, firstErr)
}

func cut(s string) (head, tail string) {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i], strings.TrimSpace(s[i+1:])
	}
	return s, ""
}
