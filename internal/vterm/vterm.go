// Package vterm runs a child process inside a pseudo-terminal and keeps a
// virtual terminal emulator in sync with its output.
//
// The emulator is the source of truth for what the agent's screen looks like:
// it lets swarm render a snapshot at any moment (for the TUI, or to prime a
// freshly connected web client) instead of replaying a raw byte stream that
// may start in the middle of an escape sequence.
package vterm

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// Options configures a Terminal.
type Options struct {
	// Command is the argv to run. Command[0] is looked up in PATH.
	Command []string
	// Dir is the working directory of the child process.
	Dir string
	// Env is the full environment of the child process. When nil the parent
	// environment is used.
	Env []string
	// Cols and Rows size both the pty and the emulator.
	Cols, Rows int
	// Scrollback is how many lines the emulator keeps above the screen.
	Scrollback int
	// OnTitle is called when the child sets the terminal title.
	OnTitle func(string)
	// OnBell is called when the child rings the bell; agents often use it to
	// signal "I need you".
	OnBell func()
	// OnAltScreen is called when the child enters or leaves the alternate
	// screen buffer.
	OnAltScreen func(bool)
	// OnOutput is called with every chunk read from the pty, after the
	// emulator has consumed it. It runs on the reader goroutine and must not
	// block for long.
	OnOutput func([]byte)
	// OnExit is called once, when the child process is reaped.
	OnExit func(status ExitStatus)
	// OnInput, when set, is called with everything written to the child, and
	// where it came from. It is how one answers "did swarm type that?".
	OnInput func(source string, data []byte)
}

// ExitStatus describes how the child process ended.
type ExitStatus struct {
	Code   int
	Signal syscall.Signal
	Err    error
	At     time.Time
}

// String renders the exit status for humans.
func (e ExitStatus) String() string {
	switch {
	case e.Signal != 0:
		return fmt.Sprintf("killed by %s", e.Signal)
	case e.Err != nil && e.Code == 0:
		return e.Err.Error()
	default:
		return fmt.Sprintf("exit %d", e.Code)
	}
}

// Terminal is a child process attached to a pty, mirrored by an emulator.
type Terminal struct {
	opts Options

	cmd *exec.Cmd
	ptm *os.File // pty master

	// emu is created once in Start and never replaced. Its *state* is guarded
	// by mu; the pointer itself is read without locking, which matters for
	// replyReadLoop (see below).
	emu *vt.Emulator

	// mu guards the emulator state, subs and size. Taking it around both a
	// snapshot and a subscription is what makes "snapshot then follow"
	// lossless.
	mu   sync.Mutex
	subs map[uint64]*Subscription
	cols int
	rows int

	// replies carries what the emulator answers to the child. It decouples
	// draining the emulator from writing to the pty: the emulator's internal
	// pipe is unbuffered, so a reader that is not always ready would stall
	// emu.Write — which runs while mu is held.
	replies chan []byte

	nextSub  uint64
	bytesOut atomic.Uint64
	lastOut  atomic.Int64 // unix nanos of the last byte read
	exited   atomic.Bool
	status   atomic.Pointer[ExitStatus]
	altOn    atomic.Bool
	// strOpen, strBuf and strEsc carry a string sequence across chunk
	// boundaries while oscSafe decides whether the parser may have it.
	strOpen bool
	strEsc  bool
	strBuf  []byte
	// syncOn is DECSET 2026: the child is drawing a frame. A resize waits for
	// the end of it, and pending holds what it will become.
	syncOn      atomic.Bool
	pendingMu   sync.Mutex
	pendingCols int
	pendingRows int
	pendingAt   time.Time

	curVisible atomic.Bool
	bracketed  atomic.Bool
	stopping   atomic.Bool

	// focused is what swarm tells the agent about the focus of the window
	// showing it. It starts true, and that is a deliberate policy rather than a
	// simplification: swarm renders every agent all the time — in the grid, in
	// the web page, in `swarm screen` — so an agent that believed itself
	// unfocused would draw a degraded interface nobody asked for.
	focused atomic.Bool

	// focusPending records that the application has just enabled focus
	// reporting and is owed the current state.
	focusPending atomic.Bool

	// modeCarry holds the tail of a mode sequence split across two reads. It
	// is only touched from the reader goroutine.
	modeCarry []byte

	// ptmMu guards every use of the pty master, including closing it: an ioctl
	// racing the close would touch a descriptor that may already have been
	// reused.
	ptmMu     sync.Mutex
	ptmClosed bool

	// readerDone is closed once readLoop has drained the pty to EOF. Waiting
	// for it before tearing anything down is what keeps a dying agent's last
	// words — usually the error that killed it.
	readerDone chan struct{}
	done       chan struct{}
}

// Start launches the command in a new pty.
func Start(o Options) (*Terminal, error) {
	if len(o.Command) == 0 {
		return nil, errors.New("vterm: empty command")
	}
	if o.Cols <= 0 {
		o.Cols = 120
	}
	if o.Rows <= 0 {
		o.Rows = 40
	}
	if o.Scrollback <= 0 {
		o.Scrollback = 2000
	}

	t := &Terminal{
		opts:    o,
		subs:    make(map[uint64]*Subscription),
		cols:    o.Cols,
		rows:    o.Rows,
		replies: make(chan []byte, 64),

		readerDone: make(chan struct{}),
		done:       make(chan struct{}),
	}

	emu := vt.NewEmulator(o.Cols, o.Rows)
	emu.SetScrollbackSize(o.Scrollback)
	emu.SetCallbacks(vt.Callbacks{
		Title: func(s string) {
			if o.OnTitle != nil {
				o.OnTitle(s)
			}
		},
		Bell: func() {
			if o.OnBell != nil {
				o.OnBell()
			}
		},
		AltScreen: func(on bool) {
			t.altOn.Store(on)
			if o.OnAltScreen != nil {
				o.OnAltScreen(on)
			}
		},
		CursorVisibility: func(visible bool) { t.curVisible.Store(visible) },
	})
	t.emu = emu
	t.curVisible.Store(true)
	t.focused.Store(true)

	cmd := exec.Command(o.Command[0], o.Command[1:]...)
	cmd.Dir = o.Dir
	cmd.Env = o.Env
	// A session leader with the pty as controlling terminal: this is what
	// makes job control, SIGINT-on-^C and terminal queries work.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}

	ptm, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(o.Cols), Rows: uint16(o.Rows)})
	if err != nil {
		return nil, fmt.Errorf("vterm: start %s: %w", o.Command[0], err)
	}
	t.cmd = cmd
	t.ptm = ptm
	t.lastOut.Store(time.Now().UnixNano())

	go t.readLoop()
	go t.replyReadLoop()
	go t.replyWriteLoop()
	go t.waitLoop()
	return t, nil
}

// readLoop feeds pty output into the emulator and to the subscribers. It owns
// the pty master: closing it anywhere else would race with this read and throw
// away whatever the child wrote last.
func (t *Terminal) readLoop() {
	defer func() {
		close(t.readerDone)
		t.closePTM()
	}()
	buf := make([]byte, 32*1024)
	for {
		n, err := t.ptm.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			t.consume(chunk)
		}
		if err != nil {
			// The pty master reports EIO once the slave side is gone, which
			// is the normal way a child's terminal ends on Linux.
			return
		}
	}
}

func (t *Terminal) consume(chunk []byte) {
	t.scanModes(chunk)

	// The emulator gets what its parser can handle; a subscriber is a real
	// terminal on the other end, which has no such trouble and should see the
	// output exactly as the agent wrote it.
	safe := t.oscSafe(chunk)

	t.mu.Lock()
	_, _ = t.emu.Write(safe)
	for _, s := range t.subs {
		s.push(chunk)
	}
	// After the write, never before: the emulator drops a focus event unless it
	// has already parsed the sequence that enabled the mode.
	if t.focusPending.Swap(false) {
		t.sendFocusLocked()
	}
	t.mu.Unlock()

	t.applyPendingResize()

	t.bytesOut.Add(uint64(len(chunk)))
	t.lastOut.Store(time.Now().UnixNano())
	if t.opts.OnOutput != nil {
		t.opts.OnOutput(chunk)
	}
}

// replyReadLoop drains the emulator's answers (device attributes, cursor
// position reports, in-band resize notifications). Without this, agent CLIs
// that probe their terminal hang waiting for a reply.
//
// Two constraints shape this loop. The emulator writes those answers from
// inside emu.Write, which runs while mu is held, so this loop must never wait
// on mu — it reads t.emu without locking and drops replies rather than block on
// a full queue. And the emulator's pipe is unbuffered, so the only way out is
// through a Read: wakeReplyReader pushes a sentinel byte to get us there.
func (t *Terminal) replyReadLoop() {
	buf := make([]byte, 1024)
	for {
		n, err := t.emu.Read(buf)
		if err != nil {
			return
		}
		if n == 0 {
			continue
		}
		if t.stopping.Load() {
			// The sentinel byte from wakeReplyReader: the child is gone.
			return
		}
		chunk := make([]byte, n)
		copy(chunk, buf[:n])
		select {
		case t.replies <- chunk:
		default:
			// The child is not reading its input and the queue is full.
			// Dropping a terminal report beats stalling the emulator.
		}
	}
}

// wakeReplyReader unblocks replyReadLoop, which is parked in emu.Read.
//
// Writing to the emulator's input pipe is the only way to get it moving:
// closing the emulator from here would race with the read. io.Pipe accepts
// concurrent writers, and the loop is guaranteed to consume this byte because
// it never exits without going through a Read first.
func (t *Terminal) wakeReplyReader() {
	t.stopping.Store(true)
	go func() { _, _ = t.emu.InputPipe().Write([]byte{0}) }()
}

// replyWriteLoop forwards drained replies to the child.
func (t *Terminal) replyWriteLoop() {
	for {
		select {
		case chunk := <-t.replies:
			// Labelled, because a terminal reply landing in an agent's prompt
			// is exactly the kind of thing one needs to be able to rule out.
			if t.opts.OnInput != nil {
				t.opts.OnInput("terminal-reply", chunk)
			}
			if _, err := t.write(chunk); err != nil {
				return
			}
		case <-t.done:
			return
		}
	}
}

func (t *Terminal) waitLoop() {
	err := t.cmd.Wait()
	st := ExitStatus{At: time.Now(), Err: err}
	if t.cmd.ProcessState != nil {
		st.Code = t.cmd.ProcessState.ExitCode()
		if ws, ok := t.cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			st.Signal = ws.Signal()
		}
	}
	// Let the reader finish draining before anything is torn down, so the
	// screen shows the child's final output. It ends on its own when the pty
	// reports EIO; the timeout is only there so a stuck reader cannot wedge
	// the shutdown.
	select {
	case <-t.readerDone:
	case <-time.After(2 * time.Second):
	}

	t.status.Store(&st)
	t.exited.Store(true)
	close(t.done)
	t.wakeReplyReader()

	t.mu.Lock()
	for id, s := range t.subs {
		s.close()
		delete(t.subs, id)
	}
	t.mu.Unlock()

	if t.opts.OnExit != nil {
		t.opts.OnExit(st)
	}
}

// Write sends raw bytes to the child's terminal input. This is the injection
// primitive: everything else (text, keys, pastes) is built on top of it.
func (t *Terminal) Write(p []byte) (int, error) {
	return t.WriteSource("input", p)
}

// WriteSource is Write with a label for the input log, so a recorded byte can
// be traced back to an injection, a key press, or the emulator answering a
// query on the child's behalf.
func (t *Terminal) WriteSource(source string, p []byte) (int, error) {
	if t.exited.Load() {
		return 0, ErrExited
	}
	if t.opts.OnInput != nil && len(p) > 0 {
		t.opts.OnInput(source, p)
	}
	return t.write(p)
}

func (t *Terminal) write(p []byte) (int, error) {
	t.ptmMu.Lock()
	defer t.ptmMu.Unlock()
	if t.ptmClosed {
		return 0, ErrExited
	}
	return t.ptm.Write(p)
}

// closePTM closes the pty master exactly once.
func (t *Terminal) closePTM() {
	t.ptmMu.Lock()
	defer t.ptmMu.Unlock()
	if t.ptmClosed {
		return
	}
	t.ptmClosed = true
	_ = t.ptm.Close()
}

// setSize applies the window size to the pty, unless it is already closed.
func (t *Terminal) setSize(cols, rows int) error {
	t.ptmMu.Lock()
	defer t.ptmMu.Unlock()
	if t.ptmClosed {
		return nil
	}
	return pty.Setsize(t.ptm, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// ErrExited is returned when writing to a terminal whose child is gone.
var ErrExited = errors.New("vterm: process has exited")

// maxResizeHold bounds how long a resize waits for a frame to end. A child that
// leaves DECSET 2026 on — because it crashed mid-frame, or never turns it off —
// must not be able to freeze the geometry for good.
const maxResizeHold = 250 * time.Millisecond

// Resize changes the pty window size and the emulator geometry.
//
// A frame is computed against a geometry. A child brackets its frame with
// DECSET 2026 and then writes absolute positions worked out from the height it
// believes in; resizing the emulator halfway through leaves the rest of that
// frame addressing a screen that no longer exists, and what it meant to put on
// line 34 of 50 lands wherever line 27 of 27 clamps it — in the prompt, as far
// as anyone watching is concerned. So a resize arriving mid-frame waits.
func (t *Terminal) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return fmt.Errorf("vterm: invalid size %dx%d", cols, rows)
	}
	// Nothing to do, and nothing to hold: the pane, an attach and the web page
	// all resize the same agent without knowing what the others asked for, so
	// the same geometry arrives repeatedly. The kernel would not send SIGWINCH
	// for it either — this just stops the emulator being reworked for nothing.
	if c, r := t.Size(); c == cols && r == rows {
		return nil
	}
	if t.syncOn.Load() {
		t.pendingMu.Lock()
		t.pendingCols, t.pendingRows = cols, rows
		t.pendingAt = time.Now()
		t.pendingMu.Unlock()
		// The end of the frame is what normally releases it, on the next chunk
		// read. This timer is for the child that says nothing more: a held
		// resize must still land.
		time.AfterFunc(maxResizeHold, t.applyPendingResize)
		return nil
	}
	return t.resizeNow(cols, rows)
}

// resizeNow applies a geometry, emulator first and then the pty — the child is
// told last, so nothing it has already written is read against a screen it did
// not have.
func (t *Terminal) resizeNow(cols, rows int) error {
	// Recorded like anything else swarm sends the child, because that is what a
	// resize is: SIGWINCH, and a full redraw computed against a new geometry.
	// Reading an input log without it leaves the redraws unexplained.
	if t.opts.OnInput != nil {
		t.opts.OnInput("resize", []byte(fmt.Sprintf("%dx%d", cols, rows)))
	}
	t.mu.Lock()
	// Shrinking the height: the emulator's buffer truncates from the bottom,
	// which throws away the most recent output — the opposite of what a
	// terminal does. Scroll up just enough to keep the cursor on screen first;
	// the emulator's own scroll path pushes the displaced lines into the
	// scrollback, so nothing is lost. The alternate screen has no scrollback
	// and is redrawn by the application, so leave it alone.
	if rows < t.rows && !t.altOn.Load() {
		pos := t.emu.CursorPosition()
		if need := pos.Y - (rows - 1); need > 0 {
			t.scrollUp(need)
		}
	}
	t.cols, t.rows = cols, rows
	t.emu.Resize(cols, rows)
	t.mu.Unlock()
	return t.setSize(cols, rows)
}

// Size returns the current geometry.
func (t *Terminal) Size() (cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cols, t.rows
}

// Render returns the current screen as a string with ANSI styling, ready to be
// printed inside a TUI pane or pushed to a browser terminal.
func (t *Terminal) Render() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.emu.Render()
}

// Text returns the current screen as plain text, without styling. Useful for
// pattern matching and for logs.
func (t *Terminal) Text() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.emu.String()
}

// Cursor returns the cursor position and visibility.
func (t *Terminal) Cursor() (x, y int, visible bool) {
	t.mu.Lock()
	pos := t.emu.CursorPosition()
	t.mu.Unlock()
	return pos.X, pos.Y, t.curVisible.Load()
}

// AltScreen reports whether the child is using the alternate screen.
func (t *Terminal) AltScreen() bool { return t.altOn.Load() }

// Pid returns the child process id, or 0 if it is gone.
func (t *Terminal) Pid() int {
	if t.cmd == nil || t.cmd.Process == nil {
		return 0
	}
	return t.cmd.Process.Pid
}

// Exited reports whether the child has been reaped.
func (t *Terminal) Exited() bool { return t.exited.Load() }

// Status returns the exit status, or nil while the child is alive.
func (t *Terminal) Status() *ExitStatus { return t.status.Load() }

// Done is closed when the child has been reaped.
func (t *Terminal) Done() <-chan struct{} { return t.done }

// BytesOut is the total number of bytes the child has written.
func (t *Terminal) BytesOut() uint64 { return t.bytesOut.Load() }

// LastOutput is when the child last wrote something.
func (t *Terminal) LastOutput() time.Time { return time.Unix(0, t.lastOut.Load()) }

// Signal sends a signal to the child's process group, so that a whole tool
// tree (shell wrappers, node children) gets it and not just the leader.
func (t *Terminal) Signal(sig syscall.Signal) error {
	if t.cmd == nil || t.cmd.Process == nil {
		return ErrExited
	}
	pid := t.cmd.Process.Pid
	if err := syscall.Kill(-pid, sig); err != nil {
		return syscall.Kill(pid, sig)
	}
	return nil
}

// Stop asks the child to quit, then kills it if it outstays the grace period.
func (t *Terminal) Stop(grace time.Duration) error {
	if t.exited.Load() {
		return nil
	}
	if err := t.Signal(syscall.SIGTERM); err != nil && !t.exited.Load() {
		return err
	}
	select {
	case <-t.done:
		return nil
	case <-time.After(grace):
		return t.Signal(syscall.SIGKILL)
	}
}

// Subscription is a live feed of raw pty output, primed with a snapshot of the
// screen as it was when the subscription started.
type Subscription struct {
	// Snapshot is the ANSI rendering of the screen at subscription time.
	Snapshot string

	t  *Terminal
	id uint64

	mu       sync.Mutex
	buf      [][]byte
	size     int
	max      int
	closed   bool
	overflow bool
	notify   chan struct{}
}

// Subscribe returns a snapshot of the screen plus a feed of everything written
// after it. maxBuffer bounds the pending bytes; a subscriber that falls behind
// is marked overflowed and told to resynchronise rather than being fed a
// truncated stream.
func (t *Terminal) Subscribe(maxBuffer int) *Subscription {
	if maxBuffer <= 0 {
		maxBuffer = 1 << 20
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextSub++
	s := &Subscription{
		Snapshot: t.emu.Render(),
		t:        t,
		id:       t.nextSub,
		max:      maxBuffer,
		notify:   make(chan struct{}, 1),
	}
	if t.exited.Load() {
		s.closed = true
		return s
	}
	t.subs[s.id] = s
	return s
}

// Close detaches the subscription.
func (s *Subscription) Close() {
	s.t.mu.Lock()
	delete(s.t.subs, s.id)
	s.t.mu.Unlock()
	s.close()
}

func (s *Subscription) close() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
	}
	s.mu.Unlock()
	s.wake()
}

func (s *Subscription) push(chunk []byte) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if s.overflow {
		s.mu.Unlock()
		return
	}
	if s.size+len(chunk) > s.max {
		// Drop what we have: the reader will pull a fresh snapshot instead of
		// applying a stream with a hole in it.
		s.buf = nil
		s.size = 0
		s.overflow = true
	} else {
		s.buf = append(s.buf, chunk)
		s.size += len(chunk)
	}
	s.mu.Unlock()
	s.wake()
}

func (s *Subscription) wake() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// Next blocks until output is available, the subscription overflows, or it is
// closed. It returns the pending bytes; resync is true when the caller must
// discard its state and start again from Resnapshot.
func (s *Subscription) Next() (data []byte, resync bool, err error) {
	for {
		s.mu.Lock()
		switch {
		case s.overflow:
			s.overflow = false
			s.mu.Unlock()
			return nil, true, nil
		case len(s.buf) > 0:
			out := s.buf
			s.buf = nil
			s.size = 0
			s.mu.Unlock()
			return flatten(out), false, nil
		case s.closed:
			s.mu.Unlock()
			return nil, false, io.EOF
		}
		s.mu.Unlock()
		<-s.notify
	}
}

// Resnapshot returns a fresh screen rendering after an overflow.
func (s *Subscription) Resnapshot() string { return s.t.Render() }

func flatten(chunks [][]byte) []byte {
	n := 0
	for _, c := range chunks {
		n += len(c)
	}
	out := make([]byte, 0, n)
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out
}

// applyPendingResize lets a held resize through once the frame it would have
// cut into is over — or once it has waited long enough that a child which never
// clears DECSET 2026 stops being able to hold the geometry hostage.
func (t *Terminal) applyPendingResize() {
	t.pendingMu.Lock()
	cols, rows := t.pendingCols, t.pendingRows
	if cols == 0 || (t.syncOn.Load() && time.Since(t.pendingAt) < maxResizeHold) {
		t.pendingMu.Unlock()
		return
	}
	t.pendingCols, t.pendingRows = 0, 0
	t.pendingMu.Unlock()

	if c, r := t.Size(); c == cols && r == rows {
		return
	}
	_ = t.resizeNow(cols, rows)
}
