package vterm

import "syscall"

// child is the process swarm launched, and the pseudo-terminal it lives in,
// seen as the five things the rest of this package needs: something to read
// from, something to write to, something that can be told the window changed
// size, something that can be asked to stop, and something that ends.
//
// The two implementations share almost nothing. On Unix it is a forked process
// in a session of its own, holding a pty pair, ended by signalling its process
// group. On Windows it is a process created with a pseudoconsole bound to it at
// creation time — CreatePseudoConsole plus PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
// an attribute os/exec cannot pass — and ended through a job object. That last
// point is why the interface starts at the spawn rather than wrapping a
// *exec.Cmd: on Windows there is no exec.Cmd to wrap.
type child interface {
	// Read returns what the child wrote to its terminal, and ends when the
	// child's side of it is gone.
	Read(p []byte) (int, error)

	// Write sends terminal input to the child.
	Write(p []byte) (int, error)

	// Resize tells the terminal its new geometry, and the child through it.
	Resize(cols, rows int) error

	// Signal asks the whole process tree to stop — wrappers and children
	// included, since agent CLIs are rarely a single process.
	Signal(sig syscall.Signal) error

	// Pid is the child's process id, or 0 once there is none.
	Pid() int

	// Wait blocks until the child has been reaped, and says how it ended.
	Wait() ExitStatus

	// Close releases the terminal, after which Read ends.
	Close() error
}
