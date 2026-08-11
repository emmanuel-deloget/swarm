package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/emmanuel-deloget/swarm/internal/config"
	"github.com/emmanuel-deloget/swarm/internal/ipc"
	"github.com/emmanuel-deloget/swarm/internal/vterm"
)

// statusBar keeps a reminder of how to leave on the last row of the window.
//
// The agent is given one row less than the window and a scrolling region keeps
// its output above the line, so the only thing that can wipe the bar is a
// full-screen clear — which is why it is redrawn after every chunk the agent
// sends.
type statusBar struct {
	mu      sync.Mutex
	enabled bool
	agent   string
	// detach is the key name shown in the bar, so the reminder matches whatever
	// key actually detaches.
	detach string
	w, h   int
}

func (s *statusBar) size() (w, h int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w, s.h
}

func (s *statusBar) resize(w, h int) {
	s.mu.Lock()
	s.w, s.h = w, h
	s.mu.Unlock()
}

// rows is the height the agent gets: the whole window minus the bar.
func (s *statusBar) rows() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled || s.h < 3 {
		return s.h
	}
	return s.h - 1
}

// crlf gives a rendered screen the line endings a raw terminal needs.
//
// A rendering separates rows with \n, and everywhere else that is enough: a
// terminal in its usual mode turns each one into a carriage return and a line
// feed. An attach does not run in that mode — MakeRaw exists to stop the
// terminal touching what passes through, and it stops this too — so a bare \n
// drops a row without returning to column one, and every row starts where the
// one above it ended.
//
// It shows on Unix as a staircase in the first repaint, gone as soon as the
// agent redraws with absolute positions. On a Windows console it stays, which
// is how it was finally noticed.
//
// What arrives as raw agent output needs none of this: it has already been
// through that agent's own terminal, which added the carriage returns.
func crlf(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

// detachLabel is what the reminder says, wherever it is shown: on the last row
// for the whole attach, or printed once where that cannot be held.
func detachLabel(agent, key string) string {
	return fmt.Sprintf(" %s — %s detach ", agent, key)
}

func (s *statusBar) draw() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled || s.h < 3 || s.w < 20 {
		return
	}

	label := detachLabel(s.agent, s.detach)
	// Never write in the very last column: on the bottom row that would wrap
	// and scroll the whole window.
	width := s.w - 1
	if ansi.StringWidth(label) > width {
		label = ansi.Truncate(label, width, "")
	}

	var b strings.Builder
	b.WriteString("\x1b7")                    // save cursor and attributes
	fmt.Fprintf(&b, "\x1b[1;%dr", s.h-1)      // keep the agent above the bar
	fmt.Fprintf(&b, "\x1b[%d;1H\x1b[2K", s.h) // go to the last row, clear it
	b.WriteString("\x1b[0;7m")                // reverse video
	b.WriteString(label)
	b.WriteString(strings.Repeat(" ", width-ansi.StringWidth(label)))
	b.WriteString("\x1b[0m\x1b8") // reset, restore cursor
	_, _ = os.Stdout.WriteString(b.String())
}

// leaveAgentModes puts back everything an agent may have switched on in this
// terminal: the alternate screen, the cursor, the scrolling region, attributes,
// then mouse reporting in each of its flavours (1000 clicks, 1002 cell motion,
// 1003 any motion, 1005/1006/1015 the encodings), bracketed paste and focus
// events. Resetting a mode that was never set is harmless, which is why the
// list is exhaustive rather than clever.
const leaveAgentModes = "\x1b[?1049l\x1b[?25h\x1b[r\x1b[0m" +
	"\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1005l\x1b[?1006l\x1b[?1015l" +
	"\x1b[?2004l\x1b[?1004l\r\n"

// clear removes the bar and gives the whole window back.
func (s *statusBar) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled || s.h < 3 {
		return
	}
	fmt.Printf("\x1b[r\x1b[%d;1H\x1b[2K", s.h)
}

// cmdAttach hands this terminal over to one agent: everything you type goes to
// it, everything it prints comes here. It is the escape hatch for when the
// mosaic view is too small and you want the agent full screen.
func cmdAttach(args []string) error {
	var cf clientFlags
	fs := newFlagSet("attach")
	cf.register(fs)
	keepSize := fs.Bool("keep-size", false, "do not resize the agent to this window")
	noStatus := fs.Bool("no-status", false, "do not reserve the last row for the detach reminder")
	detachKey := fs.String("detach-key", "", "key that detaches, overriding the configured one (e.g. ctrl+g)")
	_ = parseArgs(fs, args, -1)

	name := fs.Arg(0)
	if name == "" {
		return fmt.Errorf("usage: swarm attach <agent>    (detach with the configured key, %s by default)", config.DefaultDetachKey)
	}

	stdin := os.Stdin.Fd()
	if !term.IsTerminal(stdin) {
		return errors.New("attach needs a terminal on stdin")
	}

	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	// Ask the swarm which key detaches, unless told on the command line. The
	// default collides with tmux, screen and asciinema, so it has to be movable.
	keyName := *detachKey
	if keyName == "" {
		if info, err := c.Do(ipc.Request{Cmd: ipc.CmdInfo}); err == nil && info.DetachKey != "" {
			keyName = info.DetachKey
		} else {
			keyName = config.DefaultDetachKey
		}
	}
	if err := vterm.CheckBindable(keyName); err != nil {
		return fmt.Errorf("detach key: %w (see `swarm keys -list`)", err)
	}
	detachSeq, err := vterm.KeySequences(keyName)
	if err != nil {
		return fmt.Errorf("detach key %q: %w", keyName, err)
	}

	bar := &statusBar{enabled: !*noStatus && statusBarSupported, agent: name, detach: keyName}
	if w, h, err := term.GetSize(os.Stdout.Fd()); err == nil {
		bar.resize(w, h)
	}

	req := ipc.Request{Cmd: ipc.CmdAttach, Target: name}
	if !*keepSize {
		if w, _ := bar.size(); w > 0 {
			req.Cols, req.Rows = w, bar.rows()
		}
	}
	if err := c.Send(req); err != nil {
		return err
	}

	oldState, err := term.MakeRaw(stdin)
	if err != nil {
		return err
	}
	restoreVT := enableVTOutput()
	var restoreOnce sync.Once
	restore := func() {
		restoreOnce.Do(func() {
			bar.clear()
			_ = term.Restore(stdin, oldState)
			// Leave the local terminal in a sane state whatever the agent did.
			// An attach hands the agent's raw output straight to this terminal,
			// so every mode it turned on is on here too — and it has no idea
			// the connection is ending, so nobody turns them off. Mouse
			// reporting is the one that hurts: the terminal stops selecting
			// text of its own accord, and no key in swarm can undo it, because
			// swarm did not do it.
			fmt.Print(leaveAgentModes)
			// And give the terminal back empty. What is on it is the agent's
			// screen, and the agent is still running and still drawing it
			// elsewhere; left there, the shell prompt lands in the middle of a
			// picture nothing will ever finish. The scrollback is untouched —
			// that is this terminal's history, not the agent's.
			fmt.Print("\x1b[2J\x1b[H")
			// Last, since the lines above are themselves escape sequences and
			// a console that has stopped interpreting them would print them.
			restoreVT()
		})
	}
	defer restore()

	fmt.Print("\x1b[2J\x1b[H")
	bar.draw()
	if !*noStatus && !statusBarSupported {
		// Into the window title, not onto the screen: the agent owns every row
		// and the first thing it sends is a repaint, so a line printed here is
		// wiped before anyone reads it. A title costs no row and survives it.
		// An agent that sets its own title takes it back, which is its right.
		fmt.Printf("\x1b]2;%s\x07", strings.TrimSpace(detachLabel(name, keyName)))
	}

	// Follow this window, so the agent redraws for the size it is shown at.
	// An agent still laid out for a size that no longer exists wraps every line
	// it draws, which is the difference between a readable screen and confetti.
	if !*keepSize {
		stopResize := make(chan struct{})
		defer close(stopResize)
		go watchResize(stopResize, func() {
			w, h, err := term.GetSize(os.Stdout.Fd())
			if err != nil {
				return
			}
			bar.resize(w, h)
			_ = c.Send(ipc.Request{Cols: w, Rows: bar.rows()})
			bar.draw()
		})
	}

	// Keystrokes in. The detach key never reaches the agent; everything else
	// does, byte for byte.
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if i := bytes.Index(buf[:n], []byte(detachSeq)); i >= 0 {
					if i > 0 {
						_ = c.Send(ipc.Request{Data: append([]byte(nil), buf[:i]...)})
					}
					return
				}
				if err := c.Send(ipc.Request{Data: append([]byte(nil), buf[:n]...)}); err != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Screen out.
	streamDone := make(chan error, 1)
	go func() {
		for {
			resp, err := c.Recv()
			if err != nil {
				streamDone <- err
				return
			}
			if !resp.OK && resp.Error != "" {
				streamDone <- errors.New(resp.Error)
				return
			}
			switch {
			case resp.Resync:
				// Clear, then repaint from the snapshot: the stream we were
				// following had a hole in it.
				fmt.Print("\x1b[2J\x1b[H")
				fmt.Print(crlf(resp.Text))
				bar.draw()
			case len(resp.Data) > 0:
				_, _ = os.Stdout.Write(resp.Data)
				// The agent may have cleared the screen; put the bar back.
				bar.draw()
			}
			if resp.Done {
				streamDone <- nil
				return
			}
		}
	}()

	select {
	case <-inputDone:
		return nil
	case err := <-streamDone:
		if err != nil && !errors.Is(err, io.EOF) {
			restore()
			return err
		}
		return nil
	}
}

var _ flag.Value = (*stringList)(nil)
