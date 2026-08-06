package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

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

func (s *statusBar) draw() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled || s.h < 3 || s.w < 20 {
		return
	}

	label := fmt.Sprintf(" %s — %s detach ", s.agent, s.detach)
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
		return fmt.Errorf("usage: swarm attach <agent>    (detach with the configured key, ctrl+\\ by default)")
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
	detachSeq, err := vterm.KeySequences(keyName)
	if err != nil {
		return fmt.Errorf("detach key %q: %w", keyName, err)
	}

	bar := &statusBar{enabled: !*noStatus, agent: name, detach: keyName}
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
	var restoreOnce sync.Once
	restore := func() {
		restoreOnce.Do(func() {
			bar.clear()
			_ = term.Restore(stdin, oldState)
			// Leave the local terminal in a sane state whatever the agent did:
			// no alternate screen, visible cursor, no scrolling region.
			fmt.Print("\x1b[?1049l\x1b[?25h\x1b[r\x1b[0m\r\n")
		})
	}
	defer restore()

	fmt.Print("\x1b[2J\x1b[H")
	bar.draw()

	// Forward SIGWINCH so the agent follows this window.
	if !*keepSize {
		winch := make(chan os.Signal, 1)
		signal.Notify(winch, syscall.SIGWINCH)
		defer signal.Stop(winch)
		go func() {
			for range winch {
				w, h, err := term.GetSize(os.Stdout.Fd())
				if err != nil {
					continue
				}
				bar.resize(w, h)
				_ = c.Send(ipc.Request{Cols: w, Rows: bar.rows()})
				bar.draw()
			}
		}()
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
				fmt.Print(resp.Text)
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
