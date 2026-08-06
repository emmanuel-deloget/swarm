// Package sockpath decides where a session's control socket lives.
//
// Unix sockets have a hard path limit (108 bytes on Linux), which a project
// checked out a few directories deep blows through easily. When the natural
// path next to the config is too long, the socket moves to the runtime
// directory and a pointer file is left behind so clients still find it.
package sockpath

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxLen is a conservative bound on a Unix socket path. The kernel limit is
// 108 bytes including the terminator; staying under 100 leaves room for the
// ".tmp" suffixes some tools append.
const maxLen = 100

// For returns the socket path for a session, and the pointer file that records
// it when the socket had to be relocated ("" when it did not).
func For(stateDir, session string) (socket, pointer string) {
	if session == "" {
		session = "default"
	}
	natural := filepath.Join(stateDir, session+".sock")
	if len(natural) <= maxLen {
		return natural, ""
	}
	return fallback(stateDir, session), PointerPath(stateDir, session)
}

// PointerPath is the file holding the real socket location.
func PointerPath(stateDir, session string) string {
	if session == "" {
		session = "default"
	}
	return filepath.Join(stateDir, session+".socketpath")
}

func fallback(stateDir, session string) string {
	sum := sha256.Sum256([]byte(stateDir))
	name := fmt.Sprintf("%s-%s.sock", session, hex.EncodeToString(sum[:4]))

	for _, base := range candidateDirs() {
		dir := filepath.Join(base, "swarm")
		p := filepath.Join(dir, name)
		if len(p) <= maxLen {
			return p
		}
	}
	// Nothing short enough: fall back to the shortest thing we can build and
	// let the bind error speak for itself.
	return filepath.Join(os.TempDir(), name)
}

func candidateDirs() []string {
	var dirs []string
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	dirs = append(dirs, os.TempDir(), "/tmp")
	return dirs
}

// WritePointer records where the socket really is, so `swarm ls` run from the
// project directory can find a relocated socket.
func WritePointer(pointer, socket string) error {
	if pointer == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(pointer), 0o755); err != nil {
		return err
	}
	return os.WriteFile(pointer, []byte(socket+"\n"), 0o600)
}

// Resolve returns the socket to connect to for a session: the pointer file's
// target when there is one, otherwise the natural path.
func Resolve(stateDir, session string) string {
	if data, err := os.ReadFile(PointerPath(stateDir, session)); err == nil {
		if p := strings.TrimSpace(string(data)); p != "" {
			return p
		}
	}
	socket, _ := For(stateDir, session)
	return socket
}
